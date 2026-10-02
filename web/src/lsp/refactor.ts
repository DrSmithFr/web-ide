// Rename a symbol (textDocument/rename, every file of the project) and reformat a document
// or the selection (textDocument/formatting, rangeFormatting).
import { request } from '../pod/rpc'
import type { Doc } from '../editor/doc'
import type { EditorView } from '../editor/view'
import { lspLanguage } from '../editor/languages'
import { fileUri, flushLsp, relPath } from '../state/project'
import { settings } from '../state/settings'
import { prompt } from '../ui/overlay'
import { toast } from '../ui/toast'
import { applyWorkspaceEdit, capabilities, toOffsets } from './edits'
import { wordStart, isWordChar } from './completion'

function lsp(doc: Doc, method: string, params: object) {
  return request('lsp.request', { lang: lspLanguage(doc.path), method, params })
}

export async function renameSymbol(v: EditorView, doc: Doc) {
  const caps = await capabilities(doc.path)
  if (!caps?.renameProvider) {
    toast('Le serveur de langage de ce fichier ne sait pas renommer', 'info')
    return
  }
  flushLsp(doc.path)
  const offset = v.getSelection().head
  const { line, col } = doc.pos(offset)
  const position = { line, character: col }
  const textDocument = { uri: fileUri(doc.path) }
  let current = ''
  try {
    if (typeof caps.renameProvider === 'object' && caps.renameProvider.prepareProvider) {
      const r = await lsp(doc, 'textDocument/prepareRename', { textDocument, position })
      if (!r) {
        toast("Rien à renommer à cet endroit", 'info')
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
    while (end < doc.text.length && isWordChar(doc.text[end], doc.lang)) end++
    current = doc.text.slice(wordStart(doc, offset), end)
  }
  const name = await prompt({ title: 'Renommer', label: `Nouveau nom pour « ${current} »`, value: current })
  v.focus()
  if (!name || name === current) return
  try {
    const edit = await lsp(doc, 'textDocument/rename', { textDocument, position, newName: name })
    if (!edit) {
      toast('Renommage refusé par le serveur', 'info')
      return
    }
    const sel = v.getSelection()
    const r = await applyWorkspaceEdit(edit, 'rename')
    v.setSelection(Math.min(sel.anchor, doc.text.length), Math.min(sel.head, doc.text.length), false)
    let msg = `${current} → ${name} : ${r.edits} modification(s) dans ${r.files} fichier(s)`
    if (r.written.length) msg += ` (dont ${r.written.length} fichier(s) non ouverts enregistrés : ${r.written.map(relPath).slice(0, 3).join(', ')}${r.written.length > 3 ? '…' : ''})`
    toast(msg, 'ok', undefined, 6000)
    if (r.skipped.length) toast(`Non appliqué : ${r.skipped.join(', ')}`, 'warn')
  } catch (e) {
    toast(`Renommage impossible : ${(e as Error).message}`, 'error')
  }
}

export async function formatDocument(v: EditorView, doc: Doc) {
  if (doc.readOnly) return
  const caps = await capabilities(doc.path)
  const sel = v.getSelection()
  const ranged = sel.anchor !== sel.head && caps?.documentRangeFormattingProvider
  if (!caps?.documentFormattingProvider && !ranged) {
    toast('Pas de formateur pour ce fichier (serveur de langage)', 'info')
    return
  }
  flushLsp(doc.path)
  const options = { tabSize: settings.editor.tabSize, insertSpaces: settings.editor.insertSpaces, trimTrailingWhitespace: true, insertFinalNewline: true, trimFinalNewlines: true }
  const textDocument = { uri: fileUri(doc.path) }
  const version = doc.version
  try {
    let edits
    if (ranged) {
      const a = doc.pos(Math.min(sel.anchor, sel.head))
      const b = doc.pos(Math.max(sel.anchor, sel.head))
      edits = await lsp(doc, 'textDocument/rangeFormatting', { textDocument, options, range: { start: { line: a.line, character: a.col }, end: { line: b.line, character: b.col } } })
    } else edits = await lsp(doc, 'textDocument/formatting', { textDocument, options })
    if (doc.version !== version) {
      toast('Le texte a changé pendant le formatage : relancer', 'warn')
      return
    }
    if (!edits?.length) {
      toast('Déjà bien formaté', 'info', undefined, 1500)
      return
    }
    const map = doc.applyEdits(toOffsets(doc.text, edits), 'format')
    v.setSelection(map(sel.anchor), map(sel.head), false)
    toast(`Code reformaté (${edits.length} modification(s))`, 'ok', undefined, 2000)
  } catch (e) {
    toast(`Formatage impossible : ${(e as Error).message}`, 'error')
  }
}
