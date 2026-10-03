// Tool "Assistant IA": chat with a model of a llama.cpp or Ollama server, which can read and
// change the project files, ask the language servers and run commands. Layout: history side
// bar (always shown in a wide detached window, else folding), conversation, message box.
import { createEffect, createSignal, on, onCleanup, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast } from '../ui/toast'
import { route } from '../app/router'
import { approval, chat, live, loadConfig, modelsError, prefs, resetChat, restoreActive, savePrefs, setPrefs } from './state'
import { SettingsModal } from './AssistantSettings'
import { resumeIfNeeded, stopWatch } from './agent'
import { Thread } from './Thread'
import { addFiles, Composer, focusComposer, suggest } from './Composer'
import { Sidebar } from './Sidebar'
import { DiagramViewer } from './DiagramViewer'
import { board, ensureBoard, openTicket, roleLabels, statusLabels, summary, type ChatRole } from '../kanban/state'
import { pick } from '../ui/overlay'
import { setChat, saveChat } from './state'
import { t } from '../i18n'
import './assistant.css'

/** Ticket linked to the conversation (link, unlink, link another one). */
function TicketBar() {
  const tk = () => (chat.ticket ? summary(chat.ticket.id) : undefined)
  const roleFor = (status: string): ChatRole => (status === 'new' ? 'briefing' : status === 'fix' || status === 'review' ? 'correction' : 'dev')
  const link = async () => {
    ensureBoard()
    const id = await pick<number>({
      placeholder: t('Link this conversation to a ticket'),
      items: board.tickets
        .filter((x) => x.status !== 'done' && x.status !== 'abandoned')
        .map((x) => ({ label: `#${x.id} ${x.title}`, detail: statusLabels[x.status], value: x.id })),
    })
    const s = id ? summary(id) : undefined
    if (!s) return
    setChat('ticket', { id: s.id, role: roleFor(s.status) })
    saveChat()
  }
  return (
    <Show
      when={chat.ticket}
      fallback={
        <Show when={chat.messages.length && !live.busy}>
          <button class="ai-ticket-link" onClick={() => void link()} title={t('The tools that change the ticket become available')}>
            <Icon name="kanban" size={12} /> {t('Link to a ticket…')}
          </button>
        </Show>
      }
    >
      <div class="ai-ticket-bar" data-testid="ai-ticket-bar">
        <Icon name="kanban" size={13} />
        <button class="link ellipsis" onClick={() => openTicket(chat.ticket!.id)}>
          #{chat.ticket!.id} {tk()?.title ?? ''}
        </button>
        <span class={`kb-role r-${chat.ticket!.role}`}>{roleLabels[chat.ticket!.role]}</span>
        <Show when={tk()}>
          <span class={`kb-status st-${tk()!.status}`}>{statusLabels[tk()!.status]}</span>
        </Show>
      </div>
    </Show>
  )
}

/** Width from which the side bar sits next to the conversation instead of over it. */
const WIDE = 720

export function AssistantTool() {
  const detached = route().name === 'tool'
  const [width, setWidth] = createSignal(0)
  const [settings, setSettings] = createSignal(false)
  const [dragging, setDragging] = createSignal(false)
  const wide = () => width() >= WIDE
  // In a wide detached window the history is always there.
  const pinned = () => detached && wide()
  const sidebarShown = () => pinned() || prefs.sidebarOpen
  const setSidebar = (v: boolean) => {
    setPrefs('sidebarOpen', v)
    savePrefs()
  }
  let rootEl!: HTMLDivElement
  let list!: HTMLDivElement
  let stick = true

  onMount(() => {
    const ro = new ResizeObserver(() => setWidth(rootEl.clientWidth))
    ro.observe(rootEl)
    onCleanup(() => ro.disconnect())
    setWidth(rootEl.clientWidth)
    loadConfig().catch(errorToast)
    // The conversation of the project, and the answer it was waiting for before a reload.
    restoreActive()
      .then(() => loadConfig())
      .then(() => resumeIfNeeded())
      .catch(() => {})
    focusComposer()
    // Late renders (Markdown, diagrams) make the thread grow after a load: stay at the end.
    const thread = new ResizeObserver(() => stick && (list.scrollTop = list.scrollHeight))
    thread.observe(list.firstElementChild as Element)
    onCleanup(() => thread.disconnect())
  })

  // Follow the answer while the view is at the bottom.
  const onScroll = () => {
    stick = list.scrollHeight - list.scrollTop - list.clientHeight < 60
  }
  const toBottom = () => requestAnimationFrame(() => list && (list.scrollTop = list.scrollHeight))
  createEffect(
    on(
      () => [chat.messages.length, live.content, live.reasoning, live.tool, approval()],
      () => stick && toBottom(),
    ),
  )
  createEffect(
    on(
      () => chat.id,
      () => {
        stick = true
        toBottom()
      },
    ),
  )

  const newChat = () => {
    if (live.busy && !live.watching) return
    stopWatch()
    resetChat()
    focusComposer()
  }

  return (
    <div class="panel ai-panel" ref={rootEl} classList={{ wide: wide(), detached }}>
      <div class="panel-head ai-head">
        <Show when={!pinned()}>
          <button class="icon-btn" classList={{ on: prefs.sidebarOpen }} title={t('Conversations of the project')} onClick={() => setSidebar(!prefs.sidebarOpen)}>
            <Icon name="sidebar" size={15} />
          </button>
        </Show>
        <span class="ai-title ellipsis" title={chat.title}>
          {chat.title || t('New conversation')}
        </span>
        <span class="grow" />
        <button class="icon-btn" title={t('New conversation')} disabled={live.busy && !live.watching} onClick={newChat}>
          <Icon name="plus" size={15} />
        </button>
        <button class="icon-btn" title={t('Settings (servers, prompt, compaction, transcription)')} onClick={() => setSettings(true)}>
          <Icon name="gear" size={15} />
        </button>
      </div>
      <TicketBar />
      <Show when={modelsError()}>
        <div class="ai-banner">
          <Icon name="conflict" size={13} /> {modelsError()}
        </div>
      </Show>
      <div class="ai-body">
        <Show when={sidebarShown()}>
          <Show when={!wide()}>
            <div class="ai-scrim" onClick={() => setSidebar(false)} />
          </Show>
          <div class="ai-side-wrap" classList={{ overlay: !wide() }}>
            <Sidebar onPicked={() => !wide() && setSidebar(false)} onNew={() => (!wide() && setSidebar(false), focusComposer())} />
          </div>
        </Show>
        <div class="ai-main">
          <div
            class="ai-messages"
            classList={{ dragging: dragging() }}
            ref={list}
            onScroll={onScroll}
            onDragOver={(e) => {
              if (e.dataTransfer?.types.includes('Files')) {
                e.preventDefault()
                setDragging(true)
              }
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => {
              setDragging(false)
              if (e.dataTransfer?.files.length) {
                e.preventDefault()
                addFiles(e.dataTransfer.files)
              }
            }}
          >
            <Thread onSuggest={suggest} onSettings={() => setSettings(true)} />
          </div>
          <Composer
            onSettings={() => setSettings(true)}
            onSent={() => {
              stick = true
              toBottom()
            }}
          />
        </div>
      </div>
      <Show when={settings()}>
        <SettingsModal onClose={() => setSettings(false)} />
      </Show>
      <DiagramViewer />
    </div>
  )
}
