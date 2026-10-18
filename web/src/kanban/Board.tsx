// Board of the kanban (tab of the editor): one column per status, Done and Abandoned
// folded. Tickets move with the buttons of their view, never by drag and drop.
import { HelpLink } from '../help/HelpLink'
import { createMemo, createResource, createSignal, For, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { Modal } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { copyText } from '../ui/clipboard'
import { mcpAddCommand } from './claude'
import { catalog } from '../agents/state'
import { openCommands } from '../console/CommandsModal'
import { commands, loadCommands } from '../console/commands'
import {
  board, createTicket, ensureBoard, setMeta, newTicketOpen, openTicket, priorityLabels, setNewTicketOpen, statusLabels, workTime,
  MAX_DESCRIPTION, type Priority, type Status, type Summary,
} from './state'
import { blockerText } from './Lineage'
import { Roadmap } from './Roadmap'
import { Milestones } from './Milestones'
import { milestoneMatch, type MilestoneFilter } from './milestones'
import { t } from '../i18n'
import './kanban.css'

const active: Status[] = ['new', 'todo', 'in_progress', 'to_test']

const VIEW_KEY = 'webide.kanban.view'
type View = 'board' | 'roadmap' | 'milestones'
function savedView(): View {
  try {
    const v = localStorage.getItem(VIEW_KEY)
    return v === 'roadmap' || v === 'milestones' ? v : 'board'
  } catch {
    return 'board'
  }
}


export function Board() {
  onMount(ensureBoard)
  const [query, setQuery] = createSignal('')
  const [showClosed, setShowClosed] = createSignal(false)
  const [settingsOpen, setSettingsOpen] = createSignal(false)
  const [view, setView] = createSignal<View>(savedView())
  const [ms, setMs] = createSignal<MilestoneFilter>('all')
  const choose = (v: View) => {
    setView(v)
    try {
      localStorage.setItem(VIEW_KEY, v)
    } catch {
      /* private mode: the choice lasts for this window */
    }
  }
  const filtered = createMemo(() => {
    const q = query().trim().toLowerCase()
    return board.tickets.filter(
      (tk) => (!q || tk.title.toLowerCase().includes(q) || `#${tk.id}` === q || String(tk.id) === q) && milestoneMatch(ms(), tk.milestone),
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
        <div class="kb-views" role="group" aria-label={t('View')}>
          <button class="btn small" classList={{ on: view() === 'board' }} aria-pressed={view() === 'board'} onClick={() => choose('board')} data-testid="kanban-view-board">
            <Icon name="kanban" size={13} /> {t('Board')}
          </button>
          <button class="btn small" classList={{ on: view() === 'roadmap' }} aria-pressed={view() === 'roadmap'} onClick={() => choose('roadmap')} data-testid="kanban-view-roadmap">
            <Icon name="history" size={13} /> {t('Roadmap')}
          </button>
          <button class="btn small" classList={{ on: view() === 'milestones' }} aria-pressed={view() === 'milestones'} onClick={() => choose('milestones')} data-testid="kanban-view-milestones">
            <Icon name="flag" size={13} /> {t('Milestones')}
          </button>
        </div>
        <Show when={view() !== 'milestones' && board.milestones.length}>
          <select
            class="small"
            title={t('Milestone')}
            onChange={(e) => {
              const v = e.currentTarget.value
              setMs(v === 'all' || v === 'none' ? v : Number(v))
            }}
            data-testid="kanban-milestone-filter"
          >
            <option value="all" selected={ms() === 'all'}>
              {t('All milestones')}
            </option>
            <option value="none" selected={ms() === 'none'}>
              {t('No milestone')}
            </option>
            <For each={board.milestones.filter((m) => m.state === 'open' || m.id === ms())}>
              {(m) => (
                <option value={m.id} selected={ms() === m.id}>
                  {m.title}
                </option>
              )}
            </For>
          </select>
        </Show>
        <span class="grow" />
        <button class="btn small" onClick={() => setSettingsOpen(true)} data-testid="kanban-settings">
          <Icon name="gear" size={13} /> {t('Settings')}
        </button>
        <HelpLink topic="kanban" />
        <Show when={view() === 'board'}>
          <button class="btn small" classList={{ on: showClosed() }} onClick={() => setShowClosed(!showClosed())}>
            {t('Done and abandoned ({n})', { n: board.tickets.filter((tk) => tk.status === 'done' || tk.status === 'abandoned').length })}
          </button>
        </Show>
      </div>
      <Show when={board.error}>
        <p class="danger pad">{board.error}</p>
      </Show>
      <Show when={settingsOpen()}>
        <KanbanSettings onClose={() => setSettingsOpen(false)} />
      </Show>
      <Show when={view() === 'roadmap'}>
        <Roadmap tickets={board.tickets} query={query()} milestone={ms()} />
      </Show>
      <Show when={view() === 'milestones'}>
        <Milestones />
      </Show>
      <div class="kb-columns" hidden={view() !== 'board'}>
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
                {(tk) => <Card tk={tk} closed />}
              </For>
            </div>
          </section>
        </Show>
      </div>
    </div>
  )
}

/** closed: in the column of the closed tickets, which mixes done and abandoned: their status is tagged. */
export function Card(props: { tk: Summary; compact?: boolean; closed?: boolean }) {
  const tk = () => props.tk
  return (
    <button class="kb-card" classList={{ compact: props.compact }} onClick={() => openTicket(tk().id)} data-testid={`ticket-card-${tk().id}`}>
      <div class="kb-card-top">
        <span class={`kb-prio p-${tk().priority}`} title={t('Priority: {priority}', { priority: priorityLabels[tk().priority] })} />
        <span class="kb-num">#{tk().id}</span>
        <Show when={props.closed}>
          <span class={`kb-status kb-tag st-${tk().status}`} data-testid="card-status">
            {statusLabels[tk().status]}
          </span>
        </Show>
        <Show when={tk().parent}>
          <span class="kb-badge" title={t('Step of #{id}', { id: tk().parent! })} data-testid="card-parent">
            ↳ #{tk().parent}
          </span>
        </Show>
        <Show when={board.milestones.find((m) => m.id === tk().milestone)}>
          {(m) => (
            <span class="kb-badge kb-milestone ellipsis" title={t('Milestone: {title}', { title: m().title })} data-testid="card-milestone">
              <Icon name="flag" size={10} /> {m().title}
            </span>
          )}
        </Show>
        <Show when={tk().blockers?.length}>
          <span class="kb-badge blocked" title={t('Cannot start yet: {blockers}', { blockers: tk().blockers!.map(blockerText).join(', ') })} data-testid="card-blocked">
            <Icon name="lock" size={10} /> {tk().blockers!.map((b) => `#${b.id}`).join(' ')}
          </span>
        </Show>
        <Show when={!props.compact && tk().generationMs}>
          <span class="kb-time" title={t('Time of the answers of its conversations (sub-agents included)')} data-testid="card-time">
            <Icon name="clock" size={11} /> {workTime(tk().generationMs!)}
          </span>
        </Show>
        <Show when={props.compact}>
          <span class={`kb-status st-${tk().status}`}>{statusLabels[tk().status]}</span>
        </Show>
      </div>
      <div class="kb-card-title">{tk().title}</div>
      <Show when={!props.compact && tk().goalTitles?.length}>
        <ul class="kb-goals" data-testid="card-goals">
          <For each={tk().goalTitles}>
            {(g) => (
              <li classList={{ done: g.done }}>
                <Icon name={g.done ? 'check' : 'circle'} size={10} />
                <span>{g.text}</span>
              </li>
            )}
          </For>
        </ul>
      </Show>
      <Show when={!props.compact && (tk().chats || tk().branch || tk().feedbackOpen)}>
        <div class="kb-card-meta">
          <Show when={tk().feedbackOpen}>
            <span class="warn" title={t('Open feedback')} data-testid="card-feedback">
              <Icon name="comment" size={11} /> {tk().feedbackOpen}
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
  const [agent, setAgent] = createSignal(board.meta.devAgent || 'assistant')
  // The agents of the Agents tool that are installed (the one chosen stays listed).
  const [agents] = createResource(() => catalog().then((c) => (c.local ? c.agents.filter((a) => a.installed || a.id === agent()) : []), () => []))
  const [mcp] = createResource(mcpAddCommand)
  // The setup of a new worktree: the project commands flagged setup.
  onMount(() => void loadCommands())
  const setupNames = () =>
    commands()
      .filter((c) => c.flag === 'setup')
      .map((c) => c.name)
      .join(', ')
  const save = async () => {
    try {
      await setMeta({ base: base().trim(), devAgent: agent() })
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
          <span>{t('Development agent')}</span>
          <select value={agent()} onChange={(e) => setAgent(e.currentTarget.value)} data-testid="kanban-dev-agent">
            <option value="assistant">{t('Assistant')}</option>
            <For each={agents() ?? []}>{(a) => <option value={a.id}>{a.name}</option>}</For>
          </select>
          <span class="muted small">{t('Start development, corrections and conflicts of a worktree go to a session of this agent in the Agents tool, with the kanban tools of the IDE.')}</span>
        </label>
        <div class="field">
          <span>{t('Setup of a worktree')}</span>
          <div class="field-row">
            <span class="muted small grow" data-testid="kanban-setup-list">
              {setupNames() ? t('Run in a new worktree, in this order: {names}.', { names: setupNames() }) : t('No command flagged “setup” yet (npm install, copy a .env…).')}
            </span>
            <button
              class="btn small"
              onClick={() => {
                props.onClose()
                openCommands()
              }}
              data-testid="kanban-setup-commands"
            >
              {t('Edit commands…')}
            </button>
          </div>
        </div>
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
