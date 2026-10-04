// Fold ranges: brackets for the C-like languages (outside strings and comments), the
// indentation for Python, YAML, HTML and plain text, the headings for Markdown. A range is
// a header line, which stays visible, and the last line it hides; with brackets, the line
// of the closing one stays visible too.
import type { Doc } from './doc'
import type { Highlighter } from './tokenizer'

export interface Fold {
  line: number
  end: number
}

type Mode = 'brackets' | 'indent' | 'headings'

function modeOf(lang: string): Mode {
  if (lang === 'markdown') return 'headings'
  if (['python', 'yaml', 'html', 'plaintext'].includes(lang)) return 'indent'
  return 'brackets'
}

const skipped = new Set(['string', 'comment', 'regexp', 'escape'])
const pairs: Record<string, string> = { '{': '}', '[': ']', '(': ')' }
const closers = new Set(Object.values(pairs))
/** Lines scanned at most to find the end of a range. */
const SCAN = 50000

const heading = (t: string) => /^(#{1,6})\s/.exec(t)?.[1].length ?? 0
const fence = (t: string) => /^\s*(```|~~~)/.test(t)

export class Folder {
  constructor(private doc: Doc, private hl: Highlighter, private tabSize: () => number) {}

  private get mode() {
    return modeOf(this.doc.lang)
  }

  /** Indentation of a line in columns, -1 for a blank line. */
  private indent(line: number) {
    const t = this.doc.lineText(line)
    const tab = this.tabSize()
    let col = 0
    for (let i = 0; i < t.length; i++) {
      const c = t.charCodeAt(i)
      if (c === 32) col++
      else if (c === 9) col += tab - (col % tab)
      else return col
    }
    return -1
  }

  /** Brackets of a line outside strings and comments. */
  private brackets(line: number, f: (c: string) => void) {
    const text = this.doc.lineText(line)
    const toks = this.hl.get(line)
    let k = 0
    for (let i = 0; i < text.length; i++) {
      while (k < toks.length && toks[k][1] <= i) k++
      if (k < toks.length && toks[k][0] <= i && skipped.has(toks[k][2])) {
        i = toks[k][1] - 1
        continue
      }
      const c = text[i]
      if (pairs[c] || closers.has(c)) f(c)
    }
  }

  /** Brackets left open at the end of a line. */
  private openAtEnd(line: number) {
    const stack: string[] = []
    this.brackets(line, (c) => {
      if (pairs[c]) stack.push(c)
      else if (stack.length && pairs[stack[stack.length - 1]] === c) stack.pop()
    })
    return stack
  }

  /** Cheap test for the gutter: the line seems to open a range. */
  canFold(line: number): boolean {
    const n = this.doc.lineCount
    if (line + 1 >= n) return false
    switch (this.mode) {
      case 'headings':
        return heading(this.doc.lineText(line)) > 0
      case 'indent': {
        const own = this.indent(line)
        if (own < 0) return false
        for (let l = line + 1; l < n && l - line < 200; l++) {
          const i = this.indent(l)
          if (i >= 0) return i > own
        }
        return false
      }
      default: {
        const open = this.openAtEnd(line)
        if (!open.length) return false
        // An empty block ({ then } on the next line) hides nothing.
        return this.doc.lineText(line + 1).trimStart()[0] !== pairs[open[open.length - 1]]
      }
    }
  }

  /** Last line hidden by the range a line opens, -1 when it opens none. */
  end(line: number): number {
    const n = this.doc.lineCount
    let last = -1
    switch (this.mode) {
      case 'headings': {
        const level = heading(this.doc.lineText(line))
        if (!level) return -1
        let code = false
        for (let l = line + 1; l < n && l - line < SCAN; l++) {
          const t = this.doc.lineText(l)
          if (fence(t)) code = !code
          if (!code && heading(t) && heading(t) <= level) break
          if (t.trim()) last = l
        }
        break
      }
      case 'indent': {
        const own = this.indent(line)
        if (own < 0) return -1
        for (let l = line + 1; l < n && l - line < SCAN; l++) {
          const i = this.indent(l)
          if (i < 0) continue
          if (i <= own) break
          last = l
        }
        break
      }
      default: {
        const open = this.openAtEnd(line)
        if (!open.length) return -1
        let depth = 1
        for (let l = line + 1; l < n && l - line < SCAN && depth > 0; l++) {
          this.brackets(l, (c) => {
            if (depth === 0) return
            if (pairs[c]) depth++
            else depth--
          })
          if (depth === 0) last = l - 1
        }
      }
    }
    return last > line ? last : -1
  }

  /** Every range of the document (fold all), at most one per header line. */
  all(): Fold[] {
    const n = this.doc.lineCount
    const out = new Map<number, number>()
    const add = (line: number, end: number) => end > line && !out.has(line) && out.set(line, end)
    if (this.mode === 'brackets') {
      const stack: number[] = []
      for (let l = 0; l < n; l++) {
        this.brackets(l, (c) => {
          if (pairs[c]) stack.push(l)
          else if (stack.length) add(stack.pop()!, l - 1)
        })
      }
    } else if (this.mode === 'indent') {
      const stack: { line: number; indent: number }[] = []
      let lastText = -1
      for (let l = 0; l <= n; l++) {
        const i = l < n ? this.indent(l) : 0
        if (i < 0) continue
        while (stack.length && stack[stack.length - 1].indent >= i) add(stack.pop()!.line, lastText)
        if (l < n) stack.push({ line: l, indent: i })
        lastText = l
      }
    } else {
      for (let l = 0; l < n; l++) if (heading(this.doc.lineText(l))) add(l, this.end(l))
    }
    return [...out].map(([line, end]) => ({ line, end })).sort((a, b) => a.line - b.line)
  }
}
