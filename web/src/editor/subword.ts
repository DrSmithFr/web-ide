// Sub-word navigation (Alt+Arrow). Boundaries: foo|Bar, XML|Http|Request (before the last
// capital of a run followed by a lowercase letter), both sides of "_" and "-", between
// letters and digits (item|2). Same behavior in every browser.

type Kind = 'lower' | 'upper' | 'digit' | 'sep' | 'space' | 'newline' | 'other'

function kind(ch: string | undefined): Kind {
  if (ch === undefined) return 'other'
  if (ch === '\n') return 'newline'
  if (ch === '_' || ch === '-') return 'sep'
  if (/\s/.test(ch)) return 'space'
  if (/\p{Nd}/u.test(ch)) return 'digit'
  if (/\p{Lu}/u.test(ch)) return 'upper'
  if (/\p{L}/u.test(ch)) return 'lower'
  return 'other'
}

export function subwordRight(text: string, pos: number): number {
  const n = text.length
  if (pos >= n) return n
  let i = pos
  while (i < n && kind(text[i]) === 'space') i++
  if (i > pos && i < n && kind(text[i]) === 'newline') return i
  if (i >= n) return n
  const k = kind(text[i])
  switch (k) {
    case 'newline':
      return i + 1
    case 'lower':
      while (i < n && kind(text[i]) === 'lower') i++
      return i
    case 'upper': {
      const start = i
      while (i < n && kind(text[i]) === 'upper') i++
      if (i - start === 1) {
        while (i < n && kind(text[i]) === 'lower') i++
        return i
      }
      // XMLHttp: stop before the capital that starts the next word.
      if (i < n && kind(text[i]) === 'lower') return i - 1
      return i
    }
    default:
      while (i < n && kind(text[i]) === k) i++
      return i
  }
}

export function subwordLeft(text: string, pos: number): number {
  if (pos <= 0) return 0
  let i = pos
  while (i > 0 && kind(text[i - 1]) === 'space') i--
  if (i < pos && i > 0 && kind(text[i - 1]) === 'newline') return i
  if (i <= 0) return 0
  const k = kind(text[i - 1])
  switch (k) {
    case 'newline':
      return i - 1
    case 'lower':
      while (i > 0 && kind(text[i - 1]) === 'lower') i--
      if (i > 0 && kind(text[i - 1]) === 'upper') i--
      return i
    case 'upper': {
      while (i > 0 && kind(text[i - 1]) === 'upper') i--
      return i
    }
    default:
      while (i > 0 && kind(text[i - 1]) === k) i--
      return i
  }
}
