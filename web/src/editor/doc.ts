// Doc is the in-memory copy of a file. The editor never works on the pod stream directly:
// base = content at load (or last merged remote), text = local buffer, conflict.remote = new
// version pushed by the pod that could not be merged. One Doc per path, shared by every view.
import { createSignal, type Accessor, type Setter } from 'solid-js'
import { detectIndent, type Indent } from './indent'

export interface Change {
  from: number
  to: number // end in the old text
  text: string
  removed: string
  fromLine: number
  oldLines: number
  newLines: number
  origin: unknown
}

export interface Selection {
  anchor: number
  head: number
}

interface Step {
  from: number
  removed: string
  inserted: string
  selBefore: Selection
  selAfter: Selection
  time: number
}

/** Encoding and line separator of the file on disk (the text itself is UTF-8 with LF). */
export interface FileFormat {
  encoding: string
  eol: 'lf' | 'crlf'
}

export interface Conflict {
  remote: string
  rev: number
}

export class Doc {
  text: string
  lineStarts: number[] = [0]
  version = 0
  readOnly: boolean
  base: string
  baseRev: number
  lang: string
  private listeners = new Set<(c: Change) => void>()
  private undoStack: Step[][] = []
  private redoStack: Step[][] = []
  private lastStepTime = 0
  private grouping = true
  private txDepth = 0
  private txGroup: Step[] | null = null

  readonly dirty: Accessor<boolean>
  private setDirty: Setter<boolean>
  readonly conflict: Accessor<Conflict | null>
  readonly setConflict: Setter<Conflict | null>
  readonly deleted: Accessor<boolean>
  readonly setDeleted: Setter<boolean>
  /** Bumped on each change, for reactive consumers (status bar, outline). */
  readonly changed: Accessor<number>
  private setChanged: Setter<number>
  readonly format: Accessor<FileFormat>
  readonly setFormat: Setter<FileFormat>
  /** Indentation of the file (detected, or chosen in the status bar); null: the settings. */
  readonly indent: Accessor<Indent | null>
  private setIndent: Setter<Indent | null>
  /** The indentation was chosen by the user, not detected. */
  indentChosen = false

  constructor(public path: string, text: string, opts: { base?: string; rev?: number; readOnly?: boolean; lang: string; format?: FileFormat }) {
    this.text = text
    this.base = opts.base ?? text
    this.baseRev = opts.rev ?? 0
    this.readOnly = !!opts.readOnly
    this.lang = opts.lang
    this.reindex()
    ;[this.dirty, this.setDirty] = createSignal(this.text !== this.base)
    ;[this.conflict, this.setConflict] = createSignal<Conflict | null>(null)
    ;[this.deleted, this.setDeleted] = createSignal(false)
    ;[this.changed, this.setChanged] = createSignal(0)
    ;[this.format, this.setFormat] = createSignal<FileFormat>(opts.format ?? { encoding: 'utf-8', eol: 'lf' })
    ;[this.indent, this.setIndent] = createSignal<Indent | null>(detectIndent(text))
  }

  private reindex() {
    const starts = [0]
    const t = this.text
    for (let i = t.indexOf('\n'); i >= 0; i = t.indexOf('\n', i + 1)) starts.push(i + 1)
    this.lineStarts = starts
  }

  get lineCount() {
    return this.lineStarts.length
  }

  lineStart(line: number) {
    return this.lineStarts[Math.max(0, Math.min(line, this.lineStarts.length - 1))]
  }

  lineEnd(line: number) {
    return line + 1 < this.lineStarts.length ? this.lineStarts[line + 1] - 1 : this.text.length
  }

  lineText(line: number) {
    return this.text.slice(this.lineStart(line), this.lineEnd(line))
  }

