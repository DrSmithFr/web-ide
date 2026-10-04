// Indentation of a file: tabs or spaces, and the number of spaces of a level.

export interface Indent {
  tabs: boolean
  /** Spaces of a level (with tabs: unused, the tab size of the settings applies). */
  size: number
}

/** Indentation used by a text: the most frequent kind of indented lines, and for spaces the most frequent increase. Null when nothing is indented. */
export function detectIndent(text: string): Indent | null {
  let tabs = 0
  let spaces = 0
  let prev = 0
  const steps = new Map<number, number>()
  const lines = text.split('\n', 2000)
  for (const line of lines) {
    if (!line.trim()) continue
    if (line[0] === '\t') {
      tabs++
      continue
    }
    const n = /^ */.exec(line)![0].length
    if (n > 0) spaces++
    if (n > prev && n - prev <= 8) steps.set(n - prev, (steps.get(n - prev) ?? 0) + 1)
    prev = n
  }
  if (!tabs && !spaces) return null
  if (tabs >= spaces) return { tabs: true, size: 4 }
  let size = 4
  let best = 0
  for (const [d, c] of steps) if (c > best || (c === best && d < size)) [size, best] = [d, c]
  return { tabs: false, size }
}
