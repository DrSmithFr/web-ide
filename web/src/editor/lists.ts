// Markdown lists: Enter on an item opens the next one (same bullet, next number, an unchecked
// box), Enter on an empty item ends the list.

const item = /^([ \t]*)([-*+]|(\d+)([.)]))([ \t]+)(\[[ xX]\][ \t]+)?/

/**
 * Line break in a list item: `line` is the whole line, `col` the caret column. Returns the
 * text to insert at the caret, or `end` (the empty marker to remove); null outside a list.
 */
export function listBreak(line: string, col: number): { insert: string } | { end: number } | null {
  const m = item.exec(line)
  if (!m || col < m[0].length) return null
  if (!line.slice(m[0].length).trim()) return { end: m[0].length }
  const [, indent, bullet, num, delim, space, box] = m
  const marker = num ? `${Number(num) + 1}${delim}` : bullet
  return { insert: '\n' + indent + marker + space + (box ? '[ ] ' : '') }
}
