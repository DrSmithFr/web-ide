// EditorView renders a Doc in a contenteditable <pre>. The text is split into blocks of a
// few dozen lines (one <div> and one text node each): an edit only lays out its own block,
// which keeps typing fast in files of several MB. Every edit is intercepted (beforeinput),
// applied to the Doc, then mirrored into the block with replaceData, which keeps the node
// and the caret in place. Colors come from the CSS Custom Highlight API: one Highlight per
// token type, no <span> in the DOM. Only the visible lines (plus a margin) get ranges,
// rebuilt on the next frame after a change or a scroll. The gutter is virtual too.
import { Highlighter, type Token } from './tokenizer'
import { grammar } from './languages'
import { subwordLeft, subwordRight } from './subword'
import { Folder, type Fold } from './folding'
import { normalize, nextOccurrence, occurrences, selFrom, selTo, wordAt, wordLeft, wordRight } from './carets'
import type { Change, Doc, Selection } from './doc'
import type { LineMark } from './linediff'

export interface Diagnostic {
  from: number
  to: number
  severity: 'error' | 'warning' | 'info'
  message: string
  source?: string
}

export interface ViewOptions {
  tabSize: number
  insertSpaces: boolean
  highlightLine: boolean
  indentGuides?: boolean
  showWhitespace?: boolean
  readOnly?: boolean
  onSelection?: (sel: Selection) => void
  onCtrlClick?: (offset: number) => void
  onFocus?: () => void
  onScroll?: (top: number) => void
  /** Sees the keys first (completion list); returning true consumes the key. */
  onKey?: (e: KeyboardEvent) => boolean
  /** After a keystroke edit: the typed text, or '' for a deletion. */
  onType?: (text: string) => void
  /** The folded ranges changed. */
  onFolds?: () => void
}

const registry = () => (CSS as any).highlights as Map<string, any> | undefined

function highlight(name: string, priority = 0): any {
  const reg = registry()
  if (!reg) return null
  let h = reg.get(name)
  if (!h) {
    h = new (window as any).Highlight()
    h.priority = priority
    reg.set(name, h)
  }
  return h
}

const commentPrefix: Record<string, string> = {
  php: '//', javascript: '//', typescript: '//', go: '//', json: '//', css: '//', python: '#', shell: '#', yaml: '#',
  nginx: '#', sql: '--', redis: '#',
}

/** Lines per block, and the size above which a block is split again. */
const BLOCK = 64
const MAX_BLOCK = 160
/** Lines rendered (highlights, gutter) around the visible ones. */
const MARGIN = 40

interface Block {
  el: HTMLDivElement
  text: Text
  lines: number
  index: number
}

interface Spans {
  list: [number, number][]
  priority: number
}

let viewSeq = 0

export class EditorView {
  readonly id = ++viewSeq
  readonly root: HTMLDivElement
  readonly scroller: HTMLDivElement
  readonly content: HTMLPreElement
  private gutter: HTMLDivElement
  private gutterNums: HTMLPreElement
  private gutterMarks: HTMLDivElement
  private gutterFolds: HTMLDivElement
  private placeholders: HTMLDivElement
  private folder!: Folder
  /** Folded ranges, by header line. */
  private folds: Fold[] = []
  /** Lines hidden by the folds: sorted, disjoint [first, last] ranges. */
  private hidden: [number, number][] = []
  private foldable = { version: -1, lines: new Map<number, boolean>() }
  private marks = new Map<number, LineMark>()
  private curLine: HTMLDivElement
  private boxes: HTMLDivElement
  private guides: HTMLDivElement
  private ws: HTMLPreElement
  private caretLayer: HTMLDivElement
  /** Other selections (multiple carets), in the order they were added; the primary one is the DOM selection. */
  private extra: Selection[] = []
  /** The occurrences added by Alt+J started from the word at the caret: whole words only. */
  private wholeWord = false
  /** End of a middle button drag: the paste of the primary selection that follows is dropped. */
  private pasteGuard = 0
  private guideLine = -1
  private step: { version: number; tabSize: number; cols: number } | null = null
  private tooltip: HTMLDivElement
  private blocks: Block[] = []
  private blockStarts: number[] | null = null
  private blockOf = new WeakMap<Node, Block>()
  private hl: Highlighter
  private own = new Map<string, AbstractRange[]>()
  private spans = new Map<string, Spans>()
  private disposers: (() => void)[] = []
  private frame = 0
  private composing = false
  private lastSel: Selection = { anchor: 0, head: 0 }
  private diagnostics: Diagnostic[] = []
  private statement: [number, number] | null = null
  lineHeight = 20
  charWidth = 8
  private padTop = 6

  constructor(public doc: Doc, private opts: ViewOptions) {
    this.root = document.createElement('div')
    this.root.className = 'ed'
    this.scroller = document.createElement('div')
    this.scroller.className = 'ed-scroll'
    const inner = document.createElement('div')
    inner.className = 'ed-inner'
    this.gutter = document.createElement('div')
    this.gutter.className = 'ed-gutter'
    this.gutter.setAttribute('aria-hidden', 'true')
    this.gutterNums = document.createElement('pre')
    this.gutterNums.className = 'ed-gutter-nums'
    this.gutterMarks = document.createElement('div')
    this.gutterMarks.className = 'ed-marks'
    this.gutterFolds = document.createElement('div')
    this.gutterFolds.className = 'ed-folds'
    this.gutter.append(this.gutterNums, this.gutterMarks, this.gutterFolds)
    this.placeholders = document.createElement('div')
    this.placeholders.className = 'ed-placeholders'
    const main = document.createElement('div')
    main.className = 'ed-main'
    this.curLine = document.createElement('div')
    this.curLine.className = 'ed-curline'
    this.boxes = document.createElement('div')
    this.boxes.className = 'ed-boxes'
    this.guides = document.createElement('div')
    this.guides.className = 'ed-guides'
    this.ws = document.createElement('pre')
    this.ws.className = 'ed-ws'
    this.ws.setAttribute('aria-hidden', 'true')
    this.caretLayer = document.createElement('div')
    this.caretLayer.className = 'ed-carets'
    this.content = document.createElement('pre')
    this.content.className = 'ed-content'
    this.content.spellcheck = false
    this.content.setAttribute('autocapitalize', 'off')
    this.content.setAttribute('autocorrect', 'off')
    this.content.setAttribute('role', 'textbox')
    this.content.setAttribute('aria-multiline', 'true')
    this.setReadOnly(!!opts.readOnly || doc.readOnly)
    this.tooltip = document.createElement('div')
    this.tooltip.className = 'ed-tooltip'
    main.append(this.curLine, this.guides, this.boxes, this.content, this.ws, this.caretLayer, this.placeholders)
    inner.append(this.gutter, main)
    this.scroller.append(inner)
    this.root.append(this.scroller, this.tooltip)
    this.root.style.setProperty('--tab-size', String(opts.tabSize))
    this.buildAll()

    this.hl = new Highlighter(grammar(doc.lang), (i) => doc.lineText(i), () => doc.lineCount)
    this.folder = new Folder(doc, this.hl, () => this.opts.tabSize)

    this.disposers.push(doc.onChange((c) => this.onDocChange(c)))
    const listen = <K extends keyof HTMLElementEventMap>(el: HTMLElement | Document, ev: K, f: (e: HTMLElementEventMap[K]) => void, o?: AddEventListenerOptions) => {
      el.addEventListener(ev, f as EventListener, o)
      this.disposers.push(() => el.removeEventListener(ev, f as EventListener, o))
    }
    listen(this.content, 'beforeinput', (e) => this.onBeforeInput(e as InputEvent))
    listen(this.content, 'input', () => this.syncFromDom())
    listen(this.content, 'compositionstart', () => (this.composing = true))
    listen(this.content, 'compositionend', () => {
      this.composing = false
      this.syncFromDom()
    })
    listen(this.content, 'keydown', (e) => this.onKeyDown(e))
    listen(this.content, 'copy', (e) => this.onCopy(e, false))
    listen(this.content, 'cut', (e) => this.onCopy(e, true))
    listen(this.content, 'paste', (e) => this.onPaste(e))
    listen(this.content, 'focus', () => opts.onFocus?.())
    listen(this.content, 'blur', () => doc.breakUndoGroup())
    listen(this.content, 'click', (e) => {
      if ((e.ctrlKey || e.metaKey) && opts.onCtrlClick) {
        e.preventDefault()
        opts.onCtrlClick(this.getSelection().head)
      }
    })
    listen(this.content, 'mousedown', (e) => this.onMouseDown(e))
    listen(this.content, 'auxclick', (e) => e.button === 1 && e.preventDefault())
    listen(this.content, 'mousemove', (e) => this.onHover(e))
    // Fold markers of the gutter and placeholders of the folded ranges.
    const foldClick = (e: MouseEvent) => {
      const el = (e.target as HTMLElement).closest<HTMLElement>('[data-fold]')
      if (!el || e.button !== 0) return
      e.preventDefault()
      this.toggleFold(Number(el.dataset.fold))
    }
    listen(this.gutterFolds, 'mousedown', foldClick)
    listen(this.placeholders, 'mousedown', foldClick)
    listen(this.content, 'mouseleave', () => (this.tooltip.style.display = 'none'))
    listen(document as any, 'selectionchange', () => this.onSelectionChange())
    listen(this.scroller, 'scroll', () => {
      this.schedule()
      opts.onScroll?.(this.scroller.scrollTop)
    }, { passive: true })
    const ro = new ResizeObserver(() => this.schedule())
    ro.observe(this.scroller)
    this.disposers.push(() => ro.disconnect())
    this.schedule()
  }

