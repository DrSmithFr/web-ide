// EditorView renders a Doc in a contenteditable <pre> holding a single text node.
// Every edit is intercepted (beforeinput), applied to the Doc, then mirrored into the text
// node with replaceData, which keeps the node and the caret in place. Colors come from
// the CSS Custom Highlight API: one Highlight per token type, no <span> in the DOM. Only
// the visible lines (plus a margin) get ranges, rebuilt on the next frame after a change.
import { Highlighter, type Token } from './tokenizer'
import { grammar } from './languages'
import { subwordLeft, subwordRight } from './subword'
import type { Change, Doc, Selection } from './doc'

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
  readOnly?: boolean
  onSelection?: (sel: Selection) => void
  onCtrlClick?: (offset: number) => void
  onFocus?: () => void
  onScroll?: (top: number) => void
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

let viewSeq = 0

export class EditorView {
  readonly id = ++viewSeq
  readonly root: HTMLDivElement
  readonly scroller: HTMLDivElement
  readonly content: HTMLPreElement
  private gutter: HTMLPreElement
  private curLine: HTMLDivElement
  private boxes: HTMLDivElement
  private tooltip: HTMLDivElement
  private textNode: Text
  private hl: Highlighter
  private own = new Map<string, AbstractRange[]>()
  private live = new Map<string, Range[]>()
  private disposers: (() => void)[] = []
  private frame = 0
  private lineCountShown = -1
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
    this.gutter = document.createElement('pre')
    this.gutter.className = 'ed-gutter'
    this.gutter.setAttribute('aria-hidden', 'true')
    const main = document.createElement('div')
    main.className = 'ed-main'
    this.curLine = document.createElement('div')
    this.curLine.className = 'ed-curline'
    this.boxes = document.createElement('div')
    this.boxes.className = 'ed-boxes'
    this.content = document.createElement('pre')
    this.content.className = 'ed-content'
    this.content.spellcheck = false
    this.content.setAttribute('autocapitalize', 'off')
    this.content.setAttribute('autocorrect', 'off')
    this.content.setAttribute('role', 'textbox')
    this.content.setAttribute('aria-multiline', 'true')
    this.setReadOnly(!!opts.readOnly || doc.readOnly)
    this.textNode = document.createTextNode(doc.text + '\n')
    this.content.appendChild(this.textNode)
    this.tooltip = document.createElement('div')
    this.tooltip.className = 'ed-tooltip'
    main.append(this.curLine, this.boxes, this.content)
    inner.append(this.gutter, main)
    this.scroller.append(inner)
    this.root.append(this.scroller, this.tooltip)
    this.root.style.setProperty('--tab-size', String(opts.tabSize))

    this.hl = new Highlighter(grammar(doc.lang), (i) => doc.lineText(i), () => doc.lineCount)

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
    listen(this.content, 'mousemove', (e) => this.onHover(e))
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
    probe.style.cssText = 'position:absolute;visibility:hidden;white-space:pre;font:inherit'
    this.content.appendChild(probe)
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
    for (const [name, ranges] of this.live) {
      const h = highlight(name)
      for (const r of ranges) h?.delete(r)
    }
    this.live.clear()
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
    if (node === this.textNode) return Math.min(off, len)
    if (node === this.content) return off === 0 ? 0 : len
    if (node && this.content.contains(node)) {
      // Unexpected structure (during composition): count the text before the node.
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

  setSelection(anchor: number, head = anchor, scroll = true) {
    const len = this.doc.text.length
    anchor = Math.max(0, Math.min(anchor, len))
    head = Math.max(0, Math.min(head, len))
    this.lastSel = { anchor, head }
    if (document.activeElement !== this.content) this.focus()
    const sel = document.getSelection()
    sel?.setBaseAndExtent(this.textNode, anchor, this.textNode, head)
    if (scroll) this.scrollToOffset(head)
    this.updateCurLine()
    this.opts.onSelection?.(this.lastSel)
  }

  /** Restores a selection without taking the focus (tab switch). */
  restoreSelection(sel: Selection) {
    this.lastSel = sel
    this.updateCurLine()
  }

  private onSelectionChange() {
    const sel = document.getSelection()
    if (!sel?.anchorNode || !this.content.contains(sel.anchorNode)) return
    const prev = this.lastSel
    const cur = this.getSelection()
    if (prev.head !== cur.head) this.doc.breakUndoGroup()
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
    if (!(o && o.view === this && o.domDone)) {
      this.textNode.replaceData(c.from, c.to - c.from, c.text)
    }
    // Keep this view's selection in place when another view or the pod edits.
    const delta = c.text.length - (c.to - c.from)
    const map = (p: number) => (p <= c.from ? p : p >= c.to ? p + delta : c.from + c.text.length)
    if (o !== this) this.lastSel = { anchor: map(this.lastSel.anchor), head: map(this.lastSel.head) }
    if (this.statement) this.statement = null
    this.hl.edit(c.fromLine, c.oldLines, c.newLines)
    this.schedule()
  }

  /** The DOM changed without going through beforeinput (IME composition, autocorrect). */
  private syncFromDom() {
    if (this.composing) return
    if (this.content.childNodes.length !== 1 || this.content.firstChild !== this.textNode) {
      const sel = this.getSelection()
      const text = this.content.textContent ?? ''
      this.textNode = document.createTextNode(text.endsWith('\n') ? text : text + '\n')
      this.content.replaceChildren(this.textNode)
      this.clearOwn()
      this.lastSel = sel
    }
    const dom = this.textNode.data
    const expected = this.doc.text + '\n'
    if (dom === expected) return
    const sel = this.getSelection()
    let a = 0
    const max = Math.min(dom.length, expected.length)
    while (a < max && dom.charCodeAt(a) === expected.charCodeAt(a)) a++
    let b = 0
    while (b < max - a && dom.charCodeAt(dom.length - 1 - b) === expected.charCodeAt(expected.length - 1 - b)) b++
    let inserted = dom.slice(a, dom.length - b)
    let to = expected.length - b
    if (to > this.doc.text.length) to = this.doc.text.length
    if (!dom.endsWith('\n')) {
      this.textNode.appendData('\n')
    }
    inserted = inserted.replace(/\r\n?/g, '\n')
    if (this.readOnly) {
      this.textNode.data = expected
      return
    }
    this.doc.replace(a, to, inserted, { view: this, domDone: true }, { selAfter: sel })
    if (this.textNode.data !== this.doc.text + '\n') this.textNode.data = this.doc.text + '\n'
    this.setSelection(sel.anchor, sel.head, false)
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
    }
  }

