// The pages of the board of a conversation: a view over its messages, not a store. A page is
// a doodle the user sent (an attachment with its document); the pages drawn by the model
// come later. Numbered from 1 in message order, then attachment order: the model refers to
// pages by this number, so the pod counts them the same way.
import { createEffect, createMemo, createRoot, createSignal, on } from 'solid-js'
import { chat, prefs, savePrefs, setPrefs, type ChatMessage } from '../state'
import type { DoodleDoc } from '../doodle/model'

export interface Page {
  key: string
  number: number
  name: string
  doc: DoodleDoc
  description?: string
  from: 'user' | 'model'
  msgIndex: number
  thumb?: string
}

export function pagesOf(messages: ChatMessage[]): Page[] {
  const out: Page[] = []
  messages.forEach((m, i) =>
    (m.attachments ?? []).forEach((a, n) => {
      if (a.kind === 'doodle' && a.doodle)
        out.push({ key: `${i}:${n}`, number: out.length + 1, name: a.name, doc: a.doodle, description: a.description, from: 'user', msgIndex: i, thumb: a.thumb })
    }),
  )
  return out
}

const [selectedKey, setSelectedKey] = createSignal<string | null>(null)

export const pages = createRoot(() => {
  const list = createMemo(() => pagesOf(chat.messages))
  // Another conversation shows its own pages; a new page shows itself.
  createEffect(on(() => chat.id, () => setSelectedKey(null)))
  createEffect(
    on(
      () => list().length,
      (n, prev) => n > (prev ?? n) && setSelectedKey(list()[n - 1].key),
    ),
  )
  return list
})

/** The page shown: the one picked, else the last one. */
export const selectedPage = () => {
  const list = pages()
  return list.find((p) => p.key === selectedKey()) ?? list[list.length - 1]
}
export const selectPage = (key: string) => setSelectedKey(key)

// Where the board is: a column next to the conversation in a wide assistant (boardOpen), or
// in its place in a narrow one (boardView). AssistantTool tells which.
const [wideMode, setWideMode] = createSignal(true)
export const setBoardWide = setWideMode
export const boardShown = () => (wideMode() ? prefs.boardOpen : prefs.boardView === 'board')

export function showBoard(v: boolean) {
  if (wideMode()) setPrefs('boardOpen', v)
  else setPrefs('boardView', v ? 'board' : 'chat')
  savePrefs()
}

/** Shows a page on the board (a doodle card of the thread). */
export function showPage(key: string) {
  selectPage(key)
  showBoard(true)
}
