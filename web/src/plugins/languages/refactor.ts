// Rename a symbol (textDocument/rename, every file of the project) and reformat a document
// or the selection (textDocument/formatting, rangeFormatting).
import type { Doc, EditorView } from '../api'
import { api, prompt, t, toast } from './plugin'
import { applyWorkspaceEdit, capabilities, toOffsets } from './edits'
import { flushLsp } from './sync'

function lsp(doc: Doc, method: string, params: object) {
  return api.rpc('lsp.request', { lang: api.editor.serverLanguage(doc.path), method, params })
}

export async function renameSymbol(v: EditorView, doc: Doc) {
  const caps = await capabilities(doc.path)
  if (!caps?.renameProvider) {
    toast(t('The language server of this file cannot rename'), 'info')
    return
  }
  flushLsp(doc.path)
  const offset = v.getSelection().head
  const { line, col } = doc.pos(offset)
  const position = { line, character: col }
  const textDocument = { uri: api.project.fileUri(doc.path) }
  let current = ''
  try {
    if (typeof caps.renameProvider === 'object' && caps.renameProvider.prepareProvider) {
      const r = await lsp(doc, 'textDocument/prepareRename', { textDocument, position })
      if (!r) {
        toast(t('Nothing to rename here'), 'info')
        return
      }
      if (r.placeholder) current = r.placeholder
      else if (r.start || r.range) {
        const range = r.range ?? r
        const [o] = toOffsets(doc.text, [{ range, newText: '' }])
        current = doc.text.slice(o.from, o.to)
      }
    }
  } catch (e) {
    toast((e as Error).message, 'info')
    return
  }
  if (!current) {
    let end = offset
    while (end < doc.text.length && api.editor.isWordChar(doc.text[end], doc.lang)) end++
    current = doc.text.slice(api.editor.wordStart(doc, offset), end)
  }
  const name = await prompt({ title: t('Rename'), label: t('New name for “{name}”', { name: current }), value: current })
  v.focus()
  if (!name || name === current) return
  try {
    const edit = await lsp(doc, 'textDocument/rename', { textDocument, position, newName: name })
    if (!edit) {
      toast(t('Rename refused by the server'), 'info')
      return
    }
    const sel = v.getSelection()
    const r = await applyWorkspaceEdit(edit, 'rename')
    v.setSelection(Math.min(sel.anchor, doc.text.length), Math.min(sel.head, doc.text.length), false)
    let msg = `${current} → ${name}: ${t('{edits} change(s) in {files} file(s)', { edits: r.edits, files: r.files })}`
    if (r.written.length) msg += ` (${t('including {n} closed file(s) saved: {list}', { n: r.written.length, list: r.written.map(api.project.relPath).slice(0, 3).join(', ') + (r.written.length > 3 ? '…' : '') })})`
    toast(msg, 'ok', undefined, 6000)
    if (r.skipped.length) toast(t('Not applied: {list}', { list: r.skipped.join(', ') }), 'warn')
  } catch (e) {
    toast(t('Rename failed: {message}', { message: (e as Error).message }), 'error')
  }
}

export async function formatDocument(v: EditorView, doc: Doc) {
  if (doc.readOnly) return
  const caps = await capabilities(doc.path)
  const sel = v.getSelection()
  const ranged = sel.anchor !== sel.head && caps?.documentRangeFormattingProvider
  if (!caps?.documentFormattingProvider && !ranged) {
    toast(t('No formatter for this file (language server)'), 'info')
    return
  }
  flushLsp(doc.path)
  const options = { tabSize: api.editor.settings().tabSize, insertSpaces: api.editor.settings().insertSpaces, trimTrailingWhitespace: true, insertFinalNewline: true, trimFinalNewlines: true }
  const textDocument = { uri: api.project.fileUri(doc.path) }
  const version = doc.version
  try {
    let edits
    if (ranged) {
      const a = doc.pos(Math.min(sel.anchor, sel.head))
      const b = doc.pos(Math.max(sel.anchor, sel.head))
      edits = await lsp(doc, 'textDocument/rangeFormatting', { textDocument, options, range: { start: { line: a.line, character: a.col }, end: { line: b.line, character: b.col } } })
    } else edits = await lsp(doc, 'textDocument/formatting', { textDocument, options })
    if (doc.version !== version) {
      toast(t('The text changed during the formatting: try again'), 'warn')
      return
    }
    if (!edits?.length) {
      toast(t('Already well formatted'), 'info', undefined, 1500)
      return
    }
    const map = doc.applyEdits(toOffsets(doc.text, edits), 'format')
    v.setSelection(map(sel.anchor), map(sel.head), false)
    toast(t('Code reformatted ({n} change(s))', { n: edits.length }), 'ok', undefined, 2000)
  } catch (e) {
    toast(t('Formatting failed: {message}', { message: (e as Error).message }), 'error')
  }
}
