// A milestone (tab of the editor): its spec, user stories, tickets (those To test validated
// here), feedback, briefing conversations, notes and history; only the user validates it
// (docs/design/kanban.md). Also the milestone of a ticket in the ticket view.
import { createResource, createSignal, For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { contextMenu, prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { Markdown } from '../llm/parts'
import { closeTab, leaves, openFile, root, type TabState } from '../state/project'
import { board, ensureBoard, eventText, feedbackLabels, getTicket, moveTicket, openMilestone, openTicket, statusLabels, statusOrder, ticketVersion, updateTicket, type Complexity, type FeedbackKind, type Summary, type Ticket } from './state'
import {
  addMilestoneNote, createMilestone, deleteMilestone, deleteMilestoneNote, feedbackTicket, getMilestone, milestoneFeedbackOp, milestoneStateNames, milestoneVersion, setMilestoneState, storyOp, updateMilestone, validateMilestone,
  MAX_STORY, type Milestone,
} from './milestones'
import { ticketActions } from './actions'
import { openTicketChat, startMilestoneBriefing } from './sessions'
import { ComplexitySelect, Count, EditableMarkdown, EditableText, Section, tooLong } from './TicketView'
import { MAX_DESCRIPTION, MAX_NOTE } from './state'
import { fmtDate, t } from '../i18n'
import './kanban.css'

export type MApply = (p: Promise<Milestone>) => Promise<void>

export function MilestoneView(props: { tab: TabState; paneId: string }) {
  const id = props.tab.milestone!
  ensureBoard()
  const [ms, { mutate }] = createResource(
    () => milestoneVersion(id) + 1,
    async () => {
      try {
        return await getMilestone(id)
      } catch (e) {
        return { error: (e as Error).message } as any
      }
    },
  )
  const apply: MApply = async (p) => {
    try {
      mutate(await p)
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <Show when={ms()} fallback={<p class="muted pad">{t('Loading the milestone…')}</p>}>
      <Show when={!ms().error} fallback={<p class="danger pad">{ms().error}</p>}>
        <MilestoneBody ms={ms() as Milestone} apply={apply} tabId={props.tab.id} />
      </Show>
    </Show>
  )
}

/** Why the milestone cannot be validated yet ('' once it can). */
export function validateBlocker(ms: Milestone): string {
  if (ms.state !== 'open') return t('The milestone is closed.')
  if (ms.tickets > ms.ticketsDone) return t('{n} ticket(s) not finished', { n: ms.tickets - ms.ticketsDone })
  if (ms.stories > ms.storiesDone) return t('{n} user story(ies) not validated', { n: ms.stories - ms.storiesDone })
  if (ms.feedbackOpen) return t('{n} feedback(s) not handled', { n: ms.feedbackOpen })
  return ''
}

function MilestoneBody(props: { ms: Milestone; apply: MApply; tabId: string }) {
  const ms = () => props.ms
  const events = () => ms().notes.filter((n) => n.kind === 'event')
  const blocker = () => validateBlocker(ms())
  const remove = async () => {
    if (!confirm(t('Delete the milestone “{title}”? Its tickets stay.', { title: ms().title }))) return
    try {
      await deleteMilestone(ms().id)
      for (const l of leaves()) if (l.tabs.includes(props.tabId)) closeTab(l.id, props.tabId, true)
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <div class="tk ms" data-testid="milestone-view" data-state={ms().state}>
      <header class="tk-head">
        <div class="tk-title-row">
          <Icon name="flag" size={16} />
          <EditableText value={ms().title} class="tk-title" onSave={(v) => props.apply(updateMilestone(ms().id, { title: v }))} testid="milestone-title" />
          <span class={`kb-status ms-${ms().state}`} data-testid="milestone-state">
            {t(milestoneStateNames[ms().state])}
          </span>
        </div>
        <div class="tk-meta-row">
          <span class="muted small" data-testid="milestone-progress">
            {t('Tickets finished {done}/{n}', { done: ms().ticketsDone, n: ms().tickets })} · {t('User stories validated {done}/{n}', { done: ms().storiesDone, n: ms().stories })}
          </span>
          <span class="grow" />
          <button class="btn small" title={t('Conversation (Briefing mode) to clarify the milestone and write its stories and tickets')} onClick={() => void startMilestoneBriefing(ms())} data-testid="milestone-briefing">
            <Icon name="sparkle" size={12} /> {t('Briefing')}
          </button>
          <Show when={ms().state === 'open'}>
            <button
              class="btn small primary"
              disabled={!!blocker()}
              title={blocker() || t('Closes the milestone: its feature is done')}
              onClick={() => void props.apply(validateMilestone(ms().id))}
              data-testid="milestone-validate"
            >
              {t('Validate the milestone')}
            </button>
          </Show>
          <Show when={ms().state !== 'open'}>
            <button class="btn small" onClick={() => void props.apply(setMilestoneState(ms().id, 'open'))} data-testid="milestone-reopen">
              {t('Reopen')}
            </button>
          </Show>
          <button
            class="icon-btn"
            title={t('More actions')}
            onClick={(e) =>
              contextMenu(e, [
                ...(ms().state === 'open' ? [{ label: t('Abandon the milestone'), action: () => void props.apply(setMilestoneState(ms().id, 'abandoned')) }] : []),
                { label: t('Delete the milestone'), action: () => void remove() },
              ])
            }
            data-testid="milestone-more"
          >
            <Icon name="menu" size={14} />
          </button>
        </div>
      </header>

      <div class="tk-body">
        <div class="tk-main">
          <Section title={t('Description')}>
            <EditableMarkdown
              value={ms().description}
              max={MAX_DESCRIPTION}
              empty={t('No description.')}
              onSave={(v) => props.apply(updateMilestone(ms().id, { description: v }))}
              testid="milestone-description"
            />
            <Spec ms={ms()} apply={props.apply} />
          </Section>
          <Section title={`${t('User stories')} · ${ms().storiesDone}/${ms().stories}`}>
            <Stories ms={ms()} apply={props.apply} />
          </Section>
          <Section title={`${t('Tickets')} · ${ms().ticketsDone}/${ms().tickets}`}>
            <Tickets ms={ms()} apply={props.apply} />
          </Section>
          <MilestoneFeedback ms={ms()} apply={props.apply} />
          <Notes ms={ms()} apply={props.apply} />
        </div>
        <aside class="tk-side">
          <Section title={t('Briefing conversations')}>
            <For each={ms().chatList} fallback={<p class="muted small">{t('No conversation.')}</p>}>
              {(c) => (
                <div class="tk-row">
                  <Icon name="sparkle" size={12} />
                  <button class="link ellipsis small" title={c.title} onClick={() => void openTicketChat(c.chatId)} data-testid="milestone-chat">
                    {c.title || c.chatId}
                  </button>
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

/** The spec: a file of the repository opens in the IDE, an address in the browser. */
function Spec(props: { ms: Milestone; apply: MApply }) {
  const edit = async () => {
    const v = await prompt({ title: t('Spec of the milestone'), label: t('A path of the repository or an address'), value: props.ms.spec })
    if (v !== null) void props.apply(updateMilestone(props.ms.id, { spec: v }))
  }
  const open = () => {
    const s = props.ms.spec
    if (/^https?:\/\//.test(s)) window.open(s, '_blank', 'noopener')
    else openFile(s.startsWith('/') ? s : `${root()}/${s}`)
  }
  return (
    <div class="tk-row" data-testid="milestone-spec">
      <Icon name="file" size={12} />
      <span class="muted small">{t('Spec')}</span>
      <Show when={props.ms.spec} fallback={<span class="muted small">{t('none')}</span>}>
        <button class="link ellipsis" title={props.ms.spec} onClick={open} data-testid="milestone-spec-open">
          {props.ms.spec}
        </button>
      </Show>
      <button class="icon-btn small" title={t('Change the spec')} onClick={() => void edit()} data-testid="milestone-spec-edit">
        <Icon name="edit" size={11} />
      </button>
    </div>
  )
}

function Stories(props: { ms: Milestone; apply: MApply }) {
  const [text, setText] = createSignal('')
  const add = async () => {
    await props.apply(storyOp(props.ms.id, { op: 'add', text: text() }))
    setText('')
  }
  return (
    <>
      <For each={props.ms.storyList} fallback={<p class="muted small">{t('No user story yet: what a user can do once the milestone is done.')}</p>}>
        {(s, i) => (
          <div class="tk-goal" classList={{ done: s.done }} data-testid="milestone-story">
            <input type="checkbox" checked={s.done} title={t('Validated')} onChange={(e) => props.apply(storyOp(props.ms.id, { op: 'check', id: s.id, done: e.currentTarget.checked }))} data-testid="milestone-story-check" />
            <EditableText value={s.text} class="grow" onSave={(v) => props.apply(storyOp(props.ms.id, { op: 'edit', id: s.id, text: v }))} />
            <button class="icon-btn small" title={t('Move up')} disabled={i() === 0} onClick={() => props.apply(storyOp(props.ms.id, { op: 'move', id: s.id, delta: -1 }))}>
              <Icon name="up" size={11} />
            </button>
            <button class="icon-btn small" title={t('Move down')} disabled={i() === props.ms.storyList.length - 1} onClick={() => props.apply(storyOp(props.ms.id, { op: 'move', id: s.id, delta: 1 }))}>
              <Icon name="down" size={11} />
            </button>
            <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(storyOp(props.ms.id, { op: 'delete', id: s.id }))}>
              <Icon name="close" size={11} />
            </button>
          </div>
        )}
      </For>
      <div class="tk-goal-add">
        <input
          class="input small grow"
          placeholder={t('As a user, I can…')}
          value={text()}
          onInput={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && text().trim() && !tooLong(text(), MAX_STORY) && void add()}
          data-testid="milestone-story-input"
        />
        <button class="btn small" disabled={!text().trim() || tooLong(text(), MAX_STORY)} onClick={() => void add()} data-testid="milestone-story-add">
          {t('Add')}
        </button>
      </div>
    </>
  )
}

/** The tickets by status; those To test show how to test them and are validated here. */
function Tickets(props: { ms: Milestone; apply: MApply }) {
  const of = (s: string) => props.ms.ticketList.filter((tk) => tk.status === s)
  return (
    <Show when={props.ms.ticketList.length} fallback={<p class="muted small">{t('No ticket yet: choose this milestone in a ticket, or let a briefing write them.')}</p>}>
      <For each={statusOrder}>
        {(s) => (
          <Show when={of(s).length}>
            <div class="kb-panel-group">
              <span class={`kb-dot st-${s}`} /> {statusLabels[s]} <span class="kb-count">{of(s).length}</span>
            </div>
            <For each={of(s)}>{(tk) => (s === 'to_test' ? <TicketToTest id={tk.id} apply={props.apply} msId={props.ms.id} /> : <TicketRow tk={tk} />)}</For>
          </Show>
        )}
      </For>
    </Show>
  )
}

function TicketRow(props: { tk: Summary }) {
  return (
    <div class="tk-row" data-testid="milestone-ticket">
      <button class="link ellipsis" onClick={() => openTicket(props.tk.id)}>
        #{props.tk.id} {props.tk.title}
      </button>
      <Show when={props.tk.parent}>
        <span class="kb-badge">↳ #{props.tk.parent}</span>
      </Show>
    </div>
  )
}

/** A ticket To test: its how to test and its validation (the buttons of the ticket view). */
function TicketToTest(props: { id: number; msId: number; apply: MApply }) {
  const [tk, { mutate }] = createResource(
    () => ticketVersion(props.id) + 1,
    () => getTicket(props.id).catch(() => undefined),
  )
  const apply = async (p: Promise<Ticket>) => {
    try {
      mutate(await p)
      await props.apply(getMilestone(props.msId))
    } catch (e) {
      errorToast(e)
    }
  }
  const validate = () => {
    const x = tk()
    if (!x) return []
    return ticketActions(x, { move: (s, c) => apply(moveTicket(x.id, s, 'user', c ?? '')), apply, focusFeedback: () => openTicket(x.id) }).filter(
      (b) => b.testid === 'ticket-close' || b.testid === 'ticket-step' || b.testid === 'ticket-fix-feedbacks',
    )
  }
  return (
    <Show when={tk()}>
      {(x) => (
        <div class="ms-totest" data-testid="milestone-totest">
          <div class="tk-row">
            <button class="link ellipsis" onClick={() => openTicket(x().id)}>
              #{x().id} {x().title}
            </button>
            <span class="grow" />
            <Show when={x().feedbackOpen}>
              <span class="warn small">
                <Icon name="comment" size={11} /> {x().feedbackOpen}
              </span>
            </Show>
            <For each={validate()}>
              {(b) =>
                b.testid === 'ticket-fix-feedbacks' ? (
                  <button class="btn small" onClick={() => openTicket(x().id)} title={b.title}>
                    {t('Open the ticket')}
                  </button>
                ) : (
                  <button class="btn small primary" title={b.title} onClick={b.run} data-testid={`milestone-${b.testid}`}>
                    {b.label}
                  </button>
                )
              }
            </For>
          </div>
          <Show when={x().testSummary.trim()} fallback={<p class="muted small">{t('No test instructions yet.')}</p>}>
            <Markdown text={x().testSummary} final />
          </Show>
        </div>
      )}
    </Show>
  )
}

function MilestoneFeedback(props: { ms: Milestone; apply: MApply }) {
  const [text, setText] = createSignal('')
  const [kind, setKind] = createSignal<FeedbackKind>('bug')
  const [complexity, setComplexity] = createSignal<Complexity | ''>('')
  const open = () => props.ms.feedbackList.filter((f) => !f.done).length
  const add = async () => {
    await props.apply(milestoneFeedbackOp(props.ms.id, { op: 'add', kind: kind(), text: text(), complexity: complexity() }))
    setText('')
  }
  return (
    <Section title={`${t('Feedback')} ${props.ms.feedbackList.length ? `· ${t('{n} open', { n: open() })}` : ''}`}>
      <For each={props.ms.feedbackList} fallback={<p class="muted small">{t('No feedback.')}</p>}>
        {(f) => (
          <div class={`tk-feedback k-${f.kind}`} classList={{ done: f.done }} data-testid="milestone-feedback-item">
            <div class="tk-note-head">
              <input type="checkbox" checked={f.done} title={t('Handled')} onChange={(e) => props.apply(milestoneFeedbackOp(props.ms.id, { op: 'check', id: f.id, done: e.currentTarget.checked }))} data-testid="milestone-feedback-check" />
              <span class={`tk-fb-kind k-${f.kind}`}>{feedbackLabels[f.kind]}</span>
              <ComplexitySelect value={f.complexity} onChange={(c) => props.apply(milestoneFeedbackOp(props.ms.id, { op: 'complexity', id: f.id, complexity: c }))} />
              <span class="muted small">{fmtDate(f.created)}</span>
              <span class="grow" />
              <Show
                when={f.ticket}
                fallback={
                  <Show when={!f.done && props.ms.state === 'open'}>
                    <button class="btn small" title={t('A ticket of the milestone handles it: the feedback is handled once the ticket is Done')} onClick={() => props.apply(feedbackTicket(props.ms.id, f.id))} data-testid="milestone-feedback-ticket">
                      {t('Make a ticket')}
                    </button>
                  </Show>
                }
              >
                <button class="link small" onClick={() => openTicket(f.ticket!)} data-testid="milestone-feedback-link">
                  → #{f.ticket}
                </button>
              </Show>
              <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(milestoneFeedbackOp(props.ms.id, { op: 'delete', id: f.id }))}>
                <Icon name="close" size={11} />
              </button>
            </div>
            <Markdown text={f.text} final />
          </div>
        )}
      </For>
      <Show when={props.ms.state === 'open'}>
        <div class="tk-feedback-add">
          <select class="small" value={kind()} onChange={(e) => setKind(e.currentTarget.value as FeedbackKind)} data-testid="milestone-feedback-kind">
            <For each={Object.entries(feedbackLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
          </select>
          <ComplexitySelect value={complexity()} onChange={setComplexity} />
          <textarea class="tk-note-input" rows={3} placeholder={t('What did you notice while testing the feature?')} value={text()} onInput={(e) => setText(e.currentTarget.value)} data-testid="milestone-feedback-input" />
        </div>
        <div class="form-actions">
          <Count text={text()} max={MAX_NOTE} />
          <button class="btn small primary" disabled={!text().trim() || tooLong(text(), MAX_NOTE)} onClick={() => void add()} data-testid="milestone-feedback-add">
            {t('Add feedback')}
          </button>
        </div>
      </Show>
    </Section>
  )
}

function Notes(props: { ms: Milestone; apply: MApply }) {
  const [text, setText] = createSignal('')
  const notes = () => props.ms.notes.filter((n) => n.kind === 'note')
  const add = async () => {
    await props.apply(addMilestoneNote(props.ms.id, text()))
    setText('')
  }
  return (
    <Section title={t('Notes')}>
      <For each={notes()} fallback={<p class="muted small">{t('No note.')}</p>}>
        {(n) => (
          <div class="tk-note" data-testid="milestone-note">
            <div class="tk-note-head">
              <span class="muted small">
                {n.author === 'claude' ? 'Claude' : n.author === 'model' ? t('Assistant') : t('You')} · {fmtDate(n.created)}
              </span>
              <span class="grow" />
              <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(deleteMilestoneNote(props.ms.id, n.id))}>
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

/** The milestone of a ticket, in its meta row: chosen on a root or a lone ticket, shown on a step. */
export function MilestoneSelect(props: { tk: Ticket; apply: (p: Promise<Ticket>) => Promise<void> }) {
  const open = () => board.milestones.filter((m) => m.state === 'open' || m.id === props.tk.milestone)
  return (
    <Show
      when={!props.tk.parent}
      fallback={
        <Show when={props.tk.milestone}>
          <button class="badge link" title={t('A step has the milestone of its root')} onClick={() => openMilestone(props.tk.milestone!)} data-testid="ticket-milestone">
            <Icon name="flag" size={10} /> {props.tk.milestoneTitle}
          </button>
        </Show>
      }
    >
      <select
        class="small"
        onChange={async (e) => {
          let id = Number(e.currentTarget.value)
          if (id < 0) {
            e.currentTarget.value = String(props.tk.milestone ?? 0)
            const title = await prompt({ title: t('New milestone'), label: t('Title of the feature') })
            if (!title?.trim()) return
            try {
              id = (await createMilestone({ title })).id
            } catch (err) {
              errorToast(err)
              return
            }
          }
          await props.apply(updateTicket(props.tk.id, { milestone: id }))
        }}
        title={t('Milestone')}
        data-testid="ticket-milestone"
      >
        {/* selected per option: the list of the board may arrive after the ticket */}
        <option value={0} selected={!props.tk.milestone}>
          {t('No milestone')}
        </option>
        <For each={open()}>
          {(m) => (
            <option value={m.id} selected={m.id === props.tk.milestone}>
              {m.title}
            </option>
          )}
        </For>
        <option value={-1}>{t('New milestone…')}</option>
      </select>
      <Show when={props.tk.milestone}>
        <button class="icon-btn small" title={t('Open the milestone')} onClick={() => openMilestone(props.tk.milestone!)} data-testid="ticket-milestone-open">
          <Icon name="flag" size={12} />
        </button>
      </Show>
    </Show>
  )
}

/** In How to test: the milestone of the ticket and its user stories, to keep in mind. */
export function MilestoneReminder(props: { tk: Ticket }) {
  return (
    <Show when={props.tk.milestone}>
      <div class="ms-reminder" data-testid="ticket-milestone-reminder">
        <div class="tk-row">
          <Icon name="flag" size={12} />
          <span class="muted small">{t('Milestone')}</span>
          <button class="link ellipsis" onClick={() => openMilestone(props.tk.milestone!)}>
            {props.tk.milestoneTitle}
          </button>
        </div>
        <Show when={props.tk.milestoneStories?.length}>
          <ul class="ms-stories">
            <For each={props.tk.milestoneStories}>
              {(s) => (
                <li classList={{ done: s.done }}>
                  <Icon name={s.done ? 'check' : 'circle'} size={10} /> {s.text}
                </li>
              )}
            </For>
          </ul>
        </Show>
      </div>
    </Show>
  )
}
