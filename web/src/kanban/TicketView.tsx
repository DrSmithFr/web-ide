// Detail of a ticket (tab of the editor): description, plan and goals, notes and test
// feedback, linked files, attachments, conversations, commits, history, and the buttons
// that move it through the workflow (docs/kanban.md).
import { createResource, createSignal, For, type JSX, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { contextMenu, fuzzy, pick, prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { Markdown } from '../llm/parts'
import { request } from '../pod/rpc'
import { basename, closeTab, leaves, openFile, relPath, root, type TabState } from '../state/project'
import {
  addAttachment, addNote, eventText, attachmentBlob, deleteAttachment, deleteNote, deleteTicket, ensureBoard, getTicket, goalOp, linkCommit, moveTicket,
  priorityLabels, roleLabels, statusLabels, ticketVersion, typeLabels, unlinkChat, unlinkCommit, updateTicket,
  type Priority, type Status, type Ticket, type TicketType,
} from './state'
import { abandonTicket, ticketActions, TicketChats, TicketGit } from './actions'
import { fmtAgo, fmtDate, fmtSize, t } from '../i18n'
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
  const [noteText, setNoteText] = createSignal('')
  let noteArea!: HTMLTextAreaElement
  const notes = () => tk().notes.filter((n) => n.kind !== 'event')
  const events = () => tk().notes.filter((n) => n.kind === 'event')
  const closed = () => tk().status === 'done' || tk().status === 'abandoned'

  const move = (s: Status, comment = '') => props.apply(moveTicket(tk().id, s, 'user', comment))
  const focusFeedback = () => {
    noteArea?.focus()
    noteArea?.scrollIntoView({ block: 'center' })
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
          <select class="small" value={tk().type} onChange={(e) => props.apply(updateTicket(tk().id, { type: e.currentTarget.value as TicketType }))} title={t('Type')}>
            <For each={Object.entries(typeLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
          </select>
          <select class="small" value={tk().priority} onChange={(e) => props.apply(updateTicket(tk().id, { priority: e.currentTarget.value as Priority }))} title={t('Priority')}>
            <For each={Object.entries(priorityLabels)}>{([v, l]) => <option value={v}>{t('{priority} priority', { priority: l })}</option>}</For>
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
          <Section title={t('Description')}>
            <EditableMarkdown value={tk().description} empty={t('No description.')} onSave={(v) => props.apply(updateTicket(tk().id, { description: v }))} testid="ticket-description" />
          </Section>

          <Section title={t('Implementation plan')}>
            <EditableMarkdown value={tk().plan} empty={t('No plan yet.')} onSave={(v) => props.apply(updateTicket(tk().id, { plan: v }))} testid="ticket-plan" />
          </Section>

          <Section title={`${t('Goals')} ${tk().goals ? `· ${tk().goalsDone}/${tk().goals}` : ''}`}>
            <Goals tk={tk()} apply={props.apply} />
          </Section>

          <Show when={tk().testSummary || ['review', 'fix', 'done'].includes(tk().status)}>
            <Section title={t('To test')}>
              <EditableMarkdown value={tk().testSummary} empty={t('No test summary.')} onSave={(v) => props.apply(updateTicket(tk().id, { testSummary: v }))} testid="ticket-test" />
            </Section>
          </Show>

          <TicketGit tk={tk()} apply={props.apply} />

          <Section title={t('Notes and feedback')}>
            <For each={notes()} fallback={<p class="muted small">{t('No note.')}</p>}>
              {(n) => (
                <div class="tk-note" classList={{ feedback: n.kind === 'feedback' }} data-testid="ticket-note">
                  <div class="tk-note-head">
                    <span class="badge" classList={{ warn: n.kind === 'feedback' }}>
                      {n.kind === 'feedback' ? t('Test feedback') : t('Note')}
                    </span>
                    <span class="muted small">
                      {n.author === 'model' ? t('Assistant') : t('You')} · {fmtDate(n.created)}
                    </span>
                    <span class="grow" />
                    <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(deleteNote(tk().id, n.id))}>
                      <Icon name="close" size={11} />
                    </button>
                  </div>
                  <Markdown text={n.text} final />
                </div>
              )}
            </For>
            <textarea
              ref={noteArea}
              class="tk-note-input"
              rows={3}
              placeholder={tk().status === 'review' ? t('Test feedback or note…') : t('Add context, a note…')}
              value={noteText()}
              onInput={(e) => setNoteText(e.currentTarget.value)}
              data-testid="ticket-note-input"
            />
            <div class="form-actions">
              <Show when={tk().status === 'review'}>
                <button
                  class="btn small primary"
                  disabled={!noteText().trim()}
                  onClick={async () => {
                    await props.apply(addNote(tk().id, 'feedback', noteText()))
                    setNoteText('')
                  }}
                  data-testid="ticket-feedback-add"
                >
                  {t('Send as feedback (→ Fix)')}
                </button>
              </Show>
              <button
                class="btn small"
                disabled={!noteText().trim()}
                onClick={async () => {
                  await props.apply(addNote(tk().id, 'note', noteText()))
                  setNoteText('')
                }}
                data-testid="ticket-note-add"
              >
                {t('Add a note')}
              </button>
            </div>
          </Section>

          <Section title={t('History ({n})', { n: events().length })} folded>
            <ul class="tk-events">
              <For each={events()}>
                {(n) => (
                  <li>
                    <span class="muted small">{fmtDate(n.created)}</span> · {eventText(n.text)}
                    <Show when={n.author === 'model'}>
                      <span class="badge">{t('assistant')}</span>
                    </Show>
                  </li>
                )}
              </For>
            </ul>
          </Section>
        </div>

        <aside class="tk-side">
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

          <TicketChats tk={tk()} apply={props.apply} onUnlink={(chatId) => props.apply(unlinkChat(tk().id, chatId))} roleLabels={roleLabels} />

          <Section title={t('Linked commits')}>
            <For each={tk().commits} fallback={<p class="muted small">{t('No commit.')}</p>}>
              {(c) => (
                <div class="tk-row">
                  <span class="mono small">{c.hash.slice(0, 8)}</span>
                  <span class="ellipsis small" title={c.subject}>
                    {c.subject}
                  </span>
                  <span class="grow" />
                  <button class="icon-btn small" title={t('Remove')} onClick={() => props.apply(unlinkCommit(tk().id, c.hash))}>
                    <Icon name="close" size={11} />
                  </button>
                </div>
              )}
            </For>
            <button class="btn small" onClick={() => void addCommit(tk(), props.apply)}>
              <Icon name="plus" size={12} /> {t('Link a commit')}
            </button>
          </Section>
        </aside>
      </div>
    </div>
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

async function addCommit(tk: Ticket, apply: Apply) {
  const log = await request<{ hash: string; short: string; subject: string; when: number }[]>('git.log', { n: 100 }).catch(() => [])
  const c = await pick({
    placeholder: t('Commit to link to the ticket'),
    items: log.map((c) => ({ label: c.subject, detail: `${c.short} · ${fmtAgo(c.when * 1000)}`, value: c })),
  })
  if (c) await apply(linkCommit(tk.id, c.hash, c.subject))
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
          if (e.key === 'Escape') setEditing(false)
        }}
      />
    </Show>
  )
}

export function EditableMarkdown(props: { value: string; empty: string; onSave: (v: string) => void | Promise<void>; testid?: string }) {
  const [editing, setEditing] = createSignal(false)
  const [draft, setDraft] = createSignal('')
  const start = () => {
    setDraft(props.value)
    setEditing(true)
  }
  const save = async () => {
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
            if (e.key === 'Escape') setEditing(false)
          }}
          ref={(el) => queueMicrotask(() => el.focus())}
        />
        <div class="form-actions">
          <span class="muted small">{t('Ctrl+Enter to save')}</span>
          <button class="btn small" onClick={() => setEditing(false)}>
            {t('Cancel')}
          </button>
          <button class="btn small primary" onClick={() => void save()} data-testid={props.testid && `${props.testid}-save`}>
            {t('Save')}
          </button>
        </div>
      </Show>
    </div>
  )
}

