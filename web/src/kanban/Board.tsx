// Board of the kanban (tab of the editor): one column per status, Done and Abandoned
// folded. Tickets move with the buttons of their view, never by drag and drop.
import { createMemo, createSignal, For, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { Modal } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import {
  board, createTicket, ensureBoard, newTicketOpen, openTicket, priorityLabels, setNewTicketOpen, statusLabels, typeLabels,
  type Priority, type Status, type Summary, type TicketType,
} from './state'
import './kanban.css'

const active: Status[] = ['new', 'ready', 'in_progress', 'review', 'fix']

export function Board() {
  onMount(ensureBoard)
  const [query, setQuery] = createSignal('')
  const [type, setType] = createSignal<'' | TicketType>('')
  const [showClosed, setShowClosed] = createSignal(false)
  const filtered = createMemo(() => {
    const q = query().trim().toLowerCase()
    return board.tickets.filter(
      (t) => (!type() || t.type === type()) && (!q || t.title.toLowerCase().includes(q) || `#${t.id}` === q || String(t.id) === q),
    )
  })
  const column = (s: Status) => filtered().filter((t) => t.status === s)
  const closed = () => filtered().filter((t) => t.status === 'done' || t.status === 'abandoned').sort((a, b) => (b.closed ?? 0) - (a.closed ?? 0))
  return (
    <div class="kb-board" data-testid="kanban-board">
      <div class="kb-toolbar">
        <button class="btn primary small" onClick={() => setNewTicketOpen(true)} data-testid="kanban-new">
          <Icon name="plus" size={13} /> Nouveau ticket
        </button>
        <input class="input small kb-search" placeholder="Filtrer (titre ou #n°)" value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
        <select class="small" value={type()} onChange={(e) => setType(e.currentTarget.value as any)}>
          <option value="">Tous les types</option>
          <For each={Object.entries(typeLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
        </select>
        <span class="grow" />
        <button class="btn small" classList={{ on: showClosed() }} onClick={() => setShowClosed(!showClosed())}>
          Terminés et abandonnés ({board.tickets.filter((t) => t.status === 'done' || t.status === 'abandoned').length})
        </button>
      </div>
      <Show when={board.error}>
        <p class="danger pad">{board.error}</p>
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
                  {(t) => <Card t={t} />}
                </For>
              </div>
            </section>
          )}
        </For>
        <Show when={showClosed()}>
          <section class="kb-col st-done" data-status="closed">
            <header class="kb-col-head">
              <span class="kb-dot st-done" />
              Terminés · abandonnés
              <span class="kb-count">{closed().length}</span>
            </header>
            <div class="kb-col-body">
              <For each={closed()} fallback={<p class="kb-empty">—</p>}>
                {(t) => <Card t={t} />}
              </For>
            </div>
          </section>
        </Show>
      </div>
    </div>
  )
}

export function Card(props: { t: Summary; compact?: boolean }) {
  const t = () => props.t
  return (
    <button class="kb-card" classList={{ compact: props.compact }} onClick={() => openTicket(t().id)} data-testid={`ticket-card-${t().id}`}>
      <div class="kb-card-top">
        <span class={`kb-prio p-${t().priority}`} title={`Priorité ${priorityLabels[t().priority]}`} />
        <span class="kb-num">#{t().id}</span>
        <span class={`kb-type t-${t().type}`}>{typeLabels[t().type]}</span>
        <Show when={props.compact}>
          <span class={`kb-status st-${t().status}`}>{statusLabels[t().status]}</span>
        </Show>
      </div>
      <div class="kb-card-title">{t().title}</div>
      <Show when={!props.compact && (t().goals || t().chats || t().branch)}>
        <div class="kb-card-meta">
          <Show when={t().goals}>
            <span title="Goals atteints" classList={{ ok: t().goalsDone === t().goals }}>
              <Icon name="check" size={11} /> {t().goalsDone}/{t().goals}
            </span>
          </Show>
          <Show when={t().chats}>
            <span title="Conversations liées">
              <Icon name="sparkle" size={11} /> {t().chats}
            </span>
          </Show>
          <Show when={t().branch}>
            <span class="ellipsis mono" title={t().branch}>
              <Icon name="branch" size={11} /> {t().branch}
            </span>
          </Show>
        </div>
      </Show>
    </button>
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
  const [type, setType] = createSignal<TicketType>('feature')
  const [priority, setPriority] = createSignal<Priority>('normal')
  const [description, setDescription] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  const submit = async (e?: Event) => {
    e?.preventDefault()
    if (!title().trim() || busy()) return
    setBusy(true)
    try {
      const t = await createTicket({ title: title(), type: type(), priority: priority(), description: description() })
      props.onClose()
      openTicket(t.id)
    } catch (err) {
      errorToast(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title="Nouveau ticket"
      onClose={props.onClose}
      footer={
        <>
          <button class="btn" onClick={props.onClose}>
            Annuler
          </button>
          <button class="btn primary" disabled={!title().trim() || busy()} onClick={submit} data-testid="kanban-create">
            Créer
          </button>
        </>
      }
    >
      <form class="form" onSubmit={submit}>
        <label class="field">
          <span>Titre</span>
          <input autofocus value={title()} onInput={(e) => setTitle(e.currentTarget.value)} data-testid="kanban-title" />
        </label>
        <div class="field-row">
          <label class="field grow">
            <span>Type</span>
            <select value={type()} onChange={(e) => setType(e.currentTarget.value as TicketType)}>
              <For each={Object.entries(typeLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
            </select>
          </label>
          <label class="field grow">
            <span>Priorité</span>
            <select value={priority()} onChange={(e) => setPriority(e.currentTarget.value as Priority)}>
              <For each={Object.entries(priorityLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
            </select>
          </label>
        </div>
        <label class="field">
          <span>Description (Markdown)</span>
          <textarea rows={8} value={description()} onInput={(e) => setDescription(e.currentTarget.value)} data-testid="kanban-description" />
        </label>
      </form>
    </Modal>
  )
}
