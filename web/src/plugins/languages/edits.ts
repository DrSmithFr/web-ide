// LSP text edits and workspace edits, applied to the open buffers or, for the files that are
// not open, written directly through the pod.
import { api } from './plugin'
import type { TextEdit } from '../api'
export type { TextEdit }
export const toOffsets: typeof api.editor.toOffsets = (...a) => api.editor.toOffsets(...a)
const applyToString: typeof api.editor.applyToString = (...a) => api.editor.applyToString(...a)

const caps = new Map<string, Promise<any>>()

/** Server capabilities of a language (from initialize), cached. */
export function capabilities(path: string): Promise<any> {
  const lang = api.editor.serverLanguage(path)
  if (!lang) return Promise.resolve(null)
  let p = caps.get(lang)
  if (!p) {
    p = api.rpc('lsp.capabilities', { lang }).catch(() => {
      caps.delete(lang)
      return null
    })
    caps.set(lang, p)
  }
  return p
}

export interface WorkspaceResult {
  files: number
  edits: number
  written: string[]
  skipped: string[]
}

/** Applies a WorkspaceEdit: open buffers are modified (to be saved), other files are written. */
export async function applyWorkspaceEdit(we: any, origin: unknown): Promise<WorkspaceResult> {
  const byPath = new Map<string, TextEdit[]>()
  const skipped: string[] = []
  const add = (uri: string, edits: TextEdit[]) => {
    const p = api.project.pathFromUri(uri)
    byPath.set(p, [...(byPath.get(p) ?? []), ...edits])
  }
  for (const [uri, edits] of Object.entries<TextEdit[]>(we?.changes ?? {})) add(uri, edits)
  for (const dc of we?.documentChanges ?? []) {
    if (dc.textDocument) add(dc.textDocument.uri, dc.edits)
    else skipped.push(`${dc.kind} ${dc.uri ?? dc.oldUri ?? ''}`)
  }
  const res: WorkspaceResult = { files: 0, edits: 0, written: [], skipped }
  for (const [path, edits] of byPath) {
    if (!edits.length) continue
    res.files++
    res.edits += edits.length
    const doc = api.buffers.get(path)
    if (doc && !doc.readOnly) {
      doc.applyEdits(toOffsets(doc.text, edits), origin)
      continue
    }
    const f = await api.files.read(path)
    if (f.binary || f.readOnly) {
      skipped.push(path)
      continue
    }
    await api.files.write(path, applyToString(f.content, edits))
    res.written.push(path)
  }
  return res
}
