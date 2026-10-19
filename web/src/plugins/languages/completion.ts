// The completion of the language servers: textDocument/completion and completionItem/resolve.
import type { CompletionItem, CompletionList, Doc } from '../api'
import { api } from './plugin'
import { capabilities } from './edits'
import { flushLsp } from './sync'

/** The items of the server of a file, null when it has none (the editor offers its words). */
export async function complete(doc: Doc, offset: number, trigger: string | null, signal: AbortSignal): Promise<CompletionList | null> {
  const lang = api.editor.serverLanguage(doc.path)
  const caps = lang ? await capabilities(doc.path) : null
  if (!caps?.completionProvider) return null
  flushLsp(doc.path)
  const { line, col } = doc.pos(offset)
  const res = await api.rpc(
    'lsp.request',
    {
      lang,
      method: 'textDocument/completion',
      params: {
        textDocument: { uri: api.project.fileUri(doc.path) },
        position: { line, character: col },
        context: trigger ? { triggerKind: 2, triggerCharacter: trigger } : { triggerKind: 1 },
      },
    },
    signal,
  )
  const items: CompletionItem[] = Array.isArray(res) ? res : res?.items ?? []
  return { items, incomplete: !Array.isArray(res) && !!res?.isIncomplete, resolve: !!caps.completionProvider.resolveProvider }
}

export async function triggerCharacters(doc: Doc): Promise<string[]> {
  const caps = await capabilities(doc.path)
  return caps?.completionProvider?.triggerCharacters ?? []
}

export async function resolveItem(doc: Doc, item: CompletionItem): Promise<CompletionItem> {
  const lang = api.editor.serverLanguage(doc.path)
  try {
    return { ...item, ...(await api.rpc('lsp.request', { lang, method: 'completionItem/resolve', params: item })) }
  } catch {
    return item
  }
}
