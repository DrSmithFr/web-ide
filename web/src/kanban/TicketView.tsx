// Detail of a ticket (tab of the editor): the sections of its status, those of the earlier
// statuses folded (description, briefing and notes; plan and goals; development and git;
// how to test, feedback and pull request); lineage, linked files, attachments and history
// aside, and the buttons that move it on (docs/kanban.md).
import { createEffect, createMemo, createResource, createSignal, For, type JSX, on, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { contextMenu, fuzzy, pick } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { Markdown } from '../llm/parts'
import { request } from '../pod/rpc'
import { basename, closeTab, leaves, openFile, relPath, root, type TabState } from '../state/project'
import {
  addAttachment, addNote, eventText, attachmentBlob, deleteAttachment, deleteNote, deleteTicket, ensureBoard, feedbackLabels, feedbackOp, getTicket, goalOp,
  moveTicket, priorityLabels, sizeNames, statusLabels, ticketVersion, updateTicket,
  MAX_DESCRIPTION, MAX_NOTE, type FeedbackKind, type Goal, type Priority, type Size, type Status, type Ticket,
} from './state'
import { abandonTicket, ChatLink, PullRequest, ticketActions, TicketChats, TicketGit } from './actions'
import { startWorkSession } from './sessions'
import { LineageSection } from './Lineage'
import { claudeItems } from './claude'
import { fmtDate, fmtSize, t } from '../i18n'
import './kanban.css'

export function TicketView(props: { tab: TabState; paneId: string }) {
  const id = props.tab.ticket!
  ensureBoard()
  const [ticket, { mutate: setTicket }] = createResource(
    () => ticketVersion(id) + 1,
    async () => {
      try {
        return await getTicket(id)
      } catch (e) {
        return { error: (e as Error).message } as any
      }
    },
  )
  // Every change answers the ticket as it is now.
  const apply = async (p: Promise<Ticket>) => {
    try {
      setTicket(await p)
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <Show when={ticket()} fallback={<p class="muted pad">{t('Loading the ticket…')}</p>}>
      <Show when={!ticket().error} fallback={<p class="danger pad">{ticket().error}</p>}>
        <TicketBody tk={ticket() as Ticket} apply={apply} paneId={props.paneId} tabId={props.tab.id} />
      </Show>
    </Show>
  )
}

export type Apply = (p: Promise<Ticket>) => Promise<void>

function TicketBody(props: { tk: Ticket; apply: Apply; paneId: string; tabId: string }) {
  const tk = () => props.tk
  let feedbackArea!: HTMLTextAreaElement
  const events = () => tk().notes.filter((n) => n.kind === 'event')
  const closed = () => tk().status === 'done' || tk().status === 'abandoned'

  const move = (s: Status, comment = '') => props.apply(moveTicket(tk().id, s, 'user', comment))
  const focusFeedback = () => {
    feedbackArea?.focus()
    feedbackArea?.scrollIntoView({ block: 'center' })
  }

  const remove = async () => {
    if (!confirm(t('Delete ticket #{id} for good?', { id: tk().id }))) return
    try {
      await deleteTicket(tk().id)
      for (const l of leaves()) if (l.tabs.includes(props.tabId)) closeTab(l.id, props.tabId, true)
    } catch (e) {
      errorToast(e)
    }
  }

  const buttons = () => ticketActions(tk(), { move, apply: props.apply, focusFeedback })

  // The sections of the status: a closed ticket keeps those of "To test" that have content.
  const parts = () => {
    if (!closed()) return layouts[tk().status as Stage].parts
    const list = layouts.review.parts.filter((p) => filled[p](tk()))
    // No rule at either end.
    return list.filter((p, i) => p !== 'sep' || (i > 0 && i < list.length - 1))
  }
  const folded = (p: Part) => (closed() ? p !== 'description' : !layouts[tk().status as Stage].open.includes(p))
  const part = (p: Part): JSX.Element => {
    switch (p) {
      case 'sep':
        return <hr class="tk-sep" />
      case 'description':
        return (
          <Section title={t('Description')} folded={folded(p)}>
            <EditableMarkdown
              value={tk().description}
              max={MAX_DESCRIPTION}
              empty={t('No description.')}
              onSave={(v) => props.apply(updateTicket(tk().id, { description: v }))}
              testid="ticket-description"
            />
          </Section>
        )
      case 'briefing':
        return <TicketChats tk={tk()} roles={['briefing']} title={t('Briefing conversations')} folded={folded(p)} />
      case 'notes':
        return <Notes tk={tk()} apply={props.apply} folded={folded(p)} />
      case 'planChats':
        return <TicketChats tk={tk()} roles={['plan']} title={t('Plan conversations')} folded={folded(p)} />
      case 'plan':
        return (
          <Section title={t('Implementation plan')} folded={folded(p)}>
            <EditableMarkdown value={tk().plan} empty={t('No plan yet.')} onSave={(v) => props.apply(updateTicket(tk().id, { plan: v }))} testid="ticket-plan" />
          </Section>
        )
      case 'goals':
        return (
          <Section title={`${t('Goals')} ${tk().goals ? `· ${tk().goalsDone}/${tk().goals}` : ''}`} folded={folded(p)}>
            <Goals tk={tk()} apply={props.apply} />
          </Section>
        )
      case 'devChats':
        return <TicketChats tk={tk()} roles={['dev', 'resolve']} title={t('Development conversations')} folded={folded(p)} />
      case 'test':
        return (
          <Section title={t('How to test')} folded={folded(p)}>
            <EditableMarkdown
              value={tk().testSummary}
              empty={t('Filled in by the model when it finishes the development.')}
              onSave={(v) => props.apply(updateTicket(tk().id, { testSummary: v }))}
              testid="ticket-test"
            />
          </Section>
        )
      case 'git':
        return <TicketGit tk={tk()} apply={props.apply} folded={folded(p)} />
      case 'feedback':
        return <FeedbackList tk={tk()} apply={props.apply} areaRef={(el) => (feedbackArea = el)} folded={folded(p)} />
      case 'pr':
        return <PullRequest tk={tk()} apply={props.apply} folded={folded(p)} />
    }
  }

  return (
    <div class="tk" data-testid="ticket-view" data-status={tk().status}>
      <header class="tk-head">
        <div class="tk-title-row">
          <span class="tk-num">#{tk().id}</span>
          <EditableText value={tk().title} class="tk-title" onSave={(v) => props.apply(updateTicket(tk().id, { title: v }))} testid="ticket-title" />
          <span class={`kb-status st-${tk().status}`} data-testid="ticket-status">
            {statusLabels[tk().status]}
          </span>
        </div>
        <div class="tk-meta-row">
          <select class="small" value={tk().priority} onChange={(e) => props.apply(updateTicket(tk().id, { priority: e.currentTarget.value as Priority }))} title={t('Priority')}>
            <For each={Object.entries(priorityLabels)}>{([v, l]) => <option value={v}>{t('{priority} priority', { priority: l })}</option>}</For>
          </select>
          <select class="small" value={tk().size ?? ''} onChange={(e) => props.apply(updateTicket(tk().id, { size: e.currentTarget.value as Size | '' }))} title={t('Estimated size, written with the plan')} data-testid="ticket-size">
            <option value="">{t('Size not estimated')}</option>
            <For each={Object.entries(sizeNames)}>{([v, l]) => <option value={v}>{t('Size {size}', { size: l })}</option>}</For>
          </select>
          <span class="muted small">{t('created on {date}', { date: fmtDate(tk().created) })}</span>
          <span class="grow" />
          <For each={buttons()}>
            {(b) => (
              <button class={`btn small ${b.primary ? 'primary' : ''} ${b.danger ? 'danger' : ''}`} disabled={b.disabled} title={b.title} onClick={b.run} data-testid={b.testid}>
                {b.label}
              </button>
            )}
          </For>
          <Show when={claudeItems(tk()).length}>
            <button class="btn small" title={t('Run Claude Code on this ticket in a terminal')} onClick={(e) => contextMenu(e, claudeItems(tk()))} data-testid="ticket-claude">
              Claude Code
            </button>
          </Show>
          <button
            class="icon-btn"
            title={t('More actions')}
            onClick={(e) =>
              contextMenu(e, [
                ...(!closed() ? [{ label: t('Abandon the ticket'), action: () => void abandonTicket(tk(), props.apply) }] : []),
                { label: t('Delete the ticket'), action: () => void remove() },
              ])
            }
          >
            <Icon name="menu" size={14} />
          </button>
        </div>
      </header>

      <div class="tk-body">
        <div class="tk-main">
          <For each={parts()}>{(p) => part(p)}</For>
        </div>

        <aside class="tk-side">
          <LineageSection tk={tk()} apply={props.apply} />
          <Section title={t('Linked files')}>
            <For each={tk().files} fallback={<p class="muted small">{t('No file.')}</p>}>
              {(f) => (
                <div class="tk-row">
                  <Icon name="file" size={12} />
                  <button class="link ellipsis" title={f} onClick={() => openFile(absolute(f))}>
                    {f}
                  </button>
                  <span class="grow" />
                  <button class="icon-btn small" title={t('Remove')} onClick={() => props.apply(updateTicket(tk().id, { removeFiles: [f] }))}>
                    <Icon name="close" size={11} />
                  </button>
                </div>
              )}
            </For>
            <button class="btn small" onClick={() => void addFile(tk(), props.apply)} data-testid="ticket-file-add">
              <Icon name="plus" size={12} /> {t('Add a file')}
            </button>
          </Section>

          <Section title={t('Attachments')}>
            <For each={tk().attachments} fallback={<p class="muted small">{t('No attachment.')}</p>}>
              {(a) => (
                <div class="tk-row">
                  <Icon name="paperclip" size={12} />
                  <button class="link ellipsis" title={a.name} onClick={() => void openAttachment(tk().id, a.id)}>
                    {a.name}
                  </button>
                  <span class="muted small nowrap">{fmtSize(a.size)}</span>
                  <span class="grow" />
                  <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(deleteAttachment(tk().id, a.id))}>
                    <Icon name="close" size={11} />
                  </button>
                </div>
              )}
            </For>
            <label class="btn small">
              <Icon name="plus" size={12} /> {t('Attach a file')}
              <input
                type="file"
                multiple
                hidden
                onChange={async (e) => {
                  const files = [...(e.currentTarget.files ?? [])]
                  e.currentTarget.value = ''
                  for (const f of files) await props.apply(addAttachment(tk().id, f))
                }}
                data-testid="ticket-attach"
              />
            </label>
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

/** Sections of the main column of a ticket. */
type Part = 'sep' | 'description' | 'briefing' | 'notes' | 'planChats' | 'plan' | 'goals' | 'devChats' | 'test' | 'git' | 'feedback' | 'pr'
type Stage = 'new' | 'todo' | 'in_progress' | 'review'

// The sections each status shows, in order, and those open (the others folded).
// The sections each status shows, in order, and those open (the others folded). Its own
// sections come first; a rule ('sep') sets apart the earlier ones, folded.
const layouts: Record<Stage, { parts: Part[]; open: Part[] }> = {
  new: { parts: ['description', 'briefing', 'notes'], open: ['description', 'briefing', 'notes'] },
  todo: { parts: ['description', 'notes', 'briefing', 'planChats', 'goals', 'plan'], open: ['description', 'briefing', 'planChats', 'goals', 'plan'] },
  in_progress: {
    parts: ['devChats', 'goals', 'git', 'sep', 'description', 'briefing', 'notes', 'planChats', 'plan'],
    open: ['devChats', 'goals', 'git'],
  },
  review: {
    parts: ['test', 'feedback', 'git', 'pr', 'sep', 'description', 'briefing', 'notes', 'planChats', 'plan', 'goals', 'devChats'],
    open: ['test', 'feedback', 'git', 'pr'],
  },
}

const chats = (tk: Ticket, ...roles: string[]) => tk.chatList.some((c) => roles.includes(c.role))
const filled: Record<Part, (tk: Ticket) => boolean> = {
  sep: () => true,
  description: () => true,
  briefing: (tk) => chats(tk, 'briefing'),
  notes: (tk) => tk.notes.some((n) => n.kind === 'note'),
  planChats: (tk) => chats(tk, 'plan'),
  plan: (tk) => !!tk.plan.trim(),
  goals: (tk) => tk.goalList.length > 0,
  devChats: (tk) => chats(tk, 'dev', 'resolve'),
  test: (tk) => !!tk.testSummary.trim(),
  git: (tk) => !!(tk.branch || tk.snapshot),
  feedback: (tk) => tk.feedbackList.length > 0 || chats(tk, 'correction'),
  pr: (tk) => !!tk.pr,
}

/** Characters used out of the maximum of a text. */
function Count(props: { text: string; max: number }) {
  const n = () => [...props.text.trim()].length
  return (
    <span class="tk-count small" classList={{ over: n() > props.max }} data-testid="ticket-count">
      {n()}/{props.max}
    </span>
  )
}

const tooLong = (text: string, max: number) => [...text.trim()].length > max

function Notes(props: { tk: Ticket; apply: Apply; folded?: boolean }) {
  const [text, setText] = createSignal('')
  const notes = () => props.tk.notes.filter((n) => n.kind === 'note')
  const add = async () => {
    await props.apply(addNote(props.tk.id, text()))
    setText('')
  }
  return (
    <Section title={t('Notes')} folded={props.folded}>
      <For each={notes()} fallback={<p class="muted small">{t('No note.')}</p>}>
        {(n) => (
          <div class="tk-note" data-testid="ticket-note">
            <div class="tk-note-head">
              <span class="muted small">
                {n.author === 'claude' ? 'Claude' : n.author === 'model' ? t('Assistant') : t('You')} · {fmtDate(n.created)}
              </span>
              <Show when={n.chatId}>
                <ChatLink tk={props.tk} chatId={n.chatId!} />
              </Show>
              <span class="grow" />
              <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(deleteNote(props.tk.id, n.id))}>
                <Icon name="close" size={11} />
              </button>
            </div>
            <Markdown text={n.text} final />
          </div>
        )}
      </For>
      <textarea class="tk-note-input" rows={3} placeholder={t('Add context, a note…')} value={text()} onInput={(e) => setText(e.currentTarget.value)} data-testid="ticket-note-input" />
      <div class="form-actions">
        <Count text={text()} max={MAX_NOTE} />
        <button class="btn small" disabled={!text().trim() || tooLong(text(), MAX_NOTE)} onClick={() => void add()} data-testid="ticket-note-add">
          {t('Add a note')}
        </button>
      </div>
    </Section>
  )
}

/** Test feedback: added while the ticket is under test, each one handled by a conversation. */
function FeedbackList(props: { tk: Ticket; apply: Apply; areaRef: (el: HTMLTextAreaElement) => void; folded?: boolean }) {
  const [text, setText] = createSignal('')
  const [kind, setKind] = createSignal<FeedbackKind>('bug')
  const list = () => props.tk.feedbackList
  const open = () => list().filter((f) => !f.done).length
  const add = async () => {
    await props.apply(feedbackOp(props.tk.id, { op: 'add', kind: kind(), text: text() }))
    setText('')
  }
  // Correction conversations not tied to a feedback.
  const tied = () => new Set(list().map((f) => f.chatId).filter(Boolean))
  return (
    <>
      <Section title={`${t('Feedback')} ${list().length ? `· ${t('{n} open', { n: open() })}` : ''}`} folded={props.folded}>
        <For each={list()} fallback={<p class="muted small">{t('No feedback.')}</p>}>
          {(f) => (
            <div class={`tk-feedback k-${f.kind}`} classList={{ done: f.done }} data-testid="ticket-feedback-item">
              <div class="tk-note-head">
                <input
                  type="checkbox"
                  checked={f.done}
                  title={t('Handled')}
                  onChange={(e) => props.apply(feedbackOp(props.tk.id, { op: 'check', id: f.id, done: e.currentTarget.checked }))}
                />
                <span class={`tk-fb-kind k-${f.kind}`}>{feedbackLabels[f.kind]}</span>
                <span class="muted small">{fmtDate(f.created)}</span>
                <Show when={f.chatId}>
                  <ChatLink tk={props.tk} chatId={f.chatId!} />
                </Show>
                <span class="grow" />
                <Show when={!f.done && props.tk.status === 'review'}>
                  <button class="btn small" onClick={() => void startWorkSession(props.tk, 'correction', f)} data-testid="ticket-feedback-session">
                    <Icon name="sparkle" size={12} /> {t('Fix session')}
                  </button>
                </Show>
                <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(feedbackOp(props.tk.id, { op: 'delete', id: f.id }))}>
                  <Icon name="close" size={11} />
                </button>
              </div>
              <Markdown text={f.text} final />
            </div>
          )}
        </For>
        <Show when={props.tk.status === 'review'}>
          <div class="tk-feedback-add">
            <select class="small" value={kind()} onChange={(e) => setKind(e.currentTarget.value as FeedbackKind)} data-testid="ticket-feedback-kind">
              <For each={Object.entries(feedbackLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
            </select>
            <textarea
              ref={props.areaRef}
              class="tk-note-input"
              rows={3}
              placeholder={t('What did you notice while testing?')}
              value={text()}
              onInput={(e) => setText(e.currentTarget.value)}
              data-testid="ticket-feedback-input"
            />
          </div>
          <div class="form-actions">
            <Count text={text()} max={MAX_NOTE} />
            <button class="btn small primary" disabled={!text().trim() || tooLong(text(), MAX_NOTE)} onClick={() => void add()} data-testid="ticket-feedback-add">
              {t('Add feedback')}
            </button>
          </div>
        </Show>
      </Section>
      <TicketChats tk={props.tk} roles={['correction']} title={t('Other correction conversations')} hideEmpty exclude={tied()} folded={props.folded} />
    </>
  )
}

function absolute(p: string) {
  return p.startsWith('/') ? p : `${root()}/${p}`
}


let fileCache: { at: number; files: string[] } | null = null
async function addFile(tk: Ticket, apply: Apply) {
  const files = async () => {
    if (!fileCache || Date.now() - fileCache.at > 15000) fileCache = { at: Date.now(), files: await request<string[]>('search.files') }
    return fileCache.files
  }
  const p = await pick<string>({
    placeholder: t('File to link to the ticket'),
    pathDetail: true,
    provider: async (q) => {
      const list = await files()
      return list
        .map((f) => ({ f, s: q ? fuzzy(q, basename(f)) * 2 + fuzzy(q, relPath(f)) : 1 }))
        .filter((x) => x.s > 0)
        .sort((a, b) => b.s - a.s)
        .slice(0, 100)
        .map(({ f }) => ({ label: basename(f), detail: relPath(f), value: relPath(f) }))
    },
  })
  if (p) await apply(updateTicket(tk.id, { addFiles: [p] }))
}

async function openAttachment(id: number, aid: number) {
  try {
    const { name, blob } = await attachmentBlob(id, aid)
    const url = URL.createObjectURL(blob)
    if (/^(image|video|audio|text)\/|pdf/.test(blob.type)) window.open(url, '_blank')
    else {
      const a = document.createElement('a')
      a.href = url
      a.download = name
      a.click()
    }
    setTimeout(() => URL.revokeObjectURL(url), 60000)
  } catch (e) {
    errorToast(e)
  }
}

// ---------- pieces ----------

export function Section(props: { title: string; children: JSX.Element; folded?: boolean; actions?: JSX.Element }) {
  const [open, setOpen] = createSignal(!props.folded)
  // Folded again or opened when the status of the ticket changes.
  const folded = createMemo(() => !!props.folded)
  createEffect(on(folded, (f) => setOpen(!f), { defer: true }))
  return (
    <section class="tk-section" classList={{ folded: !open() }}>
      <header class="tk-section-head">
        <button class="tk-section-toggle" onClick={() => setOpen(!open())}>
          <span class="tk-chev" classList={{ open: open() }}>
            <Icon name="chevron" size={12} />
          </span>
          {props.title}
        </button>
        <span class="grow" />
        {props.actions}
      </header>
      <Show when={open()}>
        <div class="tk-section-body">{props.children}</div>
      </Show>
    </section>
  )
}

function EditableText(props: { value: string; class?: string; onSave: (v: string) => void; testid?: string }) {
  const [editing, setEditing] = createSignal(false)
  let input!: HTMLInputElement
  const save = () => {
    const v = input.value.trim()
    setEditing(false)
    if (v && v !== props.value) props.onSave(v)
  }
  return (
    <Show
      when={editing()}
      fallback={
        <h1 class={props.class} title={t('Click to edit')} onClick={() => setEditing(true)} data-testid={props.testid}>
          {props.value}
        </h1>
      }
    >
      <input
        ref={(el) => {
          input = el
          queueMicrotask(() => el.select())
        }}
        class={`${props.class} input`}
        value={props.value}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save()
          if (e.key === 'Escape') {
            e.preventDefault()
            setEditing(false)
          }
        }}
      />
    </Show>
  )
}

export function EditableMarkdown(props: { value: string; empty: string; max?: number; onSave: (v: string) => void | Promise<void>; testid?: string }) {
  const [editing, setEditing] = createSignal(false)
  const [draft, setDraft] = createSignal('')
  const start = () => {
    setDraft(props.value)
    setEditing(true)
  }
  const over = () => !!props.max && tooLong(draft(), props.max)
  const save = async () => {
    if (over()) return
    setEditing(false)
    if (draft() !== props.value) await props.onSave(draft())
  }
  return (
    <div class="tk-md" data-testid={props.testid}>
      <Show
        when={editing()}
        fallback={
          <>
            <Show when={props.value.trim()} fallback={<p class="muted small">{props.empty}</p>}>
              <Markdown text={props.value} final />
            </Show>
            <button class="btn small tk-edit" onClick={start} data-testid={props.testid && `${props.testid}-edit`}>
              <Icon name="edit" size={12} /> {t('Edit')}
            </button>
          </>
        }
      >
        <textarea
          class="tk-md-input"
          rows={Math.min(30, Math.max(6, draft().split('\n').length + 1))}
          value={draft()}
          onInput={(e) => setDraft(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) void save()
            if (e.key === 'Escape') {
            e.preventDefault()
            setEditing(false)
          }
          }}
          ref={(el) => queueMicrotask(() => el.focus())}
        />
        <div class="form-actions">
          <Show when={props.max}>
            <Count text={draft()} max={props.max!} />
          </Show>
          <span class="muted small">{t('Ctrl+Enter to save')}</span>
          <button class="btn small" onClick={() => setEditing(false)}>
            {t('Cancel')}
          </button>
          <button class="btn small primary" disabled={over()} onClick={() => void save()} data-testid={props.testid && `${props.testid}-save`}>
            {t('Save')}
          </button>
        </div>
      </Show>
    </div>
  )
}

