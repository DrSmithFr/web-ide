// Board of the kanban (tab of the editor): one column per status, Done and Abandoned
// folded. Tickets move with the buttons of their view, never by drag and drop.
import { createMemo, createResource, createSignal, For, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { Modal } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { copyText } from '../ui/clipboard'
import { mcpAddCommand } from './claude'
import {
  board, createTicket, ensureBoard, setMeta, newTicketOpen, openTicket, priorityLabels, setNewTicketOpen, statusLabels,
  MAX_DESCRIPTION, type Priority, type Status, type Summary,
} from './state'
import { t } from '../i18n'
import './kanban.css'

const active: Status[] = ['new', 'todo', 'in_progress', 'review']

export function Board() {
  onMount(ensureBoard)
  const [query, setQuery] = createSignal('')
  const [showClosed, setShowClosed] = createSignal(false)
  const [settingsOpen, setSettingsOpen] = createSignal(false)
  const filtered = createMemo(() => {
    const q = query().trim().toLowerCase()
    return board.tickets.filter(
      (tk) => !q || tk.title.toLowerCase().includes(q) || `#${tk.id}` === q || String(tk.id) === q,
    )
  })
  const column = (s: Status) => filtered().filter((tk) => tk.status === s)
  const closed = () => filtered().filter((tk) => tk.status === 'done' || tk.status === 'abandoned').sort((a, b) => (b.closed ?? 0) - (a.closed ?? 0))
  return (
    <div class="kb-board" data-testid="kanban-board">
      <div class="kb-toolbar">
        <button class="btn primary small" onClick={() => setNewTicketOpen(true)} data-testid="kanban-new">
          <Icon name="plus" size={13} /> {t('New ticket')}
        </button>
        <input class="input small kb-search" placeholder={t('Filter (title or #number)')} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
        <span class="grow" />
        <button class="btn small" onClick={() => setSettingsOpen(true)} data-testid="kanban-settings">
          <Icon name="gear" size={13} /> {t('Settings')}
        </button>
        <button class="btn small" classList={{ on: showClosed() }} onClick={() => setShowClosed(!showClosed())}>
          {t('Done and abandoned ({n})', { n: board.tickets.filter((tk) => tk.status === 'done' || tk.status === 'abandoned').length })}
        </button>
      </div>
      <Show when={board.error}>
        <p class="danger pad">{board.error}</p>
      </Show>
      <Show when={settingsOpen()}>
        <KanbanSettings onClose={() => setSettingsOpen(false)} />
      </Show>
      <div class="kb-columns">
        <For each={active}>
          {(s) => (
            <section class={`kb-col st-${s}`} data-status={s}>
              <header class="kb-col-head">
                <span class={`kb-dot st-${s}`} />
                {statusLabels[s]}
                <span class="kb-count">{column(s).length}</span>
              </header>
              <div class="kb-col-body">
                <For each={column(s)} fallback={<p class="kb-empty">—</p>}>
                  {(tk) => <Card tk={tk} />}
                </For>
              </div>
            </section>
          )}
        </For>
        <Show when={showClosed()}>
          <section class="kb-col st-done" data-status="closed">
            <header class="kb-col-head">
              <span class="kb-dot st-done" />
              {t('Done · abandoned')}
              <span class="kb-count">{closed().length}</span>
            </header>
            <div class="kb-col-body">
              <For each={closed()} fallback={<p class="kb-empty">—</p>}>
                {(tk) => <Card tk={tk} />}
              </For>
            </div>
          </section>
        </Show>
      </div>
    </div>
  )
}

export function Card(props: { tk: Summary; compact?: boolean }) {
  const tk = () => props.tk
  return (
    <button class="kb-card" classList={{ compact: props.compact }} onClick={() => openTicket(tk().id)} data-testid={`ticket-card-${tk().id}`}>
      <div class="kb-card-top">
        <span class={`kb-prio p-${tk().priority}`} title={t('Priority: {priority}', { priority: priorityLabels[tk().priority] })} />
        <span class="kb-num">#{tk().id}</span>
        <Show when={props.compact}>
          <span class={`kb-status st-${tk().status}`}>{statusLabels[tk().status]}</span>
        </Show>
      </div>
      <div class="kb-card-title">{tk().title}</div>
      <Show when={!props.compact && (tk().goals || tk().chats || tk().branch || tk().feedbackOpen)}>
        <div class="kb-card-meta">
          <Show when={tk().feedbackOpen}>
            <span class="warn" title={t('Open feedback')} data-testid="card-feedback">
              <Icon name="comment" size={11} /> {tk().feedbackOpen}
            </span>
          </Show>
          <Show when={tk().goals}>
            <span title={t('Goals reached')} classList={{ ok: tk().goalsDone === tk().goals }}>
              <Icon name="check" size={11} /> {tk().goalsDone}/{tk().goals}
            </span>
          </Show>
          <Show when={tk().chats}>
            <span title={t('Linked conversations')}>
              <Icon name="sparkle" size={11} /> {tk().chats}
            </span>
          </Show>
          <Show when={tk().branch}>
            <span class="ellipsis mono" title={tk().branch}>
              <Icon name="branch" size={11} /> {tk().branch}
            </span>
          </Show>
        </div>
      </Show>
    </button>
  )
}

/** Settings of the kanban of the project (kept in its base). */
function KanbanSettings(props: { onClose: () => void }) {
  const [base, setBase] = createSignal(board.meta.base ?? '')
  const [setup, setSetup] = createSignal(board.meta.setup ?? '')
  const [mcp] = createResource(mcpAddCommand)
  const save = async () => {
    try {
      await setMeta({ base: base().trim(), setup: setup().trim() })
      props.onClose()
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <Modal
      title={t('Kanban settings')}
      onClose={props.onClose}
      footer={
        <>
          <button class="btn" onClick={props.onClose}>
            {t('Cancel')}
          </button>
          <button class="btn primary" onClick={() => void save()} data-testid="kanban-settings-save">
            {t('Save')}
          </button>
        </>
      }
    >
      <div class="form">
        <label class="field">
          <span>{t('Base of the ticket branches (empty: origin/main, else main)')}</span>
          <input class="mono" placeholder="origin/main" value={base()} onInput={(e) => setBase(e.currentTarget.value)} data-testid="kanban-base" />
        </label>
        <label class="field">
          <span>{t('Setup command of a worktree (run in the worktree when it is created)')}</span>
          <textarea class="mono" rows={3} placeholder="npm install && cp ../../../.env ." value={setup()} onInput={(e) => setSetup(e.currentTarget.value)} data-testid="kanban-setup" />
        </label>
        <p class="muted small">{t('Worktrees are created in {dir}, ignored by git.', { dir: '.ide/worktrees/' })}</p>
        <div class="field">
          <span>{t('Claude Code: add the kanban tools of this IDE (once, in a terminal)')}</span>
          <div class="field-row">
            <input class="mono grow" readonly value={mcp() ?? ''} data-testid="kanban-mcp-command" />
            <button class="btn small" onClick={() => void copyText(mcp() ?? '')}>
              {t('Copy')}
            </button>
          </div>
        </div>
      </div>
    </Modal>
  )
}

/** The new ticket form, shown once per window (board, panel and assistant open it). */
export function NewTicketHost() {
  return (
    <Show when={newTicketOpen()}>
      <NewTicket onClose={() => setNewTicketOpen(false)} />
    </Show>
  )
}

export function NewTicket(props: { onClose: () => void }) {
  const [title, setTitle] = createSignal('')
  const [priority, setPriority] = createSignal<Priority>('normal')
  const [description, setDescription] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  const submit = async (e?: Event) => {
    e?.preventDefault()
    if (!title().trim() || busy()) return
    setBusy(true)
    try {
      const tk = await createTicket({ title: title(), priority: priority(), description: description() })
      props.onClose()
      openTicket(tk.id)
    } catch (err) {
      errorToast(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title={t('New ticket')}
      onClose={props.onClose}
      footer={
        <>
          <button class="btn" onClick={props.onClose}>
            {t('Cancel')}
          </button>
          <button class="btn primary" disabled={!title().trim() || busy() || [...description().trim()].length > MAX_DESCRIPTION} onClick={submit} data-testid="kanban-create">
            {t('Create')}
          </button>
        </>
      }
    >
      <form class="form" onSubmit={submit}>
        <label class="field">
          <span>{t('Title')}</span>
          <input autofocus value={title()} onInput={(e) => setTitle(e.currentTarget.value)} data-testid="kanban-title" />
        </label>
        <label class="field">
          <span>{t('Priority')}</span>
          <select value={priority()} onChange={(e) => setPriority(e.currentTarget.value as Priority)}>
            <For each={Object.entries(priorityLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
          </select>
        </label>
        <label class="field">
          <span>
            {t('Description (Markdown)')} <span class="tk-count" classList={{ over: [...description().trim()].length > MAX_DESCRIPTION }}>{[...description().trim()].length}/{MAX_DESCRIPTION}</span>
          </span>
          <textarea rows={8} value={description()} onInput={(e) => setDescription(e.currentTarget.value)} data-testid="kanban-description" />
        </label>
      </form>
    </Modal>
  )
}