  /** Line of an offset (binary search). */
  lineAt(offset: number) {
    const s = this.lineStarts
    let lo = 0
    let hi = s.length - 1
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1
      if (s[mid] <= offset) lo = mid
      else hi = mid - 1
    }
    return lo
  }

  pos(offset: number) {
    const line = this.lineAt(offset)
    return { line, col: offset - this.lineStarts[line] }
  }

  offset(line: number, col: number) {
    if (line >= this.lineStarts.length) return this.text.length
    return Math.min(this.lineStart(line) + Math.max(0, col), this.lineEnd(line))
  }

  onChange(f: (c: Change) => void) {
    this.listeners.add(f)
    return () => this.listeners.delete(f)
  }

  /** Replaces [from, to) with text. Every edit goes through here. */
  replace(from: number, to: number, text: string, origin: unknown, opts: { undoable?: boolean; selBefore?: Selection; selAfter?: Selection } = {}) {
    from = Math.max(0, Math.min(from, this.text.length))
    to = Math.max(from, Math.min(to, this.text.length))
    const removed = this.text.slice(from, to)
    if (removed === text) return
    const fromLine = this.lineAt(from)
    const toLine = this.lineAt(to)
    this.text = this.text.slice(0, from) + text + this.text.slice(to)
    // Line index update: replace the starts inside the edit, shift the following ones.
    const inserted: number[] = []
    for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', i + 1)) inserted.push(from + i + 1)
    // Rebuilt by copy: spreading a huge array into push() would overflow the stack.
    const delta = text.length - (to - from)
    const ls = this.lineStarts
    const out = new Array<number>(fromLine + 1 + inserted.length + ls.length - toLine - 1)
    let k = 0
    for (let i = 0; i <= fromLine; i++) out[k++] = ls[i]
    for (const p of inserted) out[k++] = p
    for (let i = toLine + 1; i < ls.length; i++) out[k++] = ls[i] + delta
    this.lineStarts = out
    this.version++

    if (opts.undoable !== false) {
      const now = performance.now()
      const step: Step = {
        from,
        removed,
        inserted: text,
        selBefore: opts.selBefore ?? { anchor: to, head: to },
        selAfter: opts.selAfter ?? { anchor: from + text.length, head: from + text.length },
        time: now,
      }
      const group = this.undoStack[this.undoStack.length - 1]
      const prev = group?.[group.length - 1]
      if (this.txDepth > 0) {
        // Inside transact(): every step joins the same undo group.
        if (this.txGroup) this.txGroup.push(step)
        else this.undoStack.push((this.txGroup = [step]))
        this.redoStack = []
        this.lastStepTime = now
      } else {
      // Typing in a row (same place, less than a second apart) is undone in one step.
      const typing = prev && this.grouping && now - this.lastStepTime < 1000 && !text.includes('\n') && text.length <= 1 && removed.length <= 1 &&
        (prev.from + prev.inserted.length === from || from + removed.length === prev.from)
      if (typing) group.push(step)
      else this.undoStack.push([step])
      if (this.undoStack.length > 500) this.undoStack.shift()
      this.redoStack = []
      this.lastStepTime = now
      this.grouping = true
      }
    }
    const change: Change = { from, to, text, removed, fromLine, oldLines: toLine - fromLine + 1, newLines: inserted.length + 1, origin }
    for (const l of this.listeners) l(change)
    this.setDirty(this.text !== this.base)
    this.setChanged((n) => n + 1)
  }

  /** Runs several edits as one undo step (completion with imports, rename, formatting). */
  transact<T>(fn: () => T): T {
    if (this.txDepth === 0) this.txGroup = null
    this.txDepth++
    try {
      return fn()
    } finally {
      this.txDepth--
      if (this.txDepth === 0) {
        this.txGroup = null
        this.grouping = false
      }
    }
  }

  /**
   * Applies LSP-style edits given as offsets of the current text, as one undo step.
   * Returns a function mapping an offset of the text before the edits to the text after.
   */
  applyEdits(edits: { from: number; to: number; text: string }[], origin: unknown): (p: number) => number {
    const sorted = [...edits].sort((a, b) => b.from - a.from || b.to - a.to)
    this.transact(() => {
      for (const e of sorted) this.replace(e.from, e.to, e.text, origin)
    })
    const asc = [...edits].sort((a, b) => a.from - b.from)
    return (p) => {
      let delta = 0
      for (const e of asc) {
        if (e.to <= p) delta += e.text.length - (e.to - e.from)
        else if (e.from < p) return e.from + e.text.length + delta
        else break
      }
      return p + delta
    }
  }

  /** Indentation chosen for this file (status bar, session restore). */
  chooseIndent(i: Indent) {
    this.indentChosen = true
    this.setIndent(i)
  }

  /** Breaks the current undo group (cursor moved, focus lost...). */
  breakUndoGroup() {
    this.grouping = false
  }

  /** Replaces the whole text with the smallest single edit (common prefix and suffix kept). */
  setText(text: string, origin: unknown, undoable = true) {
    const old = this.text
    if (old === text) return
    let a = 0
    const max = Math.min(old.length, text.length)
    while (a < max && old.charCodeAt(a) === text.charCodeAt(a)) a++
    let b = 0
    while (b < max - a && old.charCodeAt(old.length - 1 - b) === text.charCodeAt(text.length - 1 - b)) b++
    this.grouping = false
    this.replace(a, old.length - b, text.slice(a, text.length - b), origin, { undoable })
    this.grouping = false
  }

  private applySteps(steps: Step[], undo: boolean, origin: unknown): Selection | null {
    const list = undo ? [...steps].reverse() : steps
    let sel: Selection | null = null
    for (const s of list) {
      const [del, ins] = undo ? [s.inserted, s.removed] : [s.removed, s.inserted]
      this.replace(s.from, s.from + del.length, ins, origin, { undoable: false })
      sel = undo ? s.selBefore : s.selAfter
    }
    this.setDirty(this.text !== this.base)
    return sel
  }

  undo(origin: unknown): Selection | null {
    const g = this.undoStack.pop()
    if (!g) return null
    this.redoStack.push(g)
    this.grouping = false
    return this.applySteps(g, true, origin)
  }

  redo(origin: unknown): Selection | null {
    const g = this.redoStack.pop()
    if (!g) return null
    this.undoStack.push(g)
    this.grouping = false
    return this.applySteps(g, false, origin)
  }

  /** The file was saved, or a remote version merged: base moves. */
  setBase(text: string, rev: number) {
    this.base = text
    this.baseRev = rev
    this.setDirty(this.text !== this.base)
  }
}
