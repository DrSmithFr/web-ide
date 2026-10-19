// ANSI SGR sequences of log lines turned into styled segments (colors, bold, dim, italic,
// underline); other escape sequences are dropped.

export interface Segment {
  text: string
  style?: string
}

const base = ['#000000', '#cd3131', '#0dbc79', '#e5e510', '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5']
const bright = ['#666666', '#f14c4c', '#23d18b', '#f5f543', '#3b8eea', '#d670d6', '#29b8db', '#ffffff']

function color256(n: number): string {
  if (n < 8) return base[n]
  if (n < 16) return bright[n - 8]
  if (n < 232) {
    const v = [0, 95, 135, 175, 215, 255]
    n -= 16
    return `rgb(${v[Math.floor(n / 36)]},${v[Math.floor(n / 6) % 6]},${v[n % 6]})`
  }
  const g = 8 + (n - 232) * 10
  return `rgb(${g},${g},${g})`
}

interface State {
  fg?: string
  bg?: string
  bold?: boolean
  dim?: boolean
  italic?: boolean
  underline?: boolean
}

function css(s: State): string | undefined {
  const out: string[] = []
  if (s.fg) out.push(`color:${s.fg}`)
  if (s.bg) out.push(`background:${s.bg}`)
  if (s.bold) out.push('font-weight:600')
  if (s.dim) out.push('opacity:.65')
  if (s.italic) out.push('font-style:italic')
  if (s.underline) out.push('text-decoration:underline')
  return out.length ? out.join(';') : undefined
}

/** Extended color (38/48;5;n or 38/48;2;r;g;b) starting at codes[i]; returns [color, next index]. */
function extended(codes: number[], i: number): [string | undefined, number] {
  if (codes[i + 1] === 5) return [color256(codes[i + 2] ?? 0), i + 3]
  if (codes[i + 1] === 2) return [`rgb(${codes[i + 2] ?? 0},${codes[i + 3] ?? 0},${codes[i + 4] ?? 0})`, i + 5]
  return [undefined, i + 1]
}

function apply(s: State, codes: number[]) {
  for (let i = 0; i < codes.length; ) {
    const c = codes[i]
    if (c === 0) Object.keys(s).forEach((k) => delete s[k as keyof State])
    else if (c === 1) s.bold = true
    else if (c === 2) s.dim = true
    else if (c === 3) s.italic = true
    else if (c === 4) s.underline = true
    else if (c === 22) s.bold = s.dim = false
    else if (c === 23) s.italic = false
    else if (c === 24) s.underline = false
    else if (c >= 30 && c <= 37) s.fg = base[c - 30]
    else if (c >= 90 && c <= 97) s.fg = bright[c - 90]
    else if (c === 39) s.fg = undefined
    else if (c >= 40 && c <= 47) s.bg = base[c - 40]
    else if (c >= 100 && c <= 107) s.bg = bright[c - 100]
    else if (c === 49) s.bg = undefined
    else if (c === 38 || c === 48) {
      const [col, next] = extended(codes, i)
      if (c === 38) s.fg = col
      else s.bg = col
      i = next
      continue
    }
    i++
  }
}

// CSI sequences (ESC [ … final byte) and OSC sequences (ESC ] … BEL or ESC \).
const escape = /\x1b\[([0-9;?]*)([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-_]/g

/** Segments of a line; a line without escape gives one plain segment. */
export function parseAnsi(line: string): Segment[] {
  if (!line.includes('\x1b')) return [{ text: line }]
  const out: Segment[] = []
  const st: State = {}
  let last = 0
  for (const m of line.matchAll(escape)) {
    if (m.index! > last) out.push({ text: line.slice(last, m.index), style: css(st) })
    if (m[2] === 'm') apply(st, (m[1] || '0').split(';').map((x) => parseInt(x, 10) || 0))
    last = m.index! + m[0].length
  }
  if (last < line.length) out.push({ text: line.slice(last), style: css(st) })
  return out
}

/** Text of a line without its escape sequences (for the filter). */
export const stripAnsi = (line: string) => line.replace(escape, '')
