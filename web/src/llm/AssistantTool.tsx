// Tool "Assistant IA": chat with a model of a llama.cpp or Ollama server, which can read and
// change the project files, ask the language servers and run commands. Layout: history side
// bar (always shown in a wide detached window, else folding), conversation, message box.
import { createEffect, createSignal, on, onCleanup, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast } from '../ui/toast'
import { route } from '../app/router'
import { approval, chat, live, loadConfig, modelsError, prefs, resetChat, savePrefs, setPrefs } from './state'
import { SettingsModal } from './AssistantSettings'
import { restoreActive } from './agent'
import { Thread } from './Thread'
import { addFiles, Composer, focusComposer, suggest } from './Composer'
import { Sidebar } from './Sidebar'
import { DiagramViewer } from './DiagramViewer'
import { DoodleHost } from './doodle/DoodleModal'
import { Board } from './board/Board'
import { boardShown, pages, setBoardWide, showBoard, showSide, toggleSide } from './board/pages'
import { Stats } from './Stats'
import { board, ensureBoard, openTicket, roleLabels, statusLabels, summary, type ChatRole } from '../kanban/state'
import { pick } from '../ui/overlay'
import { request } from '../pod/rpc'
import { setChat } from './state'
import { t } from '../i18n'
import './assistant.css'

/** Ticket linked to the conversation (link, unlink, link another one). */
function TicketBar() {
  const tk = () => (chat.ticket ? summary(chat.ticket.id) : undefined)
  const roleFor = (status: string): ChatRole => (status === 'new' ? 'briefing' : status === 'todo' ? 'plan' : status === 'review' ? 'correction' : 'dev')
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
    const ticket = { id: s.id, role: roleFor(s.status) }
    setChat('ticket', ticket)
    await request('agent.set', { id: chat.id, ticket }).catch(errorToast)
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
/** Width from which the board is a column next to the conversation instead of in its place. */
const BOARD_WIDE = 1000

export function AssistantTool() {
  const detached = route().name === 'tool'
  const [width, setWidth] = createSignal(0)
  const [settings, setSettings] = createSignal(false)
  const [dragging, setDragging] = createSignal(false)
  const wide = () => width() >= WIDE
  const boardWide = () => width() >= BOARD_WIDE
  createEffect(() => setBoardWide(boardWide()))
  const [splitting, setSplitting] = createSignal(false)
  let mainEl!: HTMLDivElement
  let bodyEl!: HTMLDivElement
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
  let lastTop = 0

  onMount(() => {
    const ro = new ResizeObserver(() => setWidth(rootEl.clientWidth))
    ro.observe(rootEl)
    onCleanup(() => ro.disconnect())
    setWidth(rootEl.clientWidth)
    loadConfig().catch(errorToast)
    // The conversation of the project, and the answer it was waiting for before a reload.
    restoreActive()
      .then(() => loadConfig())
      .catch(() => {})
    focusComposer()
    // Late renders (Markdown, diagrams) make the thread grow after a load: stay at the end.
    const thread = new ResizeObserver(() => stick && (list.scrollTop = list.scrollHeight))
    thread.observe(list.firstElementChild as Element)
    onCleanup(() => thread.disconnect())
  })

  // Follow the answer while the view is at the bottom. Only a move up stops following: the
  // event of our own scroll to the end may come after the thread grew again (a large step).
  const onScroll = () => {
    if (list.scrollHeight - list.scrollTop - list.clientHeight < 60) stick = true
    else if (list.scrollTop < lastTop) stick = false
    lastTop = list.scrollTop
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
        <button
          class="icon-btn"
          classList={{ on: boardShown() && prefs.sideView === 'stats' }}
          title={boardShown() && prefs.sideView === 'stats' && !boardWide() ? t('Back to the conversation') : t('Statistics')}
          onClick={() => toggleSide('stats')}
          data-testid="ai-stats-toggle"
        >
          <Icon name="chart" size={15} />
        </button>
        <button
          class="icon-btn ai-board-btn"
          classList={{ on: boardShown() && prefs.sideView === 'board' }}
          title={boardShown() && prefs.sideView === 'board' && !boardWide() ? t('Back to the conversation') : t('Board of the conversation')}
          onClick={() => toggleSide('board')}
          data-testid="ai-board-toggle"
        >
          <Icon name="layout" size={15} />
          <Show when={pages().length}>
            <span class="ai-board-count">{pages().length}</span>
          </Show>
        </button>
        <button class="icon-btn" title={t('New conversation')} onClick={newChat}>
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
      <div class="ai-body" ref={bodyEl}>
        <Show when={sidebarShown()}>
          <Show when={!wide()}>
            <div class="ai-scrim" onClick={() => setSidebar(false)} />
          </Show>
          <div class="ai-side-wrap" classList={{ overlay: !wide() }}>
            <Sidebar onPicked={() => !wide() && setSidebar(false)} onNew={() => (!wide() && setSidebar(false), focusComposer())} />
          </div>
        </Show>
        <div
          class="ai-main"
          ref={mainEl}
          style={{ display: boardShown() && !boardWide() ? 'none' : undefined, flex: boardShown() && boardWide() ? `${prefs.boardSplit} 1 0` : undefined }}
        >
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
        <Show when={boardShown()}>
          <Show when={boardWide()}>
            <div
              class="ai-split"
              classList={{ on: splitting() }}
              data-testid="ai-board-split"
              onPointerDown={(e) => {
                setSplitting(true)
                e.currentTarget.setPointerCapture(e.pointerId)
              }}
              onPointerMove={(e) => {
                if (!splitting()) return
                const left = mainEl.getBoundingClientRect().left
                const right = bodyEl.getBoundingClientRect().right
                setPrefs('boardSplit', Math.min(0.75, Math.max(0.25, (e.clientX - left) / (right - left))))
              }}
              onPointerUp={() => {
                setSplitting(false)
                savePrefs()
              }}
            />
          </Show>
          <div class="ai-board-col" style={{ flex: boardWide() ? `${1 - prefs.boardSplit} 1 0` : '1 1 0' }}>
            <div class="ai-side-tabs" role="tablist">
              <button role="tab" aria-selected={prefs.sideView === 'board'} classList={{ on: prefs.sideView === 'board' }} onClick={() => showSide('board')} data-testid="ai-side-board">
                <Icon name="layout" size={13} /> {t('Board')}
              </button>
              <button role="tab" aria-selected={prefs.sideView === 'stats'} classList={{ on: prefs.sideView === 'stats' }} onClick={() => showSide('stats')} data-testid="ai-side-stats">
                <Icon name="chart" size={13} /> {t('Statistics')}
              </button>
              <span class="grow" />
              <button class="icon-btn" title={boardWide() ? t('Close') : t('Back to the conversation')} onClick={() => showBoard(false)}>
                <Icon name="close" size={14} />
              </button>
            </div>
            <Show when={prefs.sideView === 'stats'} fallback={<Board />}>
              <Stats />
            </Show>
          </div>
        </Show>
      </div>
      <Show when={settings()}>
        <SettingsModal onClose={() => setSettings(false)} />
      </Show>
      <DiagramViewer />
      <DoodleHost onSettings={() => setSettings(true)} />
    </div>
  )
}