  // ---------- blocks ----------

  private blockText(first: number, lines: number, last: boolean) {
    const d = this.doc
    const a = d.lineStart(first)
    // Each block ends with the newline of its last line; the last block gets an extra one,
    // without which the browser would not show an empty last line.
    return last ? d.text.slice(a) + '\n' : d.text.slice(a, d.lineStart(first + lines))
  }

  private makeBlock(first: number, lines: number, last: boolean): Block {
    const el = document.createElement('div')
    el.className = 'ed-block'
    const text = document.createTextNode(this.blockText(first, lines, last))
    el.append(text)
    const b: Block = { el, text, lines, index: 0 }
    this.blockOf.set(el, b)
    this.blockOf.set(text, b)
    return b
  }

  private makeBlocks(first: number, total: number, last: boolean): Block[] {
    const out: Block[] = []
    for (let line = first; line < first + total; line += BLOCK) {
      const n = Math.min(BLOCK, first + total - line)
      out.push(this.makeBlock(line, n, last && line + n >= first + total))
    }
    return out
  }

  private buildAll() {
    this.clearOwn()
    this.blocks = this.makeBlocks(0, this.doc.lineCount, true)
    const frag = document.createDocumentFragment()
    for (const b of this.blocks) frag.append(b.el)
    this.content.replaceChildren(frag)
    this.blockStarts = null
  }

  /** First line of each block (cached until the next structural change). */
  private starts(): number[] {
    if (!this.blockStarts) {
      const s = new Array<number>(this.blocks.length)
      let line = 0
      for (let i = 0; i < this.blocks.length; i++) {
        s[i] = line
        this.blocks[i].index = i
        line += this.blocks[i].lines
      }
      this.blockStarts = s
    }
    return this.blockStarts
  }

