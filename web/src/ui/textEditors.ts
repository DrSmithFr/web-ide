// The text editors shown in the window (the Code plugin's views of the file tabs register theirs),
// for the active one: the code actions of the plugins run on it (plugins/api.ts api.editor.active).
import type { Doc } from '../editor/doc'
import { EditorView } from '../editor/view'

export type TextEditor = { view: EditorView; doc: Doc }

const editors = new Set<() => TextEditor | null>()

/** Registers a text editor: f gives it while it is the active one, null otherwise. */
export function registerTextEditor(f: () => TextEditor | null) {
  editors.add(f)
  return () => editors.delete(f)
}

/** The text editor of the active tab and its buffer; null when another editor has the focus. */
export function activeTextEditor(): TextEditor | null {
  for (const e of editors) {
    const ed = e()
    if (!ed) continue
    // Another editor has the focus (the message box of the assistant): its own actions run.
    const other = EditorView.of(document.activeElement)
    return other && other !== ed.view ? null : ed
  }
  return null
}
