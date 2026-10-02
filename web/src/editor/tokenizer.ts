// Grammar engine. A grammar is data: for each state, an ordered list of {token, regex}
// rules; at the current position the first matching rule wins. Rules can push a state
// (multi-line comments, strings, embedded code) or pop back with "@pop".
// Adding a language never touches the engine.

export interface RuleDef {
  token: string
  regex: string
  flags?: string
  next?: string // state to push, or "@pop"
  include?: string // inline the rules of another state
}

export interface GrammarDef {
  id: string
  name: string
  extensions: string[]
  filenames?: string[]
  /** Regex tested on the first lines when the extension says nothing. */
  detect?: string
  states: Record<string, RuleDef[]>
}

interface Rule {
  token: string
  re: RegExp
  next?: string
}

export type Token = [start: number, end: number, type: string]

export class Grammar {
  states: Record<string, Rule[]> = {}
  errors: string[] = []

  constructor(public def: GrammarDef) {
    const expand = (name: string, seen: Set<string>): RuleDef[] => {
      const out: RuleDef[] = []
      for (const r of def.states[name] ?? []) {
        if (r.include) {
          if (!seen.has(r.include)) out.push(...expand(r.include, new Set([...seen, r.include])))
        } else out.push(r)
      }
      return out
    }
    for (const name of Object.keys(def.states)) {
      this.states[name] = []
      for (const r of expand(name, new Set([name]))) {
        try {
          const flags = (r.flags ?? '').replace(/[gy]/g, '') + 'y'
          this.states[name].push({ token: r.token, re: new RegExp(r.regex, flags), next: r.next })
        } catch (e) {
          this.errors.push(`${name}: /${r.regex}/ : ${(e as Error).message}`)
        }
      }
    }
    if (!this.states.root) this.states.root = []
  }

  /** Tokenizes one line starting in the state stack `stack` ("root/comment"). */
  line(text: string, stack: string): { tokens: Token[]; end: string } {
    const st = stack.split('/')
    const tokens: Token[] = []
    let pos = 0
    while (pos < text.length) {
      const rules = this.states[st[st.length - 1]] ?? this.states.root
      let matched = false
      for (const r of rules) {
        r.re.lastIndex = pos
        const m = r.re.exec(text)
        if (!m || m[0].length === 0) {
          if (m && r.next) {
            // Zero-width rule: change state only.
            if (r.next === '@pop') {
              if (st.length > 1) st.pop()
            } else st.push(r.next)
            matched = true
            break
          }
          continue
        }
        const end = pos + m[0].length
        if (r.token && r.token !== 'text') {
          const last = tokens[tokens.length - 1]
          if (last && last[1] === pos && last[2] === r.token) last[1] = end
          else tokens.push([pos, end, r.token])
        }
        pos = end
        if (r.next === '@pop') {
          if (st.length > 1) st.pop()
        } else if (r.next) st.push(r.next)
        matched = true
        break
      }
      if (!matched) pos++
      if (st.length > 32) st.splice(1, st.length - 32)
    }
    return { tokens, end: st.join('/') }
  }
}

/**
 * Highlighter keeps, for each line, the state at its start and its tokens. Lines are
 * tokenized lazily up to the last line asked for; an edit only invalidates the lines
 * from the edited one.
 */
export class Highlighter {
  private starts: string[] = ['root']
  private tokens: (Token[] | undefined)[] = []
  private valid = 0

  constructor(public grammar: Grammar, private lineText: (i: number) => string, private lineCount: () => number) {
    this.reset()
  }

  reset() {
    this.starts = new Array(this.lineCount() + 1).fill('root')
    this.tokens = new Array(this.lineCount())
    this.valid = 0
  }

  setGrammar(g: Grammar) {
    this.grammar = g
    this.reset()
  }

  /** Lines [fromLine, fromLine+oldLines) were replaced by newLines lines. */
  edit(fromLine: number, oldLines: number, newLines: number) {
    this.starts = splice(this.starts, fromLine + 1, oldLines, newLines, 'root')
    this.tokens = splice(this.tokens, fromLine, oldLines, newLines, undefined)
    this.valid = Math.min(this.valid, fromLine)
  }

  /** Tokens of a line, tokenizing the lines above it when needed. */
  get(line: number): Token[] {
    const n = this.lineCount()
    if (line >= n) return []
    while (this.valid <= line) {
      const i = this.valid
      const { tokens, end } = this.grammar.line(this.lineText(i), this.starts[i] ?? 'root')
      this.tokens[i] = tokens
      this.starts[i + 1] = end
      this.valid++
    }
    return this.tokens[line] ?? []
  }

  /** State at the start of a line (used to know whether a position is in a string or comment). */
  stateAt(line: number) {
    this.get(Math.max(0, line - 1))
    return this.starts[line] ?? 'root'
  }
}

/** Replaces n items at i by m copies of fill, without spreading (large pastes). */
function splice<T>(a: T[], i: number, n: number, m: number, fill: T): T[] {
  if (n === m) {
    for (let k = i; k < i + m && k < a.length; k++) a[k] = fill
    return a
  }
  const out = new Array<T>(a.length - n + m)
  for (let k = 0; k < i; k++) out[k] = a[k]
  for (let k = 0; k < m; k++) out[i + k] = fill
  for (let k = i + n; k < a.length; k++) out[k - n + m] = a[k]
  return out
}