  private blockIndex(line: number) {
    const s = this.starts()
    let lo = 0
    let hi = s.length - 1
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1
      if (s[mid] <= line) lo = mid
      else hi = mid - 1
    }
    return lo
  }

  private blockStartOffset(i: number) {
    return this.doc.lineStart(this.starts()[i])
  }

  /** DOM position (text node, offset) of a document offset. */
  private domAt(offset: number): [Text, number] {
    const o = Math.max(0, Math.min(offset, this.doc.text.length))
    const i = this.blockIndex(this.doc.lineAt(o))
    return [this.blocks[i].text, o - this.blockStartOffset(i)]
  }

  /** Mirrors a change of the Doc into the blocks. */
  private applyToBlocks(c: Change, domDone: boolean) {
    const a = this.blockIndex(c.fromLine)
    const b = this.blockIndex(c.fromLine + c.oldLines - 1)
    const delta = c.newLines - c.oldLines
    if (a === b) {
      const blk = this.blocks[a]
      // Lines before the change did not move: the block start offset is still valid.
      if (!domDone) blk.text.replaceData(c.from - this.blockStartOffset(a), c.to - c.from, c.text)
      blk.lines += delta
      this.blockStarts = null
      if (blk.lines > MAX_BLOCK) this.rebuild(a, a, 0)
      return
    }
    this.rebuild(a, b, delta)
  }

  /** Recreates blocks a..b from the Doc (edit across blocks, or a block grown too big). */
  private rebuild(a: number, b: number, delta: number) {
    const s = this.starts()
    let total = delta
    for (let i = a; i <= b; i++) total += this.blocks[i].lines
    const fresh = this.makeBlocks(s[a], total, b === this.blocks.length - 1)
    this.blocks[a].el.before(...fresh.map((x) => x.el))
    for (let i = a; i <= b; i++) this.blocks[i].el.remove()
    this.blocks.splice(a, b - a + 1, ...fresh)
    this.blockStarts = null
  }

  // ---------- setup ----------

  mount(parent: HTMLElement) {
    parent.appendChild(this.root)
    this.measure()
    this.render()
  }

  /** Measures line height and character width (after a font change). */
  measure() {
    const cs = getComputedStyle(this.content)
    const lh = parseFloat(cs.lineHeight)
    if (lh > 0) this.lineHeight = lh
    this.padTop = parseFloat(cs.paddingTop) || 0
    const probe = document.createElement('span')
    probe.textContent = 'x'.repeat(100)
    probe.style.cssText = `position:absolute;visibility:hidden;white-space:pre;font:${cs.font}`
    this.root.appendChild(probe)
    this.charWidth = probe.getBoundingClientRect().width / 100 || 8
    probe.remove()
    this.schedule()
  }

  setOptions(o: Partial<ViewOptions>) {
    Object.assign(this.opts, o)
    if (o.tabSize) this.root.style.setProperty('--tab-size', String(o.tabSize))
    if (o.readOnly !== undefined) this.setReadOnly(o.readOnly || this.doc.readOnly)
    this.schedule()
  }

  setReadOnly(ro: boolean) {
    this.content.contentEditable = ro ? 'false' : 'plaintext-only'
    if (ro) this.content.tabIndex = 0
    this.root.classList.toggle('readonly', ro)
  }

  get readOnly() {
    return this.content.contentEditable === 'false'
  }

  /** The grammar changed (language switch or edited rules). */
  refreshGrammar() {
    this.hl.setGrammar(grammar(this.doc.lang))
    this.schedule()
  }

  destroy() {
    cancelAnimationFrame(this.frame)
    this.clearOwn()
    for (const d of this.disposers) d()
    this.root.remove()
  }

  focus() {
    this.content.focus({ preventScroll: true })
  }

  hasFocus() {
    return document.activeElement === this.content
  }

  // ---------- selection ----------

  private domOffset(node: Node | null, off: number): number {
    const len = this.doc.text.length
    const b = node ? this.blockOf.get(node) : undefined
    if (b && this.blocks[b.index] === b) {
      this.starts()
      const start = this.blockStartOffset(b.index)
      if (node === b.text) return Math.min(start + off, len)
      if (off === 0) return start
      return b.index + 1 < this.blocks.length ? this.blockStartOffset(b.index + 1) : len
    }
    if (node === this.content) return off >= this.blocks.length ? len : this.blockStartOffset(off)
    if (node && this.content.contains(node)) {
      // Unexpected structure (during a composition): count the text before the node.
      const r = document.createRange()
      r.setStart(this.content, 0)
      r.setEnd(node, off)
      return Math.min(r.toString().length, len)
    }
    return 0
  }

  getSelection(): Selection {
    const sel = document.getSelection()
    if (sel && sel.rangeCount > 0 && sel.anchorNode && this.content.contains(sel.anchorNode)) {
      this.lastSel = { anchor: this.domOffset(sel.anchorNode, sel.anchorOffset), head: this.domOffset(sel.focusNode, sel.focusOffset) }
    }
    return this.lastSel
  }

  /** Sets the selection (a single one: the other carets go away). */
  setSelection(anchor: number, head = anchor, scroll = true) {
    this.clearCarets()
    this.select(anchor, head, scroll)
  }

  /** Moves the primary selection, keeping the other carets. */
  private select(anchor: number, head = anchor, scroll = true) {
    const len = this.doc.text.length
    anchor = Math.max(0, Math.min(anchor, len))
    head = Math.max(0, Math.min(head, len))
    this.lastSel = { anchor, head }
    this.reveal(anchor, head)
    if (document.activeElement !== this.content) this.focus()
    const [an, ao] = this.domAt(anchor)
    const [hn, ho] = this.domAt(head)
    document.getSelection()?.setBaseAndExtent(an, ao, hn, ho)
    if (scroll) this.scrollToOffset(head)
    this.updateCurLine()
    this.opts.onSelection?.(this.lastSel)
  }

  /** Restores a selection without taking the focus (tab switch). */
  restoreSelection(sel: Selection) {
    this.clearCarets()
    this.lastSel = sel
    this.updateCurLine()
  }

  private onSelectionChange() {
    const sel = document.getSelection()
    if (!sel?.anchorNode || !this.content.contains(sel.anchorNode)) return
    const prev = this.lastSel
    const cur = this.getSelection()
    if (prev.head !== cur.head) this.doc.breakUndoGroup()
    // Moved by the browser (mouse, select all...), not by the editor: back to a single caret.
    if (prev.head !== cur.head || prev.anchor !== cur.anchor) {
      this.wholeWord = false
      this.clearCarets()
      if (cur.anchor === cur.head) this.reveal(cur.head)
    }
    this.updateCurLine()
    this.opts.onSelection?.(cur)
  }

  selectedText() {
    const { anchor, head } = this.getSelection()
    return this.doc.text.slice(Math.min(anchor, head), Math.max(anchor, head))
  }

  // ---------- editing ----------

  /** Replaces [from, to) and puts the caret after the inserted text. */
  edit(from: number, to: number, text: string, caret?: number | Selection) {
    if (this.readOnly) return
    const before = this.getSelection()
    const after: Selection = typeof caret === 'object' ? caret : { anchor: caret ?? from + text.length, head: caret ?? from + text.length }
    this.doc.replace(from, to, text, this, { selBefore: before, selAfter: after })
    this.setSelection(after.anchor, after.head)
  }

  private onDocChange(c: Change) {
    const o = c.origin as any
    const focused = this.hasFocus()
    this.applyToBlocks(c, !!(o && o.view === this && o.domDone))
    // Keep this view's selection and highlighted spans in place when another view or the pod edits.
    const delta = c.text.length - (c.to - c.from)
    const map = (p: number) => (p <= c.from ? p : p >= c.to ? p + delta : c.from + c.text.length)
    if (o !== this) {
      this.lastSel = { anchor: map(this.lastSel.anchor), head: map(this.lastSel.head) }
      this.extra = this.extra.map((x) => ({ anchor: map(x.anchor), head: map(x.head) }))
      if (focused && !(o && o.view === this)) {
        const [an, ao] = this.domAt(this.lastSel.anchor)
        const [hn, ho] = this.domAt(this.lastSel.head)
        document.getSelection()?.setBaseAndExtent(an, ao, hn, ho)
      }
    }
    for (const s of this.spans.values()) {
      for (const sp of s.list) {
        sp[0] = map(sp[0])
        sp[1] = map(sp[1])
      }
    }
    for (const d of this.diagnostics) {
      d.from = map(d.from)
      d.to = map(d.to)
    }
    if (this.statement) this.statement = null
    // Change markers follow the lines until they are recomputed.
    const shift = c.newLines - c.oldLines
    if (this.folds.length) this.foldsAfterChange(c)
    if (this.marks.size && shift) {
      const next = new Map<number, LineMark>()
      for (const [l, m] of this.marks) next.set(l <= c.fromLine ? l : Math.max(c.fromLine, l + shift), m)
      this.marks = next
    }
    this.hl.edit(c.fromLine, c.oldLines, c.newLines)
    this.schedule()
  }

  private intact() {
    const kids = this.content.childNodes
    if (kids.length !== this.blocks.length) return false
    for (let i = 0; i < kids.length; i++) {
      const b = this.blocks[i]
      if (kids[i] !== b.el || b.el.childNodes.length !== 1 || b.el.firstChild !== b.text) return false
    }
    return true
  }

  /** The DOM changed without going through beforeinput (IME composition, autocorrect). */
  private syncFromDom() {
    if (this.composing) return
    const sel = this.getSelection()
    if (!this.intact()) {
      // The browser restructured the blocks: take the whole text back, then rebuild.
      const dom = (this.content.textContent ?? '').replace(/\n$/, '')
      this.applyDomText(0, this.doc.text.length, dom, true)
      this.buildAll()
      this.select(sel.anchor, sel.head, false)
      this.schedule()
      return
    }
    // Usually only the block holding the caret changed; else compare them all.
    let typed = ''
    const focusBlock = this.blockOf.get(document.getSelection()?.focusNode as Node)
    const order = focusBlock ? [focusBlock.index, ...this.blocks.keys()] : [...this.blocks.keys()]
    for (const i of order) {
      const s = this.starts()
      const b = this.blocks[i]
      const last = i === this.blocks.length - 1
      const expected = this.blockText(s[i], b.lines, last)
      if (b.text.data === expected) continue
      const start = this.blockStartOffset(i)
      const dom = last ? b.text.data.replace(/\n$/, '') : b.text.data
      const end = last ? this.doc.text.length : this.blockStartOffset(i + 1)
      if (last && !b.text.data.endsWith('\n')) b.text.appendData('\n')
      typed = this.applyDomText(start, end, dom, false)
      break
    }
    this.select(sel.anchor, sel.head, false)
    // A dead key or an IME typed at the primary caret: same text at the other ones.
    if (typed && this.extra.length) this.editAll((x, primary) => (primary ? null : { from: selFrom(x), to: selTo(x), text: typed }))
  }

  /** Applies a DOM text that replaces [start, end) of the Doc, as the smallest edit; returns the text typed, if any. */
  private applyDomText(start: number, end: number, dom: string, rebuildAfter: boolean): string {
    const old = this.doc.text.slice(start, end)
    if (this.readOnly) {
      if (!rebuildAfter) this.buildAll()
      return ''
    }
    let a = 0
    const max = Math.min(dom.length, old.length)
    while (a < max && dom.charCodeAt(a) === old.charCodeAt(a)) a++
    let z = 0
    while (z < max - a && dom.charCodeAt(dom.length - 1 - z) === old.charCodeAt(old.length - 1 - z)) z++
    const inserted = dom.slice(a, dom.length - z).replace(/\r\n?/g, '\n')
    this.doc.replace(start + a, end - z, inserted, { view: this, domDone: !rebuildAfter }, { selAfter: this.lastSel })
    // A dead key or an IME produced text: same as typing it.
    if (inserted && !inserted.includes('\n')) queueMicrotask(() => this.opts.onType?.(inserted))
    return inserted && !inserted.includes('\n') && end - z === start + a ? inserted : ''
  }

  private rangeOffsets(r: StaticRange) {
    return { from: this.domOffset(r.startContainer, r.startOffset), to: this.domOffset(r.endContainer, r.endOffset) }
  }

  private indentUnit() {
    return this.opts.insertSpaces ? ' '.repeat(this.opts.tabSize) : '\t'
  }

  private onBeforeInput(e: InputEvent) {
    if (this.composing || e.inputType === 'insertCompositionText') return
    e.preventDefault()
    if (this.readOnly) return
    if (this.extra.length) return this.multiInput(e)
    const sel = this.getSelection()
    let from = Math.min(sel.anchor, sel.head)
    let to = Math.max(sel.anchor, sel.head)
    const target = e.getTargetRanges?.()[0]
    const t = e.inputType
    switch (t) {
      case 'insertText':
      case 'insertReplacementText': {
        const text = e.data ?? e.dataTransfer?.getData('text/plain') ?? ''
        if (t === 'insertReplacementText' && target) ({ from, to } = this.rangeOffsets(target))
        this.edit(from, to, text.replace(/\r\n?/g, '\n'))
        if (t === 'insertText') this.opts.onType?.(text)
        return
      }
      case 'insertLineBreak':
      case 'insertParagraph':
        this.newline(from, to)
        return
      case 'insertFromPaste':
      case 'insertFromDrop':
      case 'insertFromYank':
      case 'insertFromPasteAsQuotation': {
        if (this.pasteGuarded()) return
        const text = e.dataTransfer?.getData('text/plain') ?? e.data ?? ''
        this.edit(from, to, text.replace(/\r\n?/g, '\n'))
        return
      }
      case 'insertTab':
        this.edit(from, to, this.indentUnit())
        return
      case 'historyUndo':
        this.undo()
        return
      case 'historyRedo':
        this.redo()
        return
      case 'deleteByCut':
      case 'deleteByDrag':
      case 'deleteContent':
        if (from !== to) this.edit(from, to, '')
        return
    }
    if (t.startsWith('delete')) {
      if (from === to) {
        if (t === 'deleteContentBackward') {
          // Inside the indentation, go back to the previous tab stop.
          const line = this.doc.lineAt(from)
          const before = this.doc.text.slice(this.doc.lineStart(line), from)
          if (before.length > 0 && /^ +$/.test(before) && this.opts.insertSpaces) {
            const n = before.length % this.opts.tabSize || this.opts.tabSize
            this.edit(from - n, from, '')
            return
          }
        }
        if (target) ({ from, to } = this.rangeOffsets(target))
        else if (t.includes('Backward')) from = Math.max(0, from - 1)
        else to = Math.min(this.doc.text.length, to + 1)
        // Never remove half of a surrogate pair.
        if (from > 0 && from < to && /[\udc00-\udfff]/.test(this.doc.text[from]) && /[\ud800-\udbff]/.test(this.doc.text[from - 1])) from--
      }
      if (from !== to) this.edit(from, to, '')
      this.opts.onType?.('')
    }
  }

  private newline(from: number, to: number) {
    const e = this.newlineEdit(from, to)
    this.edit(e.from, e.to, e.text, e.from + e.caret)
  }

  /** Line break with the indentation of the line, one more level after an opening bracket. */
  private newlineEdit(from: number, to: number) {
    const line = this.doc.lineAt(from)
    const lineText = this.doc.text.slice(this.doc.lineStart(line), from)
    let indent = /^[ \t]*/.exec(lineText)![0]
    const prev = lineText.trimEnd()
    const next = this.doc.text.slice(to, this.doc.lineEnd(this.doc.lineAt(to))).trimStart()
    const opens = /[{([]$/.test(prev) || (/:$/.test(prev) && ['python', 'yaml'].includes(this.doc.lang))
    if (opens) {
      const inner = indent + this.indentUnit()
      if (/^[})\]]/.test(next)) {
        // Between brackets: open an indented line and push the closing one below.
        return { from, to, text: '\n' + inner + '\n' + indent, caret: 1 + inner.length }
      }
      indent = inner
    }
    return { from, to, text: '\n' + indent, caret: 1 + indent.length }
  }

  undo() {
    const sel = this.doc.undo(this)
    if (sel) this.setSelection(sel.anchor, sel.head)
  }

  redo() {
    const sel = this.doc.redo(this)
    if (sel) this.setSelection(sel.anchor, sel.head)
  }

  private onCopy(e: ClipboardEvent, cut: boolean) {
    if (this.extra.length) return this.copyAll(e, cut)
    const sel = this.getSelection()
    let from = Math.min(sel.anchor, sel.head)
    let to = Math.max(sel.anchor, sel.head)
    let text: string
    if (from === to) {
      // Without a selection, copy / cut the whole line.
      const line = this.doc.lineAt(from)
      from = this.doc.lineStart(line)
      to = Math.min(this.doc.text.length, this.doc.lineEnd(line) + 1)
      text = this.doc.text.slice(from, to)
      if (!text.endsWith('\n')) text += '\n'
    } else text = this.doc.text.slice(from, to)
    e.preventDefault()
    e.clipboardData?.setData('text/plain', text)
    if (cut && !this.readOnly) this.edit(from, to, '')
  }

  private onPaste(e: ClipboardEvent) {
    e.preventDefault()
    if (this.readOnly || this.pasteGuarded()) return
    const text = (e.clipboardData?.getData('text/plain') ?? '').replace(/\r\n?/g, '\n')
    if (this.extra.length) return this.pasteAll(text)
    const sel = this.getSelection()
    this.edit(Math.min(sel.anchor, sel.head), Math.max(sel.anchor, sel.head), text)
  }

  private onKeyDown(e: KeyboardEvent) {
    if (e.defaultPrevented || this.composing) return
    if (this.opts.onKey?.(e)) {
      e.preventDefault()
      e.stopPropagation()
      return
    }
    if (this.extra.length && this.multiKey(e)) {
      e.preventDefault()
      e.stopPropagation()
      return
    }
    if (e.key === 'Tab' && !e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault()
      if (this.readOnly) return
      const sel = this.getSelection()
      const from = Math.min(sel.anchor, sel.head)
      const to = Math.max(sel.anchor, sel.head)
      const l1 = this.doc.lineAt(from)
      const l2 = this.doc.lineAt(to)
      if (e.shiftKey || l1 !== l2) this.indentLines(e.shiftKey ? -1 : 1)
      else this.edit(from, to, this.indentUnit())
      return
    }
    if (e.key === 'Home' && !e.ctrlKey && !e.altKey) {
      // Smart Home: first non blank character, then column 0.
      e.preventDefault()
      const sel = this.getSelection()
      const line = this.doc.lineAt(sel.head)
      const start = this.doc.lineStart(line)
      const first = start + /^[ \t]*/.exec(this.doc.lineText(line))![0].length
      const target = sel.head === first ? start : first
      this.setSelection(e.shiftKey ? sel.anchor : target, target)
    }
  }

  /** Indents (+1) or outdents (-1) the lines of the selection. */
  indentLines(dir: 1 | -1) {
    const sel = this.getSelection()
    const from = Math.min(sel.anchor, sel.head)
    const to = Math.max(sel.anchor, sel.head)
    const l1 = this.doc.lineAt(from)
    let l2 = this.doc.lineAt(to)
    if (l2 > l1 && to === this.doc.lineStart(l2)) l2--
    const start = this.doc.lineStart(l1)
    const end = this.doc.lineEnd(l2)
    const unit = this.indentUnit()
    const lines = this.doc.text.slice(start, end).split('\n')
    const out = lines.map((l) => {
      if (dir > 0) return l.length ? unit + l : l
      const m = /^(\t| {1,4})/.exec(l)
      return m ? l.slice(Math.min(m[0].length, this.opts.tabSize)) : l
    })
    const text = out.join('\n')
    this.edit(start, end, text, { anchor: start, head: start + text.length })
  }

  duplicateLine() {
    const sel = this.getSelection()
    const from = Math.min(sel.anchor, sel.head)
    const to = Math.max(sel.anchor, sel.head)
    if (from !== to) {
      const t = this.doc.text.slice(from, to)
      this.edit(to, to, t, { anchor: to, head: to + t.length })
      return
    }
    const line = this.doc.lineAt(from)
    const t = this.doc.lineText(line)
    const end = this.doc.lineEnd(line)
    const col = from - this.doc.lineStart(line)
    this.edit(end, end, '\n' + t, end + 1 + col)
  }

  deleteLine() {
    const sel = this.getSelection()
    const l1 = this.doc.lineAt(Math.min(sel.anchor, sel.head))
    const l2 = this.doc.lineAt(Math.max(sel.anchor, sel.head))
    const start = this.doc.lineStart(l1)
    let end = this.doc.lineEnd(l2)
    let s = start
    if (end < this.doc.text.length) end++
    else if (s > 0) s--
    this.edit(s, end, '', Math.min(s === start ? start : s + 1, this.doc.text.length - (end - s)))
  }

  toggleComment() {
    const prefix = commentPrefix[this.doc.lang]
    if (!prefix) return
    const sel = this.getSelection()
    const from = Math.min(sel.anchor, sel.head)
    const to = Math.max(sel.anchor, sel.head)
    const l1 = this.doc.lineAt(from)
    let l2 = this.doc.lineAt(to)
    if (l2 > l1 && to === this.doc.lineStart(l2)) l2--
    const start = this.doc.lineStart(l1)
    const end = this.doc.lineEnd(l2)
    const lines = this.doc.text.slice(start, end).split('\n')
    const esc = prefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const re = new RegExp(`^(\\s*)${esc} ?`)
    const all = lines.filter((l) => l.trim()).every((l) => re.test(l))
    const indent = Math.min(...lines.filter((l) => l.trim()).map((l) => /^\s*/.exec(l)![0].length))
    const out = lines.map((l) => {
      if (!l.trim()) return l
      if (all) return l.replace(re, '$1')
      return l.slice(0, indent) + prefix + ' ' + l.slice(indent)
    })
    const text = out.join('\n')
    if (from === to) {
      const line = this.doc.lineAt(from)
      this.doc.replace(start, end, text, this, { selBefore: sel })
      const next = Math.min(line + 1, this.doc.lineCount - 1)
      const col = from - start
      this.setSelection(line + 1 < this.doc.lineCount ? this.doc.offset(next, col) : this.doc.lineEnd(line))
    } else this.edit(start, end, text, { anchor: start, head: start + text.length })
  }

  moveSubword(dir: -1 | 1, extend: boolean) {
    const to = (x: Selection) => (dir < 0 ? subwordLeft(this.doc.text, x.head) : subwordRight(this.doc.text, x.head))
    if (this.extra.length) return this.moveAll(to, extend)
    const sel = this.getSelection()
    const head = to(sel)
    this.setSelection(extend ? sel.anchor : head, head)
  }

  // ---------- multiple carets ----------

  /** True while there are several carets. */
  hasCarets() {
    return this.extra.length > 0
  }

  /** Back to the primary selection only. */
  clearCarets() {
    if (!this.extra.length) return
    this.extra = []
    this.schedule()
  }

  /** Every selection, sorted, with the index of the primary one. */
  private allSelections() {
    return normalize([...this.extra, this.getSelection()], this.extra.length)
  }

  /** Sets every selection; the primary one becomes the DOM selection. */
  private setSelections(list: Selection[], primary: number, scroll = true) {
    const n = normalize(list, primary)
    const p = n.list[n.primary]
    this.extra = n.list.filter((_, i) => i !== n.primary)
    this.select(p.anchor, p.head, scroll)
    this.schedule()
  }

  /**
   * One edit per selection (null: none), applied as one undo step; each caret goes after
   * its edit (at `caret` characters from its start when given).
   */
  private editAll(make: (s: Selection, primary: boolean) => { from: number; to: number; text: string; caret?: number } | null) {
    if (this.readOnly) return
    const { list, primary } = this.allSelections()
    const edits = list.map((x, i) => make(x, i === primary))
    let end = 0
    for (const e of edits) {
      if (!e) continue
      e.from = Math.max(e.from, end)
      e.to = Math.max(e.to, e.from)
      end = e.to
    }
    let delta = 0
    const after = list.map((x, i) => {
      const e = edits[i]
      if (!e) return { anchor: x.anchor + delta, head: x.head + delta }
      const pos = e.from + delta + (e.caret ?? e.text.length)
      delta += e.text.length - (e.to - e.from)
      return { anchor: pos, head: pos }
    })
    this.doc.transact(() => {
      for (let i = edits.length - 1; i >= 0; i--) {
        const e = edits[i]
        if (e) this.doc.replace(e.from, e.to, e.text, this, { selBefore: list[primary], selAfter: after[primary] })
      }
    })
    this.setSelections(after, primary)
  }

  /** Moves every caret (or extends every selection) to the offset given by `to`. */
  private moveAll(to: (s: Selection) => number, extend: boolean) {
    const { list, primary } = this.allSelections()
    this.setSelections(
      list.map((x) => {
        const h = to(x)
        return { anchor: extend ? x.anchor : h, head: h }
      }),
      primary,
    )
  }

  private multiInput(e: InputEvent) {
    const t = e.inputType
    const data = (e.data ?? e.dataTransfer?.getData('text/plain') ?? '').replace(/\r\n?/g, '\n')
    const text = this.doc.text
    if (t === 'insertText' || t === 'insertReplacementText') return this.editAll((x) => ({ from: selFrom(x), to: selTo(x), text: data }))
    if (t.startsWith('insertFrom')) return this.pasteGuarded() ? undefined : this.pasteAll(data)
    if (t === 'insertLineBreak' || t === 'insertParagraph') return this.editAll((x) => this.newlineEdit(selFrom(x), selTo(x)))
    if (t === 'insertTab') return this.editAll((x) => ({ from: selFrom(x), to: selTo(x), text: this.indentUnit() }))
    if (t === 'historyUndo') return this.undo()
    if (t === 'historyRedo') return this.redo()
    if (!t.startsWith('delete')) return
    const back = t.includes('Backward')
    this.editAll((x) => {
      let from = selFrom(x)
      let to = selTo(x)
      if (from === to) {
        const line = this.doc.lineAt(from)
        if (t.includes('Line')) back ? (from = this.doc.lineStart(line)) : (to = this.doc.lineEnd(line))
        else if (t.includes('Word')) back ? (from = wordLeft(text, from)) : (to = wordRight(text, to))
        else if (back) from = this.backspaceFrom(from)
        else to = Math.min(text.length, to + 1)
      }
      return from === to ? null : { from, to, text: '' }
    })
  }

  /** Start of what Backspace removes at a caret: a whole indentation step, or one character. */
  private backspaceFrom(pos: number) {
    const before = this.doc.text.slice(this.doc.lineStart(this.doc.lineAt(pos)), pos)
    if (before.length > 0 && /^ +$/.test(before) && this.opts.insertSpaces) return pos - (before.length % this.opts.tabSize || this.opts.tabSize)
    let from = Math.max(0, pos - 1)
    if (from > 0 && /[\udc00-\udfff]/.test(this.doc.text[from]) && /[\ud800-\udbff]/.test(this.doc.text[from - 1])) from--
    return from
  }

  /** Paste at every caret: one line each when the text has as many lines as there are carets. */
  private pasteAll(text: string) {
    const n = this.extra.length + 1
    let lines = text.split('\n')
    if (lines.length === n + 1 && lines[n] === '') lines = lines.slice(0, n)
    let i = 0
    this.editAll((x) => ({ from: selFrom(x), to: selTo(x), text: lines.length === n ? lines[i++] : text }))
  }

  /** Copy (or cut) of every selection, one per line; without a selection, the lines of the carets. */
  private copyAll(e: ClipboardEvent, cut: boolean) {
    e.preventDefault()
    const { list } = this.allSelections()
    const lines = list.every((x) => x.anchor === x.head)
    const range = (x: Selection): [number, number] => {
      if (!lines) return [selFrom(x), selTo(x)]
      const line = this.doc.lineAt(x.head)
      return [this.doc.lineStart(line), Math.min(this.doc.text.length, this.doc.lineEnd(line) + 1)]
    }
    const parts = list.map((x) => this.doc.text.slice(...range(x)).replace(/\n$/, ''))
    e.clipboardData?.setData('text/plain', parts.join('\n') + (lines ? '\n' : ''))
    if (cut && !this.readOnly) this.editAll((x) => ({ from: range(x)[0], to: range(x)[1], text: '' }))
  }

  /** Keys acting on every caret; true when handled. */
  private multiKey(e: KeyboardEvent): boolean {
    if (e.altKey || e.metaKey) return false
    if (e.key === 'Escape' && !e.ctrlKey && !e.shiftKey) {
      this.clearCarets()
      return true
    }
    if (e.key === 'Tab' && !e.ctrlKey) {
      if (!e.shiftKey) this.editAll((x) => ({ from: selFrom(x), to: selTo(x), text: this.indentUnit() }))
      return true
    }
    const text = this.doc.text
    const d = this.doc
    let to: ((x: Selection) => number) | null = null
    switch (e.key) {
      case 'ArrowLeft':
        to = (x) => (e.ctrlKey ? wordLeft(text, x.head) : x.anchor !== x.head && !e.shiftKey ? selFrom(x) : Math.max(0, x.head - 1))
        break
      case 'ArrowRight':
        to = (x) => (e.ctrlKey ? wordRight(text, x.head) : x.anchor !== x.head && !e.shiftKey ? selTo(x) : Math.min(text.length, x.head + 1))
        break
      case 'ArrowUp':
      case 'ArrowDown':
        if (e.ctrlKey) return false
        to = (x) => {
          const line = d.lineAt(x.head)
          const next = this.nextLine(line, e.key === 'ArrowUp' ? -1 : 1)
          if (next < 0) return e.key === 'ArrowUp' ? 0 : text.length
          return this.offsetAtCol(next, this.visualCol(line, x.head))
        }
        break
      case 'Home':
        if (e.ctrlKey) return false
        to = (x) => {
          const line = d.lineAt(x.head)
          const first = d.lineStart(line) + /^[ \t]*/.exec(d.lineText(line))![0].length
          return x.head === first ? d.lineStart(line) : first
        }
        break
      case 'End':
        if (e.ctrlKey) return false
        to = (x) => d.lineEnd(d.lineAt(x.head))
        break
    }
    if (!to) {
      // Keys moving far away (page, document start or end): a single caret again.
      if (['PageUp', 'PageDown', 'Home', 'End'].includes(e.key)) this.clearCarets()
      return false
    }
    this.moveAll(to, e.shiftKey)
    return true
  }

  /** Line shown above (-1) or below (1), -1 past the document. */
  private nextLine(line: number, dir: -1 | 1) {
    let l = line + dir
    const h = this.hiddenAt(l)
    if (h) l = dir > 0 ? h[1] + 1 : h[0] - 1
    return l < 0 || l >= this.doc.lineCount ? -1 : l
  }

  /** Offset of a line at a visual column (tabs expanded), clamped to the line end. */
  private offsetAtCol(line: number, col: number) {
    const start = this.doc.lineStart(line)
    const t = this.doc.lineText(line)
    const tab = this.opts.tabSize
    let c = 0
    for (let i = 0; i < t.length; i++) {
      if (col <= c) return start + i
      const w = t[i] === '\t' ? tab - (c % tab) : 1
      if (col < c + w) return start + (col - c < w / 2 ? i : i + 1)
      c += w
    }
    return start + t.length
  }

  private pasteGuarded() {
    return performance.now() - this.pasteGuard < 400
  }

  private onMouseDown(e: MouseEvent) {
    if (e.button === 1 || (e.button === 0 && e.altKey && e.shiftKey)) {
      e.preventDefault()
      this.boxDrag(e)
    } else if (e.button === 0 && e.altKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault()
      this.toggleCaret(e.clientX, e.clientY)
    } else if (e.button === 0) this.clearCarets()
  }

  /** Alt+click: adds a caret (it becomes the primary one), or removes the one clicked. */
  private toggleCaret(x: number, y: number) {
    const off = this.offsetAt(x, y)
    if (off == null) return
    if (!this.hasFocus()) this.focus()
    const sel = this.getSelection()
    const i = this.extra.findIndex((s) => s.anchor === off && s.head === off)
    if (i >= 0) {
      this.extra.splice(i, 1)
    } else if (sel.anchor === off && sel.head === off) {
      const p = this.extra.pop()
      if (p) this.select(p.anchor, p.head, false)
    } else {
      this.extra.push(sel)
      this.select(off, off, false)
    }
    this.schedule()
  }

  /** Column selection while dragging (middle button, or Alt+Shift with the left one). */
  private boxDrag(e: MouseEvent) {
    if (!this.hasFocus()) this.focus()
    const middle = e.button === 1
    const start = this.boxPoint(e.clientX, e.clientY)
    const move = (ev: MouseEvent) => {
      ev.preventDefault()
      this.boxSelect(start, this.boxPoint(ev.clientX, ev.clientY))
    }
    const up = (ev: MouseEvent) => {
      ev.preventDefault()
      if (middle) this.pasteGuard = performance.now()
      window.removeEventListener('mousemove', move, true)
      window.removeEventListener('mouseup', up, true)
    }
    window.addEventListener('mousemove', move, true)
    window.addEventListener('mouseup', up, true)
    this.boxSelect(start, start)
  }

  /** Line and visual column under a mouse position. */
  private boxPoint(x: number, y: number) {
    const r = this.content.getBoundingClientRect()
    const line = Math.max(0, Math.min(this.doc.lineCount - 1, this.lineAtY(y - r.top)))
    return { line, col: Math.max(0, Math.round((x - r.left) / this.charWidth)) }
  }

  private boxSelect(a: { line: number; col: number }, b: { line: number; col: number }) {
    const list: Selection[] = []
    const dir = a.line <= b.line ? 1 : -1
    for (let l = a.line; l >= 0; l = l === b.line ? -1 : this.nextLine(l, dir)) {
      list.push({ anchor: this.offsetAtCol(l, a.col), head: this.offsetAtCol(l, b.col) })
    }
    this.setSelections(list, list.length - 1, false)
  }

  /** Alt+J: selects the word at the caret, then adds the next occurrence of the selected text. */
  addNextOccurrence() {
    const sel = this.getSelection()
    const text = this.doc.text
    if (sel.anchor === sel.head) {
      const w = wordAt(text, sel.head)
      if (!w) return
      if (this.extra.length) {
        // Several carets: each one selects its word.
        const { list, primary } = this.allSelections()
        this.setSelections(list.map((x) => {
          const v = wordAt(text, x.head)
          return v ? { anchor: v[0], head: v[1] } : x
        }), primary)
      } else this.select(w[0], w[1])
      this.wholeWord = true
      return
    }
    const needle = text.slice(selFrom(sel), selTo(sel))
    const at = nextOccurrence(text, needle, selTo(sel), this.wholeWord, [...this.extra, sel])
    if (at == null) return
    this.extra.push(sel)
    this.select(at, at + needle.length)
    this.schedule()
  }

  /** Shift+Alt+J: removes the last occurrence added. */
  removeLastOccurrence() {
    const p = this.extra.pop()
    if (!p) return
    this.select(p.anchor, p.head)
    this.schedule()
  }

  /** Ctrl+Alt+Shift+J: selects every occurrence of the selection (or of the word at the caret). */
  selectAllOccurrences() {
    let sel = this.getSelection()
    const text = this.doc.text
    if (sel.anchor === sel.head) {
      const w = wordAt(text, sel.head)
      if (!w) return
      sel = { anchor: w[0], head: w[1] }
      this.wholeWord = true
    }
    const needle = text.slice(selFrom(sel), selTo(sel))
    const all = occurrences(text, needle, this.wholeWord)
    const list = all.map((i) => ({ anchor: i, head: i + needle.length }))
    this.setSelections(list.length ? list : [sel], Math.max(0, all.indexOf(selFrom(sel))), false)
  }

  /** Other carets and selections of lines a..b. */
  private renderCarets(a: number, b: number) {
    if (!this.extra.length) {
      if (this.caretLayer.firstChild) this.caretLayer.replaceChildren()
      return
    }
    const from = this.doc.lineStart(a)
    const to = this.doc.lineEnd(b)
    let html = ''
    for (const x of this.extra) {
      if (x.anchor !== x.head && selTo(x) >= from && selFrom(x) <= to && registry()) {
        this.addOwn('ed-sel2', this.range(Math.max(selFrom(x), from), Math.min(selTo(x), to)))
      }
      if (x.head < from || x.head > to) continue
      html += `<div class="ed-caret" style="left:${this.xAt(x.head)}px;top:${this.lineTop(this.doc.lineAt(x.head))}px;height:${this.lineHeight}px"></div>`
    }
    this.caretLayer.innerHTML = html
  }

  /** Horizontal position of an offset in the content. */
  private xAt(offset: number) {
    const line = this.doc.lineAt(offset)
    const end = this.doc.lineEnd(line)
    if (this.doc.lineStart(line) === end) return 0
    const [n, o] = this.domAt(offset < end ? offset : offset - 1)
    const r = document.createRange()
    r.setStart(n, o)
    r.setEnd(n, Math.min(o + 1, n.length))
    const rect = r.getBoundingClientRect()
    return (offset < end ? rect.left : rect.right) - this.content.getBoundingClientRect().left
  }

  // ---------- folding ----------

  /** Header lines of the folded ranges (saved in the session). */
  foldedLines() {
    return this.folds.map((f) => f.line)
  }

  /** Folds again the ranges opened by these lines (session restore). */
  restoreFolds(lines: number[]) {
    const list: Fold[] = []
    for (const line of lines) {
      const end = line < this.doc.lineCount ? this.folder.end(line) : -1
      if (end > line) list.push({ line, end })
    }
    this.setFolds(list, false)
  }

  /** Folds the innermost range holding the caret line. */
  fold() {
    const line = this.doc.lineAt(this.getSelection().head)
    for (let l = line, k = 0; l >= 0 && k < 5000; l = this.nextLine(l, -1), k++) {
      if (this.folds.some((f) => f.line === l)) continue
      const end = this.folder.end(l)
      // With brackets, the line of the closing one belongs to the range too.
      if (end > l && (end >= line || (end + 1 === line && /^\s*[}\])]/.test(this.doc.lineText(line))))) {
        this.setFolds([...this.folds, { line: l, end }])
        return
      }
    }
  }

  /** Unfolds the range of the caret line. */
  unfold() {
    const line = this.doc.lineAt(this.getSelection().head)
    this.setFolds(this.folds.filter((f) => f.line !== line))
  }

  foldAll() {
    this.setFolds(this.folder.all())
  }

  unfoldAll() {
    this.setFolds([])
  }

  /** Gutter marker: folds or unfolds the range a line opens. */
  toggleFold(line: number) {
    if (this.folds.some((f) => f.line === line)) return this.setFolds(this.folds.filter((f) => f.line !== line))
    const end = this.folder.end(line)
    if (end > line) this.setFolds([...this.folds, { line, end }])
  }

  private setFolds(list: Fold[], notify = true) {
    if (this.hasFocus()) this.getSelection()
    const byLine = new Map<number, Fold>()
    for (const f of list) byLine.set(f.line, f)
    this.folds = [...byLine.values()].sort((a, b) => a.line - b.line)
    this.clearCarets()
    this.applyFolds()
    // A caret left in a hidden line goes to the end of the header line.
    const h = this.hiddenAt(this.doc.lineAt(this.lastSel.head))
    if (h) {
      const end = this.doc.lineEnd(h[0] - 1)
      if (this.hasFocus()) this.select(end, end)
      else this.lastSel = { anchor: end, head: end }
    }
    if (notify) this.opts.onFolds?.()
    this.schedule()
  }

  /** Unfolds the ranges hiding these offsets. */
  private reveal(...offsets: number[]) {
    if (!this.hidden.length) return
    const lines = offsets.map((o) => this.doc.lineAt(o))
    if (!lines.some((l) => this.hiddenAt(l))) return
    this.folds = this.folds.filter((f) => !lines.some((l) => l > f.line && l <= f.end))
    this.applyFolds()
    this.opts.onFolds?.()
    this.schedule()
  }

  /** Folds follow the edits; an edit inside a range (or changing the lines of its header) unfolds it. */
  private foldsAfterChange(c: Change) {
    const last = c.fromLine + c.oldLines - 1
    const shift = c.newLines - c.oldLines
    const kept = this.folds.filter((f) => !(c.fromLine <= f.end && last > f.line) && !(shift && c.fromLine <= f.line && last >= f.line))
    const dropped = kept.length !== this.folds.length
    for (const f of kept) {
      if (f.line > last) {
        f.line += shift
        f.end += shift
      }
    }
    this.folds = kept
    this.applyFolds()
    if (dropped) this.opts.onFolds?.()
  }

  /** Hides the folded lines: blocks are split at the range limits and the inner ones get display: none. */
  private applyFolds() {
    const ranges = this.folds.map((f): [number, number] => [f.line + 1, Math.min(f.end, this.doc.lineCount - 1)]).sort((a, b) => a[0] - b[0])
    const merged: [number, number][] = []
    for (const r of ranges) {
      const m = merged[merged.length - 1]
      if (m && r[0] <= m[1] + 1) m[1] = Math.max(m[1], r[1])
      else if (r[1] >= r[0]) merged.push([r[0], r[1]])
    }
    this.hidden = merged
    let split = false
    for (const [s, e] of merged) {
      split = this.splitAt(s) || split
      split = this.splitAt(e + 1) || split
    }
    const starts = this.starts()
    for (let i = 0, k = 0; i < this.blocks.length; i++) {
      while (k < merged.length && merged[k][1] < starts[i]) k++
      this.blocks[i].el.classList.toggle('ed-folded', k < merged.length && merged[k][0] <= starts[i])
    }
    // Moving a text node to a new block loses the DOM selection.
    if (split && this.hasFocus()) {
      const [an, ao] = this.domAt(this.lastSel.anchor)
      const [hn, ho] = this.domAt(this.lastSel.head)
      document.getSelection()?.setBaseAndExtent(an, ao, hn, ho)
    }
  }

  /** Makes a block start at a line; true when a block was split. */
  private splitAt(line: number) {
    if (line <= 0 || line >= this.doc.lineCount) return false
    const i = this.blockIndex(line)
    const first = this.starts()[i]
    if (first === line) return false
    const blk = this.blocks[i]
    const rest = blk.text.splitText(this.doc.lineStart(line) - this.blockStartOffset(i))
    const el = document.createElement('div')
    el.className = 'ed-block'
    el.append(rest)
    blk.el.after(el)
    const nb: Block = { el, text: rest, lines: blk.lines - (line - first), index: 0 }
    blk.lines = line - first
    this.blockOf.set(el, nb)
    this.blockOf.set(rest, nb)
    this.blocks.splice(i + 1, 0, nb)
    this.blockStarts = null
    return true
  }

  /** The line opens a range (cached until the next edit). */
  private canFold(line: number) {
    if (this.foldable.version !== this.doc.version) this.foldable = { version: this.doc.version, lines: new Map() }
    let v = this.foldable.lines.get(line)
    if (v === undefined) this.foldable.lines.set(line, (v = this.folder.canFold(line)))
    return v
  }

  // ---------- rendering ----------

  private schedule() {
    if (this.frame) return
    this.frame = requestAnimationFrame(() => {
      this.frame = 0
      this.render()
    })
  }

  private clearOwn() {
    for (const [name, ranges] of this.own) {
      const h = highlight(name)
      for (const r of ranges) h?.delete(r)
    }
    this.own.clear()
  }

  private addOwn(name: string, r: AbstractRange, priority = 0) {
    let l = this.own.get(name)
    if (!l) this.own.set(name, (l = []))
    l.push(r)
    highlight(name, priority)?.add(r)
  }

  private range(from: number, to: number): StaticRange {
    const [sn, so] = this.domAt(from)
    const [en, eo] = this.domAt(to)
    return new StaticRange({ startContainer: sn, startOffset: so, endContainer: en, endOffset: eo })
  }

  /** Rows (screen lines) shown, first and last. */
  private visibleRows(): [number, number] {
    const top = this.scroller.scrollTop
    const h = this.scroller.clientHeight || 800
    return [Math.max(0, Math.floor((top - this.padTop) / this.lineHeight)), Math.ceil((top + h) / this.lineHeight)]
  }

  visibleLines(): [number, number] {
    const [first, last] = this.visibleRows()
    return [this.lineOfRow(first), this.lineOfRow(last)]
  }

  render() {
    if (!this.root.isConnected) return
    const n = this.doc.lineCount
    const [first, last] = this.visibleRows()
    const a = this.lineOfRow(first - MARGIN)
    const b = this.lineOfRow(last + MARGIN)
    // Lines a..b not hidden by a fold.
    const vis: number[] = []
    for (let l = a, k = 0; l <= b; l++) {
      while (k < this.hidden.length && this.hidden[k][1] < l) k++
      if (k < this.hidden.length && this.hidden[k][0] <= l) l = this.hidden[k][1]
      else vis.push(l)
    }

    // Gutter: only the numbers of the rendered lines, moved to their place.
    let nums = ''
    for (const l of vis) nums += l + 1 + '\n'
    this.gutterNums.textContent = nums
    this.gutterNums.style.transform = `translateY(${this.lineTop(a)}px)`
    this.gutter.style.width = `calc(${String(n).length}ch + 36px)`
    let marks = ''
    let folds = ''
    let holders = ''
    const lh = this.lineHeight
    for (const l of vis) {
      const m = this.marks.get(l)
      const top = this.lineTop(l)
      if (m) marks += `<div class="ed-mark mark-${m}" style="top:${top}px;height:${lh}px"></div>`
      if (this.folds.some((f) => f.line === l)) {
        folds += `<div class="ed-fold folded" data-fold="${l}" style="top:${top}px;height:${lh}px"></div>`
        holders += `<div class="ed-placeholder" data-fold="${l}" style="left:${this.xAt(this.doc.lineEnd(l)) + 6}px;top:${top + 2}px;height:${lh - 4}px;line-height:${lh - 4}px">\u22ef</div>`
      } else if (this.canFold(l)) folds += `<div class="ed-fold" data-fold="${l}" style="top:${top}px;height:${lh}px"></div>`
    }
    this.gutterMarks.innerHTML = marks
    this.gutterFolds.innerHTML = folds
    this.placeholders.innerHTML = holders

    this.clearOwn()
    if (registry()) {
      this.starts()
      let bi = this.blockIndex(a)
      for (const line of vis) {
        while (bi + 1 < this.blocks.length && this.blockStarts![bi + 1] <= line) bi++
        const blk = this.blocks[bi]
        const base = this.doc.lineStart(line) - this.blockStartOffset(bi)
        const len = blk.text.length
        for (const [s, e, type] of this.hl.get(line) as Token[]) {
          if (base + e > len) break
          this.addOwn('tok-' + type, new StaticRange({ startContainer: blk.text, startOffset: base + s, endContainer: blk.text, endOffset: base + e }))
        }
      }
      // Search results and diagnostics: only those crossing the rendered lines.
      const from = this.doc.lineStart(a)
      const to = this.doc.lineEnd(b)
      for (const [name, sp] of this.spans) {
        const list = sp.list
        let lo = 0
        let hi = list.length
        while (lo < hi) {
          const mid = (lo + hi) >> 1
          if (list[mid][1] < from) lo = mid + 1
          else hi = mid
        }
        for (let i = lo; i < list.length && list[i][0] <= to; i++) {
          this.addOwn(name, this.range(list[i][0], Math.min(list[i][1], this.doc.text.length)), sp.priority)
        }
      }
    }
    this.renderGuides(vis)
    this.renderWhitespace(vis)
    this.renderCarets(a, b)
    this.renderStatement()
    this.updateCurLine()
  }

  /** Top of a line in the content. */
  private lineTop(line: number) {
    return this.padTop + this.rowOf(line) * this.lineHeight
  }

  /** Line at a height in the content. */
  private lineAtY(y: number) {
    return this.lineOfRow(Math.floor((y - this.padTop) / this.lineHeight))
  }

  /** Screen row of a line: its number less the hidden lines above it. */
  private rowOf(line: number) {
    let row = line
    for (const [s, e] of this.hidden) {
      if (s >= line) break
      row -= Math.min(e, line - 1) - s + 1
    }
    return row
  }

  /** Line shown at a screen row. */
  private lineOfRow(row: number) {
    let line = Math.max(0, row)
    for (const [s, e] of this.hidden) {
      if (s > line) break
      line += e - s + 1
    }
    return Math.min(line, this.doc.lineCount - 1)
  }

  /** Hidden range holding a line, if any. */
  private hiddenAt(line: number) {
    for (const r of this.hidden) {
      if (r[0] > line) return null
      if (r[1] >= line) return r
    }
    return null
  }

  /** Indentation width of a line in columns, -1 for a blank line. */
  private indentOf(line: number) {
    const t = this.doc.lineText(line)
    const tab = this.opts.tabSize
    let col = 0
    for (let i = 0; i < t.length; i++) {
      const c = t.charCodeAt(i)
      if (c === 32) col++
      else if (c === 9) col += tab - (col % tab)
      else return col
    }
    return -1
  }

  /** Indentation of a line; a blank line takes the smaller one of the lines around it. */
  private blockIndent(line: number) {
    const own = this.indentOf(line)
    if (own >= 0) return own
    const near = (dir: number) => {
      for (let l = line + dir, k = 0; l >= 0 && l < this.doc.lineCount && k < 200; l += dir, k++) {
        const i = this.indentOf(l)
        if (i >= 0) return i
      }
      return 0
    }
    return Math.min(near(-1), near(1))
  }

  /** Indentation step of the file: the most frequent indentation increase, else the tab size. */
  private indentStep() {
    const tab = this.opts.tabSize
    if (this.step?.version === this.doc.version && this.step.tabSize === tab) return this.step.cols
    const counts = new Map<number, number>()
    let prev = 0
    for (let l = 0, n = Math.min(this.doc.lineCount, 1000); l < n; l++) {
      const i = this.indentOf(l)
      if (i < 0) continue
      if (i > prev) counts.set(i - prev, (counts.get(i - prev) ?? 0) + 1)
      prev = i
    }
    let cols = tab
    let best = 0
    for (const [d, c] of counts) if (c > best || (c === best && d < cols)) [cols, best] = [d, c]
    this.step = { version: this.doc.version, tabSize: tab, cols: Math.max(1, Math.min(cols, 8)) }
    return this.step.cols
  }

  /**
   * Whitespace of the rendered lines, drawn over the text: a copy of the lines in transparent
   * characters (same widths, tabs included) where spaces, tabs and line ends get a mark.
   */
  private renderWhitespace(vis: number[]) {
    if (!this.opts.showWhitespace || !vis.length) {
      if (this.ws.firstChild) this.ws.replaceChildren()
      return
    }
    const esc: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', ' ': '<i> </i>', '\t': '<i class="t">\t</i>' }
    let html = ''
    for (const l of vis) {
      html += this.doc.lineText(l).replace(/[&<> \t]/g, (c) => esc[c])
      html += l < this.doc.lineCount - 1 ? '<i class="n">\u21b5</i>\n' : '\n'
    }
    this.ws.style.top = `${this.lineTop(vis[0])}px`
    this.ws.innerHTML = html
  }

  /**
   * Indentation guides of the rendered lines (indexes in `vis`); the one of the block
   * holding the caret stands out.
   */
  private renderGuides(vis: number[]) {
    if (!this.opts.indentGuides) {
      this.guides.replaceChildren()
      return
    }
    const step = this.indentStep()
    const ind = vis.map((l) => this.blockIndent(l))
    const b = vis.length - 1
    // Active guide: the block the caret line opens, else the block holding it.
    let active: [number, number, number] | null = null
    const caretLine = this.doc.lineAt(this.lastSel.head)
    this.guideLine = caretLine
    const caret = vis.indexOf(caretLine)
    if (caret >= 0) {
      const own = ind[caret]
      let next = -1
      for (let l = this.nextLine(caretLine, 1), k = 0; l >= 0 && next < 0 && k < 200; l = this.nextLine(l, 1), k++) next = this.indentOf(l)
      const col = next > own ? own : own - step
      if (col >= 0) {
        let from = next > own ? caret + 1 : caret
        let to = from
        while (from - 1 >= 0 && ind[from - 1] > col) from--
        while (to + 1 <= b && ind[to + 1] > col) to++
        active = [Math.floor(col / step) * step, from, to]
      }
    }
    let html = ''
    const seg = (col: number, from: number, to: number) => {
      const on = active && active[0] === col && active[1] <= from && active[2] >= to
      html += `<div class="ed-guide${on ? ' active' : ''}" style="left:${(col + 0.5) * this.charWidth}px;top:${this.lineTop(vis[from])}px;height:${(to - from + 1) * this.lineHeight}px"></div>`
    }
    const max = Math.max(0, ...ind)
    for (let col = 0; col < max; col += step) {
      let start = -1
      for (let l = 0; l <= b + 1; l++) {
        const inside = l <= b && ind[l] > col
        // The active block gets its own segment.
        const cut = active && active[0] === col && (l === active[1] || l === active[2] + 1)
        if (start >= 0 && (!inside || cut)) {
          seg(col, start, l - 1)
          start = -1
        }
        if (inside && start < 0) start = l
      }
    }
    this.guides.innerHTML = html
  }

  private updateCurLine() {
    // The active indentation guide follows the caret line.
    if (this.opts.indentGuides && this.doc.lineAt(this.lastSel.head) !== this.guideLine) this.schedule()
    const show = this.opts.highlightLine && this.lastSel.anchor === this.lastSel.head
    this.curLine.style.display = show ? 'block' : 'none'
    if (show) {
      const line = this.doc.lineAt(this.lastSel.head)
      this.curLine.style.transform = `translateY(${this.lineTop(line)}px)`
      this.curLine.style.height = `${this.lineHeight}px`
    }
  }

  /** Change markers of the gutter (lines added, modified, deleted against the VCS). */
  setLineMarks(marks: Map<number, LineMark>) {
    this.marks = marks
    this.schedule()
  }

  /** Highlighted spans (search results...), sorted by start; they follow the edits. */
  setLiveRanges(name: string, spans: [number, number][], priority = 1) {
    if (!spans.length) this.spans.delete(name)
    else this.spans.set(name, { list: spans.map(([a, b]) => [a, b] as [number, number]), priority })
    this.schedule()
  }

  setDiagnostics(list: Diagnostic[]) {
    this.diagnostics = [...list].sort((a, b) => a.from - b.from)
    for (const sev of ['error', 'warning', 'info'] as const) {
      this.setLiveRanges(
        'diag-' + sev,
        this.diagnostics.filter((d) => d.severity === sev).map((d) => [d.from, Math.max(d.to, d.from + 1)]),
        1,
      )
    }
  }

  /** Frames the active statement (SQL console). */
  setStatement(range: [number, number] | null) {
    this.statement = range
    this.renderStatement()
  }

  private renderStatement() {
    this.boxes.replaceChildren()
    if (!this.statement) return
    const [from, to] = this.statement
    if (to <= from || to > this.doc.text.length) return
    const l1 = this.doc.lineAt(from)
    const l2 = this.doc.lineAt(to)
    let maxCol = 0
    for (let l = l1; l <= l2; l++) maxCol = Math.max(maxCol, this.visualCol(l, this.doc.lineEnd(l)))
    const box = document.createElement('div')
    box.className = 'ed-statement'
    box.style.top = `${this.lineTop(l1) - 1}px`
    box.style.height = `${(l2 - l1 + 1) * this.lineHeight + 2}px`
    box.style.width = `${maxCol * this.charWidth + 6}px`
    this.boxes.append(box)
  }

  private visualCol(line: number, offset: number) {
    const text = this.doc.text.slice(this.doc.lineStart(line), offset)
    let col = 0
    for (const ch of text) col = ch === '\t' ? col + this.opts.tabSize - (col % this.opts.tabSize) : col + 1
    return col
  }

  scrollToOffset(offset: number, center = false) {
    const line = this.doc.lineAt(offset)
    const top = this.lineTop(line)
    const s = this.scroller
    if (center) s.scrollTop = Math.max(0, top - s.clientHeight / 3)
    else if (top < s.scrollTop) s.scrollTop = top - this.lineHeight
    else if (top + this.lineHeight * 2 > s.scrollTop + s.clientHeight) s.scrollTop = top + this.lineHeight * 2 - s.clientHeight
    const gutterW = this.gutter.getBoundingClientRect().width
    const x = this.visualCol(line, offset) * this.charWidth
    const view = s.clientWidth - gutterW
    if (x < s.scrollLeft) s.scrollLeft = Math.max(0, x - 40)
    else if (x > s.scrollLeft + view - 30) s.scrollLeft = x - view + 60
    this.schedule()
  }

  /** Offset under a mouse position. */
  offsetAt(x: number, y: number): number | null {
    const d = document as any
    // Right on the left edge, the point can fall in the gutter.
    x = Math.max(x, this.content.getBoundingClientRect().left + 1)
    if (d.caretPositionFromPoint) {
      const p = d.caretPositionFromPoint(x, y)
      if (p && this.content.contains(p.offsetNode)) return this.domOffset(p.offsetNode, p.offset)
    } else if (d.caretRangeFromPoint) {
      const r: Range | null = d.caretRangeFromPoint(x, y)
      if (r && this.content.contains(r.startContainer)) return this.domOffset(r.startContainer, r.startOffset)
    }
    return null
  }

  private hoverTimer = 0
  private onHover(e: MouseEvent) {
    clearTimeout(this.hoverTimer)
    this.tooltip.style.display = 'none'
    if (!this.diagnostics.length) return
    this.hoverTimer = window.setTimeout(() => {
      const off = this.offsetAt(e.clientX, e.clientY)
      if (off == null) return
      const found = this.diagnostics.filter((d) => off >= d.from && off <= Math.max(d.to, d.from + 1))
      if (!found.length) return
      this.showTooltip(e.clientX, e.clientY, found.map((d) => `${d.source ? `[${d.source}] ` : ''}${d.message}`).join('\n\n'), found[0].severity)
    }, 350)
  }

  showTooltip(x: number, y: number, text: string, kind = 'info') {
    const rect = this.root.getBoundingClientRect()
    this.tooltip.textContent = text
    this.tooltip.dataset.kind = kind
    this.tooltip.style.display = 'block'
    const left = Math.min(x - rect.left + 8, rect.width - 380)
    this.tooltip.style.left = `${Math.max(4, left)}px`
    this.tooltip.style.top = `${y - rect.top + 16}px`
  }

  hideTooltip() {
    this.tooltip.style.display = 'none'
  }

  /** Screen position of an offset (popups anchored at the caret). */
  coordsAt(offset: number) {
    const o = Math.min(offset, this.doc.text.length)
    const [n, off] = this.domAt(o)
    const r = document.createRange()
    r.setStart(n, off)
    r.setEnd(n, Math.min(off + 1, n.length))
    const rect = r.getClientRects()[0] ?? r.getBoundingClientRect()
    return { left: rect.left, top: rect.top, bottom: rect.bottom }
  }
}
