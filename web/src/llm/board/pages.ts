// The pages of the board of a conversation: a view over its messages, not a store. A page is
// a doodle or an image the user sent (an attachment), or a page drawn by the model (a tool
// message with its page). Numbered from 1 in message order, then attachment order: the
// model refers to pages by this number, so the pod counts them the same way (agent.Pages).
import { createEffect, createMemo, createRoot, createSignal, on } from 'solid-js'
import { chat, prefs, savePrefs, setPrefs, type ChatMessage } from '../state'
import type { DoodleDoc } from '../doodle/model'
import { toast } from '../../ui/toast'
import { t } from '../../i18n'

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

/**
 * The images of the image attachments of a message, in order: its image parts that are not
 * the PNG of a doodle. None when video frames or PDF pages mix in (as the pod does).
 */
function imageSources(m: ChatMessage): string[] {
  const atts = m.attachments ?? []
  if (atts.some((a) => a.kind === 'video' || a.kind === 'pdf') || !Array.isArray(m.content)) return []
  const doodles = new Set(atts.filter((a) => a.kind === 'doodle').map((a) => a.png))
  return m.content.flatMap((p) => (p.type === 'image_url' && !doodles.has(p.image_url.url) ? [p.image_url.url] : []))
}

// The document of an image page is made once per image: the view keeps its zoom.
const imageDocs = new Map<string, DoodleDoc>()
function imageDoc(src: string | undefined, w: number, h: number): DoodleDoc {
  const key = `${w}x${h}:${src ?? ''}`
  let doc = imageDocs.get(key)
  if (!doc) {
    doc = { v: 1, frame: { x: 0, y: 0, w, h }, preset: 'image', elements: [], background: src ? { src, x: 0, y: 0, w, h } : undefined }
    imageDocs.set(key, doc)
  }
  return doc
}

export function pagesOf(messages: ChatMessage[]): Page[] {
  const out: Page[] = []
  messages.forEach((m, i) => {
    const images = imageSources(m)
    let k = 0
    ;(m.attachments ?? []).forEach((a, n) => {
      if (a.kind === 'doodle' && a.doodle)
        out.push({ key: `${i}:${n}`, number: out.length + 1, name: a.name, doc: a.doodle, description: a.description, from: 'user', msgIndex: i, thumb: a.thumb })
      // An image sent (with its size: older ones are not pages).
      if (a.kind === 'image' && a.w && a.h) out.push({ key: `${i}:${n}`, number: out.length + 1, name: a.name, doc: imageDoc(images[k], a.w, a.h), from: 'user', msgIndex: i, thumb: a.thumb })
      if (a.kind === 'image') k++
    })
    if (m.page) out.push({ key: `${i}:page`, number: out.length + 1, name: m.page.name, doc: m.page.doc, description: m.page.description, from: 'model', msgIndex: i, thumb: m.page.thumb })
  })
  return out
}

// Where the board is: a column next to the conversation in a wide assistant (boardOpen), or
// in its place in a narrow one (boardView). AssistantTool tells which.
const [wideMode, setWideMode] = createSignal(true)

const [selectedKey, setSelectedKey] = createSignal<string | null>(null)

export const pages = createRoot(() => {
  const list = createMemo(() => pagesOf(chat.messages))
  // Another conversation shows its own pages; a new page shows itself.
  createEffect(on(() => chat.id, () => setSelectedKey(null)))
  createEffect(
    on(
      () => [chat.id, list().length] as const,
      ([id, n], prev) => {
        if (!prev || n <= prev[1]) return
        const last = list()[n - 1]
        setSelectedKey(last.key)
        // A page the model just drew in this conversation: the board opens beside it, or
        // says so when it would hide the conversation.
        if (id !== prev[0] || last.from !== 'model') return
        if (wideMode()) {
          setPrefs('boardOpen', true)
          savePrefs()
        } else if (!boardShown()) toast(t('New page on the board'), 'info', { label: t('Show'), run: () => showBoard(true) })
      },
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
