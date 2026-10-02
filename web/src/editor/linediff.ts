// Line diff for the diff view and the change markers of the gutter. The common head and
// tail are cut first: a big file with a small change stays cheap.
import { diffIndices } from 'node-diff3'

export interface Hunk {
  a: [start: number, length: number]
  b: [start: number, length: number]
}

const LIMIT = 8000

/** Hunks between lines a (old) and b (new). null when the changed middle is too big. */
export function lineHunks(a: string[], b: string[]): Hunk[] | null {
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++
  const ma = a.slice(head, a.length - tail)
  const mb = b.slice(head, b.length - tail)
  if (!ma.length && !mb.length) return []
  if (!ma.length || !mb.length) return [{ a: [head, ma.length], b: [head, mb.length] }]
  if (ma.length > LIMIT || mb.length > LIMIT) return null
  return diffIndices(ma, mb).map((h) => ({ a: [h.buffer1[0] + head, h.buffer1[1]], b: [h.buffer2[0] + head, h.buffer2[1]] }))
}

export type LineMark = 'add' | 'mod' | 'del'

/** Gutter markers of the new text: added, modified lines, and deletions (on the line after). */
export function lineMarks(oldText: string, newText: string): Map<number, LineMark> {
  const marks = new Map<number, LineMark>()
  const b = newText.split('\n')
  const hunks = lineHunks(oldText.split('\n'), b)
  if (!hunks) return marks
  for (const h of hunks) {
    const [bs, bl] = h.b
    if (bl === 0) marks.set(Math.min(bs, b.length - 1), 'del')
    else for (let i = bs; i < bs + bl; i++) marks.set(i, h.a[1] === 0 ? 'add' : 'mod')
  }
  return marks
}