  private newline(from: number, to: number) {
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
        this.edit(from, to, '\n' + inner + '\n' + indent, from + 1 + inner.length)
        return
      }
      indent = inner
    }
    this.edit(from, to, '\n' + indent)
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
    if (this.readOnly) return
    const text = (e.clipboardData?.getData('text/plain') ?? '').replace(/\r\n?/g, '\n')
    const sel = this.getSelection()
    this.edit(Math.min(sel.anchor, sel.head), Math.max(sel.anchor, sel.head), text)
  }

  private onKeyDown(e: KeyboardEvent) {
    if (e.defaultPrevented || this.composing) return
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
    const sel = this.getSelection()
    const head = dir < 0 ? subwordLeft(this.doc.text, sel.head) : subwordRight(this.doc.text, sel.head)
    this.setSelection(extend ? sel.anchor : head, head)
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

  private addOwn(name: string, r: AbstractRange) {
    let l = this.own.get(name)
    if (!l) this.own.set(name, (l = []))
    l.push(r)
    highlight(name)?.add(r)
  }

  visibleLines(): [number, number] {
    const top = this.scroller.scrollTop
    const h = this.scroller.clientHeight || 800
    const first = Math.max(0, Math.floor((top - this.padTop) / this.lineHeight))
    const last = Math.min(this.doc.lineCount - 1, Math.ceil((top + h) / this.lineHeight))
    return [first, last]
  }

  render() {
    if (!this.root.isConnected) return
    const n = this.doc.lineCount
    if (n !== this.lineCountShown) {
      let s = ''
      for (let i = 1; i <= n; i++) s += i + '\n'
      this.gutter.textContent = s
      this.gutter.style.minWidth = `${String(n).length + 2}ch`
      this.lineCountShown = n
    }
    this.clearOwn()
    if (registry()) {
      const [first, last] = this.visibleLines()
      const a = Math.max(0, first - 40)
      const b = Math.min(n - 1, last + 40)
      const len = this.textNode.length
      for (let line = a; line <= b; line++) {
        const base = this.doc.lineStart(line)
        const tokens: Token[] = this.hl.get(line)
        for (const [s, e, type] of tokens) {
          if (base + e > len) break
          this.addOwn('tok-' + type, new StaticRange({ startContainer: this.textNode, startOffset: base + s, endContainer: this.textNode, endOffset: base + e }))
        }
      }
    }
    this.renderDiagnostics()
    this.renderStatement()
    this.updateCurLine()
  }

  private updateCurLine() {
    const show = this.opts.highlightLine && this.lastSel.anchor === this.lastSel.head
    this.curLine.style.display = show ? 'block' : 'none'
    if (show) {
      const line = this.doc.lineAt(this.lastSel.head)
      this.curLine.style.transform = `translateY(${this.padTop + line * this.lineHeight}px)`
      this.curLine.style.height = `${this.lineHeight}px`
    }
  }

  /** Ranges that follow the edits by themselves (search results, diagnostics). */
  setLiveRanges(name: string, spans: [number, number][], priority = 1) {
    const h = highlight(name, priority)
    for (const r of this.live.get(name) ?? []) h?.delete(r)
    const list: Range[] = []
    const len = this.doc.text.length
    for (const [from, to] of spans) {
      if (from > len) continue
      const r = document.createRange()
      r.setStart(this.textNode, Math.min(from, len))
      r.setEnd(this.textNode, Math.min(to, len))
      list.push(r)
      h?.add(r)
    }
    this.live.set(name, list)
  }

  setDiagnostics(list: Diagnostic[]) {
    this.diagnostics = list
    this.renderDiagnostics()
  }

  private renderDiagnostics() {
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
    box.style.top = `${this.padTop + l1 * this.lineHeight - 1}px`
    box.style.height = `${(l2 - l1 + 1) * this.lineHeight + 2}px`
    box.style.width = `${maxCol * this.charWidth + 12}px`
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
    const top = this.padTop + line * this.lineHeight
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
    const r = document.createRange()
    const o = Math.min(offset, this.doc.text.length)
    r.setStart(this.textNode, o)
    r.setEnd(this.textNode, Math.min(o + 1, this.textNode.length))
    const rect = r.getClientRects()[0] ?? r.getBoundingClientRect()
    return { left: rect.left, top: rect.top, bottom: rect.bottom }
  }
}
