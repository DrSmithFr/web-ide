// The open buffers announced to the language servers: didOpen, didChange (a moment after the
// edits, or at once before a request: flushLsp), didSave, didClose.
import type { Doc } from '../api'
import { api } from './plugin'

const pending = new Map<string, () => void>()
const stops = new Map<string, () => void>()

/** Sends the pending change of a buffer to its server now (before a request). */
export function flushLsp(path: string) {
  pending.get(path)?.()
}

function follow(doc: Doc) {
  const lang = api.editor.serverLanguage(doc.path)
  if (!lang || doc.readOnly || stops.has(doc.path)) return
  const uri = api.project.fileUri(doc.path)
  let timer: number | undefined
  let dirty = false
  const send = () => {
    clearTimeout(timer)
    if (!dirty) return
    dirty = false
    api.notify('lsp.notify', { lang, method: 'textDocument/didChange', params: { textDocument: { uri, version: 0 }, contentChanges: [{ text: doc.text }] } })
  }
  pending.set(doc.path, send)
  api.notify('lsp.notify', { lang, method: 'textDocument/didOpen', params: { textDocument: { uri, languageId: api.editor.serverLanguageId(doc.path), version: 1, text: doc.text } } })
  const off = doc.onChange(() => {
    dirty = true
    clearTimeout(timer)
    timer = window.setTimeout(send, 250)
  })
  stops.set(doc.path, () => {
    off()
    clearTimeout(timer)
    pending.delete(doc.path)
    stops.delete(doc.path)
    api.notify('lsp.notify', { lang, method: 'textDocument/didClose', params: { textDocument: { uri } } })
  })
}

/** Starts following the buffers: those already open, and the next ones. */
export function watchBuffers() {
  for (const d of api.buffers.list()) follow(d)
  api.buffers.onOpen(follow)
  api.buffers.onClose((d) => stops.get(d.path)?.())
  api.buffers.onSave((d) => {
    const lang = api.editor.serverLanguage(d.path)
    if (lang) api.notify('lsp.notify', { lang, method: 'textDocument/didSave', params: { textDocument: { uri: api.project.fileUri(d.path) } } })
  })
}

/** Announces every buffer again (the plugin enabled again: the servers lost what it refused). */
export function resync() {
  for (const stop of [...stops.values()]) stop()
  for (const d of api.buffers.list()) follow(d)
}
