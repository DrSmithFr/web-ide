// Text edits in LSP terms (a range by line and UTF-16 column, a new text), turned into offsets
// of a text: the completion of the editor and the edits of the language servers use them.

export interface Range {
  start: { line: number; character: number }
  end: { line: number; character: number }
}

export interface TextEdit {
  range: Range
  newText: string
}

/** Offset of an LSP position in a text (UTF-16 columns, like JavaScript strings). */
export function offsetIn(text: string, starts: number[], line: number, character: number) {
  if (line >= starts.length) return text.length
  const end = line + 1 < starts.length ? starts[line + 1] - 1 : text.length
  return Math.min(starts[line] + character, end)
}

export function lineStarts(text: string) {
  const s = [0]
  for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', i + 1)) s.push(i + 1)
  return s
}

export function toOffsets(text: string, edits: TextEdit[]) {
  const starts = lineStarts(text)
  return edits.map((e) => ({
    from: offsetIn(text, starts, e.range.start.line, e.range.start.character),
    to: offsetIn(text, starts, e.range.end.line, e.range.end.character),
    text: e.newText,
  }))
}

/** Applies edits to a string (descending order, so offsets stay valid). */
export function applyToString(text: string, edits: TextEdit[]) {
  const list = toOffsets(text, edits).sort((a, b) => b.from - a.from || b.to - a.to)
  let out = text
  for (const e of list) out = out.slice(0, e.from) + e.text + out.slice(e.to)
  return out
}