function Goals(props: { tk: Ticket; apply: Apply }) {
  const [title, setTitle] = createSignal('')
  const [description, setDescription] = createSignal('')
  const add = async () => {
    if (!title().trim()) return
    await props.apply(goalOp(props.tk.id, { op: 'add', text: title(), description: description() }))
    setTitle('')
    setDescription('')
  }
  return (
    <div class="tk-goals">
      <For each={props.tk.goalList} fallback={<p class="muted small">{t('No goal: the plan defines them, you can also add some.')}</p>}>
        {(g) => <GoalRow tk={props.tk} g={g} apply={props.apply} />}
      </For>
      <div class="tk-goal-add">
        <input
          class="input small"
          placeholder={t('New goal')}
          value={title()}
          onInput={(e) => setTitle(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && void add()}
          data-testid="ticket-goal-input"
        />
        <input
          class="input small"
          placeholder={t('How to check it (optional)')}
          value={description()}
          onInput={(e) => setDescription(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && void add()}
          data-testid="ticket-goal-description"
        />
        <button class="btn small" disabled={!title().trim()} onClick={() => void add()}>
          {t('Add')}
        </button>
      </div>
    </div>
  )
}

/** A goal: its title and description, edited in place (double click). */
function GoalRow(props: { tk: Ticket; g: Goal; apply: Apply }) {
  const [editing, setEditing] = createSignal(false)
  const [title, setTitle] = createSignal('')
  const [description, setDescription] = createSignal('')
  const start = () => {
    setTitle(props.g.text)
    setDescription(props.g.description)
    setEditing(true)
  }
  const save = async () => {
    if (!title().trim()) return
    setEditing(false)
    await props.apply(goalOp(props.tk.id, { op: 'edit', id: props.g.id, text: title(), description: description() }))
  }
  return (
    <div class="tk-goal" classList={{ done: props.g.done }} data-testid="ticket-goal">
      <input type="checkbox" checked={props.g.done} onChange={(e) => props.apply(goalOp(props.tk.id, { op: 'check', id: props.g.id, done: e.currentTarget.checked }))} />
      <Show
        when={editing()}
        fallback={
          <div class="tk-goal-text" title={t('Double-click to edit')} onDblClick={start}>
            <div class="tk-goal-title">{props.g.text}</div>
            <Show when={props.g.description.trim()}>
              <div class="tk-goal-desc muted small" data-testid="ticket-goal-desc">
                {props.g.description}
              </div>
            </Show>
          </div>
        }
      >
        <div class="tk-goal-edit">
          <input class="input small" value={title()} onInput={(e) => setTitle(e.currentTarget.value)} onKeyDown={(e) => e.key === 'Escape' && (e.preventDefault(), setEditing(false))} ref={(el) => queueMicrotask(() => el.focus())} />
          <textarea class="tk-note-input" rows={2} value={description()} placeholder={t('How to check it (optional)')} onInput={(e) => setDescription(e.currentTarget.value)} />
          <div class="form-actions">
            <button class="btn small" onClick={() => setEditing(false)}>
              {t('Cancel')}
            </button>
            <button class="btn small primary" disabled={!title().trim()} onClick={() => void save()}>
              {t('Save')}
            </button>
          </div>
        </div>
      </Show>
      <span class="grow" />
      <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(goalOp(props.tk.id, { op: 'delete', id: props.g.id }))}>
        <Icon name="close" size={11} />
      </button>
    </div>
  )
}