function Goals(props: { tk: Ticket; apply: Apply }) {
  const [text, setText] = createSignal('')
  const add = async () => {
    if (!text().trim()) return
    await props.apply(goalOp(props.tk.id, { op: 'add', text: text() }))
    setText('')
  }
  return (
    <div class="tk-goals">
      <For each={props.tk.goalList} fallback={<p class="muted small">{t('No goal: the plan defines them, you can also add some.')}</p>}>
        {(g) => (
          <div class="tk-goal" classList={{ done: g.done }} data-testid="ticket-goal">
            <input type="checkbox" checked={g.done} onChange={(e) => props.apply(goalOp(props.tk.id, { op: 'check', id: g.id, done: e.currentTarget.checked }))} />
            <span
              class="tk-goal-text"
              onDblClick={async () => {
                const v = await prompt({ title: t('Edit the goal'), value: g.text })
                if (v?.trim()) await props.apply(goalOp(props.tk.id, { op: 'edit', id: g.id, text: v }))
              }}
            >
              {g.text}
            </span>
            <Show when={g.source === 'feedback'}>
              <span class="badge warn">{t('feedback')}</span>
            </Show>
            <span class="grow" />
            <button class="icon-btn small" title={t('Delete')} onClick={() => props.apply(goalOp(props.tk.id, { op: 'delete', id: g.id }))}>
              <Icon name="close" size={11} />
            </button>
          </div>
        )}
      </For>
      <div class="tk-goal-add">
        <input class="input small" placeholder={t('New goal')} value={text()} onInput={(e) => setText(e.currentTarget.value)} onKeyDown={(e) => e.key === 'Enter' && void add()} data-testid="ticket-goal-input" />
        <button class="btn small" disabled={!text().trim()} onClick={() => void add()}>
          {t('Add')}
        </button>
      </div>
    </div>
  )
}
