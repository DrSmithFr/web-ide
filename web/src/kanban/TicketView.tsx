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
  addAttachment, addNote, attachmentBlob, deleteAttachment, deleteNote, deleteTicket, ensureBoard, getTicket, goalOp, linkCommit, moveTicket,
  priorityLabels, roleLabels, statusLabels, ticketVersion, typeLabels, unlinkChat, unlinkCommit, updateTicket,
  type Priority, type Status, type Ticket, type TicketType,
} from './state'
import { abandonTicket, ticketActions, TicketChats, TicketGit } from './actions'
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
    <Show when={ticket()} fallback={<p class="muted pad">Chargement du ticket…</p>}>
      <Show when={!ticket().error} fallback={<p class="danger pad">{ticket().error}</p>}>
        <TicketBody t={ticket() as Ticket} apply={apply} paneId={props.paneId} tabId={props.tab.id} />
      </Show>
    </Show>
  )
}

export type Apply = (p: Promise<Ticket>) => Promise<void>

function TicketBody(props: { t: Ticket; apply: Apply; paneId: string; tabId: string }) {
  const t = () => props.t
  const [noteText, setNoteText] = createSignal('')
  let noteArea!: HTMLTextAreaElement
  const notes = () => t().notes.filter((n) => n.kind !== 'event')
  const events = () => t().notes.filter((n) => n.kind === 'event')
  const closed = () => t().status === 'done' || t().status === 'abandoned'

  const move = (s: Status, comment = '') => props.apply(moveTicket(t().id, s, 'user', comment))
  const focusFeedback = () => {
    noteArea?.focus()
    noteArea?.scrollIntoView({ block: 'center' })
  }

  const remove = async () => {
    if (!confirm(`Supprimer définitivement le ticket #${t().id} ?`)) return
    try {
      await deleteTicket(t().id)
      for (const l of leaves()) if (l.tabs.includes(props.tabId)) closeTab(l.id, props.tabId, true)
    } catch (e) {
      errorToast(e)
    }
  }

  const buttons = () => ticketActions(t(), { move, apply: props.apply, focusFeedback })

  return (
    <div class="tk" data-testid="ticket-view" data-status={t().status}>
      <header class="tk-head">
        <div class="tk-title-row">
          <span class="tk-num">#{t().id}</span>
          <EditableText value={t().title} class="tk-title" onSave={(v) => props.apply(updateTicket(t().id, { title: v }))} testid="ticket-title" />
          <span class={`kb-status st-${t().status}`} data-testid="ticket-status">
            {statusLabels[t().status]}
          </span>
        </div>
        <div class="tk-meta-row">
          <select class="small" value={t().type} onChange={(e) => props.apply(updateTicket(t().id, { type: e.currentTarget.value as TicketType }))} title="Type">
            <For each={Object.entries(typeLabels)}>{([v, l]) => <option value={v}>{l}</option>}</For>
          </select>
          <select class="small" value={t().priority} onChange={(e) => props.apply(updateTicket(t().id, { priority: e.currentTarget.value as Priority }))} title="Priorité">
            <For each={Object.entries(priorityLabels)}>{([v, l]) => <option value={v}>Priorité {l.toLowerCase()}</option>}</For>
          </select>
          <span class="muted small">créé le {new Date(t().created).toLocaleString()}</span>
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
            title="Autres actions"
            onClick={(e) =>
              contextMenu(e, [
                ...(!closed() ? [{ label: 'Abandonner le ticket', action: () => void abandonTicket(t(), props.apply) }] : []),
                { label: 'Supprimer le ticket', action: () => void remove() },
              ])
            }
          >
            <Icon name="menu" size={14} />
          </button>
        </div>
      </header>

      <div class="tk-body">
        <div class="tk-main">
          <Section title="Description">
            <EditableMarkdown value={t().description} empty="Aucune description." onSave={(v) => props.apply(updateTicket(t().id, { description: v }))} testid="ticket-description" />
          </Section>

          <Section title="Plan d'implémentation">
            <EditableMarkdown value={t().plan} empty="Pas encore de plan." onSave={(v) => props.apply(updateTicket(t().id, { plan: v }))} testid="ticket-plan" />
          </Section>

          <Section title={`Goals ${t().goals ? `· ${t().goalsDone}/${t().goals}` : ''}`}>
            <Goals t={t()} apply={props.apply} />
          </Section>

          <Show when={t().testSummary || ['review', 'fix', 'done'].includes(t().status)}>
            <Section title="À tester">
              <EditableMarkdown value={t().testSummary} empty="Pas de résumé de test." onSave={(v) => props.apply(updateTicket(t().id, { testSummary: v }))} testid="ticket-test" />
            </Section>
          </Show>

          <TicketGit t={t()} apply={props.apply} />

          <Section title="Notes et retours">
            <For each={notes()} fallback={<p class="muted small">Aucune note.</p>}>
              {(n) => (
                <div class="tk-note" classList={{ feedback: n.kind === 'feedback' }} data-testid="ticket-note">
                  <div class="tk-note-head">
                    <span class="badge" classList={{ warn: n.kind === 'feedback' }}>
                      {n.kind === 'feedback' ? 'Retour de test' : 'Note'}
                    </span>
                    <span class="muted small">
                      {n.author === 'model' ? 'Assistant' : 'Vous'} · {new Date(n.created).toLocaleString()}
                    </span>
                    <span class="grow" />
                    <button class="icon-btn small" title="Supprimer" onClick={() => props.apply(deleteNote(t().id, n.id))}>
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
              placeholder={t().status === 'review' ? 'Retour de test ou note…' : 'Ajouter du contexte, une note…'}
              value={noteText()}
              onInput={(e) => setNoteText(e.currentTarget.value)}
              data-testid="ticket-note-input"
            />
            <div class="form-actions">
              <Show when={t().status === 'review'}>
                <button
                  class="btn small primary"
                  disabled={!noteText().trim()}
                  onClick={async () => {
                    await props.apply(addNote(t().id, 'feedback', noteText()))
                    setNoteText('')
                  }}
                  data-testid="ticket-feedback-add"
                >
                  Envoyer comme retour (→ Correction)
                </button>
              </Show>
              <button
                class="btn small"
                disabled={!noteText().trim()}
                onClick={async () => {
                  await props.apply(addNote(t().id, 'note', noteText()))
                  setNoteText('')
                }}
                data-testid="ticket-note-add"
              >
                Ajouter une note
              </button>
            </div>
          </Section>

          <Section title={`Historique (${events().length})`} folded>
            <ul class="tk-events">
              <For each={events()}>
                {(n) => (
                  <li>
                    <span class="muted small">{new Date(n.created).toLocaleString()}</span> · {n.text}
                    <Show when={n.author === 'model'}>
                      <span class="badge">assistant</span>
                    </Show>
                  </li>
                )}
              </For>
            </ul>
          </Section>
        </div>

        <aside class="tk-side">
          <Section title="Fichiers liés">
            <For each={t().files} fallback={<p class="muted small">Aucun fichier.</p>}>
              {(f) => (
                <div class="tk-row">
                  <Icon name="file" size={12} />
                  <button class="link ellipsis" title={f} onClick={() => openFile(absolute(f))}>
                    {f}
                  </button>
                  <span class="grow" />
                  <button class="icon-btn small" title="Retirer" onClick={() => props.apply(updateTicket(t().id, { removeFiles: [f] }))}>
                    <Icon name="close" size={11} />
                  </button>
                </div>
              )}
            </For>
            <button class="btn small" onClick={() => void addFile(t(), props.apply)} data-testid="ticket-file-add">
              <Icon name="plus" size={12} /> Ajouter un fichier
            </button>
          </Section>

          <Section title="Pièces jointes">
            <For each={t().attachments} fallback={<p class="muted small">Aucune pièce jointe.</p>}>
              {(a) => (
                <div class="tk-row">
                  <Icon name="paperclip" size={12} />
                  <button class="link ellipsis" title={a.name} onClick={() => void openAttachment(t().id, a.id)}>
                    {a.name}
                  </button>
                  <span class="muted small nowrap">{formatSize(a.size)}</span>
                  <span class="grow" />
                  <button class="icon-btn small" title="Supprimer" onClick={() => props.apply(deleteAttachment(t().id, a.id))}>
                    <Icon name="close" size={11} />
                  </button>
                </div>
              )}
            </For>
            <label class="btn small">
              <Icon name="plus" size={12} /> Joindre un fichier
              <input
                type="file"
                multiple
                hidden
                onChange={async (e) => {
                  const files = [...(e.currentTarget.files ?? [])]
                  e.currentTarget.value = ''
                  for (const f of files) await props.apply(addAttachment(t().id, f))
                }}
                data-testid="ticket-attach"
              />
            </label>
          </Section>

          <TicketChats t={t()} apply={props.apply} onUnlink={(chatId) => props.apply(unlinkChat(t().id, chatId))} roleLabels={roleLabels} />

          <Section title="Commits liés">
            <For each={t().commits} fallback={<p class="muted small">Aucun commit.</p>}>
              {(c) => (
                <div class="tk-row">
                  <span class="mono small">{c.hash.slice(0, 8)}</span>
                  <span class="ellipsis small" title={c.subject}>
                    {c.subject}
                  </span>
                  <span class="grow" />
                  <button class="icon-btn small" title="Retirer" onClick={() => props.apply(unlinkCommit(t().id, c.hash))}>
                    <Icon name="close" size={11} />
                  </button>
                </div>
              )}
            </For>
            <button class="btn small" onClick={() => void addCommit(t(), props.apply)}>
              <Icon name="plus" size={12} /> Lier un commit
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

function formatSize(n: number) {
  if (n < 1024) return `${n} o`
  if (n < 1 << 20) return `${(n / 1024).toFixed(0)} Ko`
  return `${(n / (1 << 20)).toFixed(1)} Mo`
}

let fileCache: { at: number; files: string[] } | null = null
async function addFile(t: Ticket, apply: Apply) {
  const files = async () => {
    if (!fileCache || Date.now() - fileCache.at > 15000) fileCache = { at: Date.now(), files: await request<string[]>('search.files') }
    return fileCache.files
  }
  const p = await pick<string>({
    placeholder: 'Fichier à lier au ticket',
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
  if (p) await apply(updateTicket(t.id, { addFiles: [p] }))
}

async function addCommit(t: Ticket, apply: Apply) {
  const log = await request<{ hash: string; short: string; subject: string; when: string }[]>('git.log', { n: 100 }).catch(() => [])
  const c = await pick({
    placeholder: 'Commit à lier au ticket',
    items: log.map((c) => ({ label: c.subject, detail: `${c.short} · ${c.when}`, value: c })),
  })
  if (c) await apply(linkCommit(t.id, c.hash, c.subject))
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
        <h1 class={props.class} title="Cliquer pour modifier" onClick={() => setEditing(true)} data-testid={props.testid}>
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
              <Icon name="edit" size={12} /> Modifier
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
          <span class="muted small">Ctrl+Entrée pour enregistrer</span>
          <button class="btn small" onClick={() => setEditing(false)}>
            Annuler
          </button>
          <button class="btn small primary" onClick={() => void save()} data-testid={props.testid && `${props.testid}-save`}>
            Enregistrer
          </button>
        </div>
      </Show>
    </div>
  )
}

function Goals(props: { t: Ticket; apply: Apply }) {
  const [text, setText] = createSignal('')
  const add = async () => {
    if (!text().trim()) return
    await props.apply(goalOp(props.t.id, { op: 'add', text: text() }))
    setText('')
  }
  return (
    <div class="tk-goals">
      <For each={props.t.goalList} fallback={<p class="muted small">Aucun goal : le plan les définit, vous pouvez aussi en ajouter.</p>}>
        {(g) => (
          <div class="tk-goal" classList={{ done: g.done }} data-testid="ticket-goal">
            <input type="checkbox" checked={g.done} onChange={(e) => props.apply(goalOp(props.t.id, { op: 'check', id: g.id, done: e.currentTarget.checked }))} />
            <span
              class="tk-goal-text"
              onDblClick={async () => {
                const v = await prompt({ title: 'Modifier le goal', value: g.text })
                if (v?.trim()) await props.apply(goalOp(props.t.id, { op: 'edit', id: g.id, text: v }))
              }}
            >
              {g.text}
            </span>
            <Show when={g.source === 'feedback'}>
              <span class="badge warn">retour</span>
            </Show>
            <span class="grow" />
            <button class="icon-btn small" title="Supprimer" onClick={() => props.apply(goalOp(props.t.id, { op: 'delete', id: g.id }))}>
              <Icon name="close" size={11} />
            </button>
          </div>
        )}
      </For>
      <div class="tk-goal-add">
        <input class="input small" placeholder="Nouveau goal" value={text()} onInput={(e) => setText(e.currentTarget.value)} onKeyDown={(e) => e.key === 'Enter' && void add()} data-testid="ticket-goal-input" />
        <button class="btn small" disabled={!text().trim()} onClick={() => void add()}>
          Ajouter
        </button>
      </div>
    </div>
  )
}
