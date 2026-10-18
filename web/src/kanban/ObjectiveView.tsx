// An objective (tab of the editor): its date, description, progress, its tickets grouped by
// milestone then the lone ones, notes and history; only the user reaches it
// (docs/design/kanban.md). Also the objective of a ticket and of a milestone, chosen in their
// views.
import { createResource, createSignal, For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { contextMenu, prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { Markdown } from '../llm/parts'
import { closeTab, leaves, type TabState } from '../state/project'
import { board, ensureBoard, eventText, openMilestone, openObjective, openTicket, statusLabels, updateTicket, type Summary, type Ticket } from './state'
import { updateMilestone, type Milestone } from './milestones'
import {
  addObjectiveNote, createObjective, deleteObjective, deleteObjectiveNote, getObjective, objectiveStateNames, objectiveVersion, reachObjective, setObjectiveState, updateObjective, type Objective,
} from './objectives'
import { blockerText } from './Lineage'
import { Count, EditableMarkdown, EditableText, Section, tooLong } from './TicketView'
import { MAX_DESCRIPTION, MAX_NOTE } from './state'
import { fmtDate, t } from '../i18n'
import './kanban.css'

export type OApply = (p: Promise<Objective>) => Promise<void>

export function ObjectiveView(props: { tab: TabState }) {
  const id = props.tab.objective!
  ensureBoard()
  const [o, { mutate }] = createResource(
    () => objectiveVersion(id) + 1,
    async () => {
      try {
        return await getObjective(id)
      } catch (e) {
        return { error: (e as Error).message } as any
      }
    },
  )
  const apply: OApply = async (p) => {
    try {
      mutate(await p)
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <Show when={o()} fallback={<p class="muted pad">{t('Loading the objective…')}</p>}>
      <Show when={!o().error} fallback={<p class="danger pad">{o().error}</p>}>
        <ObjectiveBody o={o() as Objective} apply={apply} tabId={props.tab.id} />
      </Show>
    </Show>
  )
}

/** Why the objective cannot be reached yet ('' once it can). */
export function reachBlocker(o: { state: string; tickets: number; ticketsDone: number }): string {
  if (o.state !== 'open') return t('The objective is closed.')
  if (o.tickets > o.ticketsDone) return t('{n} ticket(s) not finished', { n: o.tickets - o.ticketsDone })
  return ''
}

/** The date of an objective, asked as YYYY-MM-DD ('' removes it); null when cancelled. */
export async function askDue(value = ''): Promise<string | null> {
  const v = await prompt({ title: t('Date of the objective'), label: t('YYYY-MM-DD, empty for none'), value, placeholder: '2026-12-31' })
  return v === null ? null : v.trim()
}

function ObjectiveBody(props: { o: Objective; apply: OApply; tabId: string }) {
  const o = () => props.o
  const events = () => o().notes.filter((n) => n.kind === 'event')
  const blocker = () => reachBlocker(o())
  const pct = () => (o().tickets ? Math.round((o().ticketsDone / o().tickets) * 100) : 0)
  const remove = async () => {
    if (!confirm(t('Delete the objective “{title}”? Its tickets and milestones stay.', { title: o().title }))) return
    try {
      await deleteObjective(o().id)
      for (const l of leaves()) if (l.tabs.includes(props.tabId)) closeTab(l.id, props.tabId, true)
    } catch (e) {
      errorToast(e)
    }
  }
  const editDue = async () => {
    const v = await askDue(o().due)
    if (v !== null) void props.apply(updateObjective(o().id, { due: v }))
  }
  return (
    <div class="tk ms ob" data-testid="objective-view" data-state={o().state}>
      <header class="tk-head">
        <div class="tk-title-row">
          <Icon name="target" size={16} />
          <EditableText value={o().title} class="tk-title" onSave={(v) => props.apply(updateObjective(o().id, { title: v }))} testid="objective-title" />
          <span class={`kb-status ms-${o().state === 'reached' ? 'validated' : o().state}`} data-testid="objective-state">
            {t(objectiveStateNames[o().state])}
          </span>
        </div>
        <div class="tk-meta-row">
          <button class="badge link" title={t('Change the date')} onClick={() => void editDue()} data-testid="objective-due">
            <Icon name="clock" size={10} /> {o().due ? fmtDue(o().due!) : t('No date')}
          </button>
          <span class="muted small" data-testid="objective-progress">
            {t('Tickets finished {done}/{n}', { done: o().ticketsDone, n: o().tickets })}
            <Show when={o().toTest}> · {t('{n} to test', { n: o().toTest })}</Show>
          </span>
          <span class="grow" />
          <Show when={o().state === 'open'}>
            <button
              class="btn small primary"
              disabled={!!blocker()}
              title={blocker() || t('Closes the objective: it is reached')}
              onClick={() => void props.apply(reachObjective(o().id))}
              data-testid="objective-reach"
            >
              {t('Reach the objective')}
            </button>
          </Show>
          <Show when={o().state !== 'open'}>
            <button class="btn small" onClick={() => void props.apply(setObjectiveState(o().id, 'open'))} data-testid="objective-reopen">
              {t('Reopen')}
            </button>
          </Show>
          <button
            class="icon-btn"
            title={t('More actions')}
            onClick={(e) =>
              contextMenu(e, [
                ...(o().state === 'open' ? [{ label: t('Abandon the objective'), action: () => void props.apply(setObjectiveState(o().id, 'abandoned')) }] : []),
                { label: t('Delete the objective'), action: () => void remove() },
              ])
            }
            data-testid="objective-more"
          >
            <Icon name="menu" size={14} />
          </button>
        </div>
        <div class="ms-bar ob-bar" title={t('Tickets finished {done}/{n}', { done: o().ticketsDone, n: o().tickets })}>
          <span style={{ width: `${pct()}%` }} />
        </div>
      </header>

      <div class="tk-body">
        <div class="tk-main">
          <Section title={t('Description')}>
            <EditableMarkdown
              value={o().description}
              max={MAX_DESCRIPTION}
              empty={t('No description: what reaching it means.')}
              onSave={(v) => props.apply(updateObjective(o().id, { description: v }))}
              testid="objective-description"
            />
          </Section>
          <Section title={`${t('Tickets')} · ${o().ticketsDone}/${o().tickets}`}>
            <Tickets o={o()} />
          </Section>
          <Notes o={o()} apply={props.apply} />
        </div>
        <aside class="tk-side">
          <Section title={t('Milestones')}>
            <For each={o().milestoneList} fallback={<p class="muted small">{t('No milestone.')}</p>}>
              {(m) => (
                <div class="tk-row" data-testid="objective-milestone">
                  <Icon name="flag" size={12} />
                  <button class="link ellipsis small" title={m.title} onClick={() => openMilestone(m.id)}>
                    {m.title}
                  </button>
                  <Show when={m.objective === o().id}>
                    <span class="badge" title={t('Its new tickets come into this objective')}>
                      {t('default')}
                    </span>
                  </Show>
                </div>
              )}
            </For>
          </Section>
          <Section title={t('History ({n})', { n: events().length })} folded>
            <ul class="tk-events">
              <For each={events()}>
                {(n) => (
                  <li>
                    <span class="muted small">{fmtDate(n.created)}</span> · {eventText(n.text)}
                    <Show when={n.author !== 'user'}>
                      <span class="badge">{n.author === 'claude' ? 'Claude' : t('assistant')}</span>
                    </Show>
                  </li>
                )}
              </For>
            </ul>
          </Section>
        </aside>
      </div>
    </div>
  )
}

/** A date YYYY-MM-DD in the language of the page. */
export function fmtDue(due: string): string {
  const [y, m, d] = due.split('-').map(Number)
  return new Date(y, m - 1, d).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}

/** The tickets grouped by milestone, then those without milestone; finished ones dimmed. */
function Tickets(props: { o: Objective }) {
  const groups = () => {
    const ids = [...new Set(props.o.ticketList.map((tk) => tk.milestone ?? 0))]
    ids.sort((a, b) => (a === 0 ? 1 : b === 0 ? -1 : a - b))
    return ids.map((id) => ({
      id,
      title: id ? (props.o.milestoneList.find((m) => m.id === id)?.title ?? `#${id}`) : t('No milestone'),
      tickets: props.o.ticketList.filter((tk) => (tk.milestone ?? 0) === id),
    }))
  }
  return (
    <Show when={props.o.ticketList.length} fallback={<p class="muted small">{t('No ticket yet: choose this objective in a ticket, or as the default objective of a milestone.')}</p>}>
      <For each={groups()}>
        {(g) => (
          <div data-testid="objective-group">
            <div class="kb-panel-group">
              <Icon name="flag" size={11} />
              <Show when={g.id} fallback={<span>{g.title}</span>}>
                <button class="link" onClick={() => openMilestone(g.id)}>
                  {g.title}
                </button>
              </Show>
              <span class="kb-count">
                {g.tickets.filter((tk) => tk.status === 'done' || tk.status === 'abandoned').length}/{g.tickets.length}
              </span>
            </div>
            <For each={g.tickets}>{(tk) => <TicketRow tk={tk} />}</For>
          </div>
        )}
      </For>
    </Show>
  )
}

function TicketRow(props: { tk: Summary }) {
  const finished = () => props.tk.status === 'done' || props.tk.status === 'abandoned'
  // The board fills the blockers; the list of the objective does not.
  const blockers = () => board.tickets.find((x) => x.id === props.tk.id)?.blockers ?? []
  return (
    <div class="tk-row" classList={{ muted: finished() }} data-testid="objective-ticket">
      <span class={`kb-dot st-${props.tk.status}`} title={statusLabels[props.tk.status]} />
      <button class="link ellipsis" onClick={() => openTicket(props.tk.id)}>
        #{props.tk.id} {props.tk.title}
      </button>
      <Show when={props.tk.parent}>
        <span class="kb-badge">↳ #{props.tk.parent}</span>
      </Show>
      <span class="grow" />
      <Show when={blockers().length}>
        <span class="kb-badge warn" title={t('Waits for {blockers}', { blockers: blockers().map(blockerText).join(', ') })}>
          <Icon name="lock" size={10} />
        </span>
      </Show>
      <span class="muted small">{statusLabels[props.tk.status]}</span>
    </div>
  )
}

function Notes(props: { o: Objective; apply: OApply }) {
  const [text, setText] = createSignal('')
  const notes = () => props.o.notes.filter((n) => n.kind === 'note')
  const add = async () => {
    await props.apply(addObjectiveNote(props.o.id, text()))
    setText('')
  }
  return (
    <Section title={t('Notes')}>
      <For each={notes()} fallback={<p class="muted small">{t('No note.')}</p>}>
        {(n) => (
          <div class="tk-note" data-testid="objective-note">
            <div class="tk-note-head">
              <span class="muted small">
                {n.author === 'claude' ? 'Claude' : n.author === 'model' ? t('Assistant') : t('You')} · {fmtDate(n.created)}
              </span>
              <span class="grow" />
              <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(deleteObjectiveNote(props.o.id, n.id))}>
                <Icon name="close" size={11} />
              </button>
            </div>
            <Markdown text={n.text} final />
          </div>
        )}
      </For>
      <textarea class="tk-note-input" rows={2} placeholder={t('Add context, a note…')} value={text()} onInput={(e) => setText(e.currentTarget.value)} />
      <div class="form-actions">
        <Count text={text()} max={MAX_NOTE} />
        <button class="btn small" disabled={!text().trim() || tooLong(text(), MAX_NOTE)} onClick={() => void add()}>
          {t('Add a note')}
        </button>
      </div>
    </Section>
  )
}

/** Asks the title of a new objective and creates it; its number, or 0 when cancelled. */
export async function newObjective(open = false): Promise<number> {
  const title = await prompt({ title: t('New objective'), label: t('Title of the deadline (MVP, a launch…)') })
  if (!title?.trim()) return 0
  try {
    const id = (await createObjective({ title })).id
    if (open) openObjective(id)
    return id
  } catch (e) {
    errorToast(e)
    return 0
  }
}

/** A select of the open objectives (and the current one), with None and New objective…. */
function Select(props: { value?: number; title: string; onChange: (id: number) => void; testid: string }) {
  const open = () => board.objectives.filter((o) => o.state === 'open' || o.id === props.value)
  return (
    <>
      <select
        class="small"
        onChange={async (e) => {
          let id = Number(e.currentTarget.value)
          if (id < 0) {
            e.currentTarget.value = String(props.value ?? 0)
            id = await newObjective()
            if (!id) return
          }
          props.onChange(id)
        }}
        title={props.title}
        data-testid={props.testid}
      >
        {/* selected per option: the list of the board may arrive after the ticket */}
        <option value={0} selected={!props.value}>
          {t('No objective')}
        </option>
        <For each={open()}>
          {(o) => (
            <option value={o.id} selected={o.id === props.value}>
              {o.title}
            </option>
          )}
        </For>
        <option value={-1}>{t('New objective…')}</option>
      </select>
      <Show when={props.value}>
        <button class="icon-btn small" title={t('Open the objective')} onClick={() => openObjective(props.value!)} data-testid={`${props.testid}-open`}>
          <Icon name="target" size={12} />
        </button>
      </Show>
    </>
  )
}

/** The objective of a ticket, in its meta row: chosen on a root or a lone ticket, shown on a step. */
export function ObjectiveSelect(props: { tk: Ticket; apply: (p: Promise<Ticket>) => Promise<void> }) {
  return (
    <Show
      when={!props.tk.parent}
      fallback={
        <Show when={props.tk.objective}>
          <button
            class="badge link"
            title={t('A step has the objective of its root: choose it on #{id}', { id: props.tk.parent! })}
            onClick={() => openObjective(props.tk.objective!)}
            data-testid="ticket-objective"
          >
            <Icon name="target" size={10} /> {props.tk.objectiveTitle}
          </button>
        </Show>
      }
    >
      <Select value={props.tk.objective} title={t('Objective')} onChange={(id) => void props.apply(updateTicket(props.tk.id, { objective: id }))} testid="ticket-objective" />
    </Show>
  )
}

/** The default objective of a milestone: its open tickets go into it, and the later ones. */
export function MilestoneObjectiveSelect(props: { ms: Milestone; apply: (p: Promise<Milestone>) => Promise<void> }) {
  return (
    <Select
      value={props.ms.objective}
      title={t('Default objective: its open tickets go into it, and those added later')}
      onChange={(id) => void props.apply(updateMilestone(props.ms.id, { objective: id }))}
      testid="milestone-objective"
    />
  )
}
