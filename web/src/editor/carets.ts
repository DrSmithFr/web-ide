// Helpers of the multiple carets: words, occurrences, normalization of a selection list.
import type { Selection } from './doc'

const wordChar = /[\p{L}\p{N}_$]/u
const isWord = (c: string | undefined) => !!c && wordChar.test(c)
const isSpace = (c: string | undefined) => c === ' ' || c === '\t'

export const selFrom = (s: Selection) => Math.min(s.anchor, s.head)
export const selTo = (s: Selection) => Math.max(s.anchor, s.head)

/** Word around an offset (or just before it), null outside a word. */
export function wordAt(text: string, offset: number): [number, number] | null {
  let a = offset
  let b = offset
  while (a > 0 && isWord(text[a - 1])) a--
  while (b < text.length && isWord(text[b])) b++
  return a < b ? [a, b] : null
}

/** Start of the previous word (Ctrl+Left): spaces, then a run of word characters or of punctuation. */
export function wordLeft(text: string, offset: number): number {
  let p = offset
  if (p > 0 && text[p - 1] === '\n') return p - 1
  while (p > 0 && isSpace(text[p - 1])) p--
  if (p > 0 && isWord(text[p - 1])) while (p > 0 && isWord(text[p - 1])) p--
  else while (p > 0 && !isWord(text[p - 1]) && !isSpace(text[p - 1]) && text[p - 1] !== '\n') p--
  return p
}

/** End of the next word (Ctrl+Right). */
export function wordRight(text: string, offset: number): number {
  let p = offset
  if (text[p] === '\n') return p + 1
  while (p < text.length && isSpace(text[p])) p++
  if (isWord(text[p])) while (p < text.length && isWord(text[p])) p++
  else while (p < text.length && !isWord(text[p]) && !isSpace(text[p]) && text[p] !== '\n') p++
  return p
}

function wholeWordAt(text: string, from: number, to: number) {
  return !isWord(text[from - 1]) && !isWord(text[to])
}

/** Offsets of all the occurrences of needle (whole words only when asked). */
export function occurrences(text: string, needle: string, whole: boolean, limit = 10000): number[] {
  const out: number[] = []
  if (!needle) return out
  for (let i = text.indexOf(needle); i >= 0 && out.length < limit; i = text.indexOf(needle, i + needle.length)) {
    if (!whole || wholeWordAt(text, i, i + needle.length)) out.push(i)
  }
  return out
}

/** Next occurrence after `after` (wrapping around) not already selected, or null. */
export function nextOccurrence(text: string, needle: string, after: number, whole: boolean, taken: Selection[]): number | null {
  const all = occurrences(text, needle, whole)
  if (!all.length) return null
  const free = (i: number) => !taken.some((s) => selFrom(s) === i && selTo(s) === i + needle.length)
  const start = all.findIndex((i) => i >= after)
  for (let k = 0; k < all.length; k++) {
    const i = all[((start < 0 ? 0 : start) + k) % all.length]
    if (free(i)) return i
  }
  return null
}

/**
 * Sorts selections by position and merges the ones that overlap (or carets at the same
 * place). Returns the list and the new index of the primary selection.
 */
export function normalize(list: Selection[], primary: number): { list: Selection[]; primary: number } {
  const items = list.map((s, i) => ({ s, p: i === primary }))
  items.sort((x, y) => selFrom(x.s) - selFrom(y.s) || selTo(x.s) - selTo(y.s))
  const out: { s: Selection; p: boolean }[] = []
  for (const it of items) {
    const last = out[out.length - 1]
    if (last && (selFrom(it.s) < selTo(last.s) || (selFrom(it.s) === selTo(last.s) && (selFrom(it.s) === selTo(it.s) || selFrom(last.s) === selTo(last.s))))) {
      // Merged: the union, oriented as the later one.
      const from = Math.min(selFrom(last.s), selFrom(it.s))
      const to = Math.max(selTo(last.s), selTo(it.s))
      const back = it.s.head < it.s.anchor
      last.s = back ? { anchor: to, head: from } : { anchor: from, head: to }
      last.p = last.p || it.p
    } else out.push({ s: { ...it.s }, p: it.p })
  }
  const p = out.findIndex((x) => x.p)
  return { list: out.map((x) => x.s), primary: p < 0 ? out.length - 1 : p }
}
