// Workflow of a ticket: the buttons of each status, its linked conversations and its git
// state (branch, worktree, changes against the base).
import { createResource, createSignal, For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { request } from '../pod/rpc'
import { openFile, project, root } from '../state/project'
import {
  abortGit, continueGit, filePatch, finishTicket, gitInfo, mergeTicket, openWorktreeWindow, rebaseTicket, ticketDiff, ticketVersion, updateTicket, worktreeProject,
  type ChatRole, type Diff, type GitInfo, type GitOpState, type Status, type Ticket,
} from './state'
import { openTicketChat, openWorktree, startTicketChat, startWorkSession } from './sessions'
import { Section, type Apply } from './TicketView'

export interface ActionButton {
  label: string
  run: () => void
  primary?: boolean
  danger?: boolean
  disabled?: boolean
  title?: string
  testid?: string
}

interface Ctx {
  move: (s: Status, comment?: string) => Promise<void>
  apply: Apply
  focusFeedback: () => void
}

/** Buttons of the header of a ticket for its status. */
export function ticketActions(t: Ticket, ctx: Ctx): ActionButton[] {
  const here = project()?.ticket === t.id
  const worktree: ActionButton[] = t.worktree && !here ? [{ label: 'Ouvrir le worktree', run: () => void openWorktree(t), testid: 'ticket-open-worktree' }] : []
  switch (t.status) {
    case 'new':
      return [
        { label: 'Briefing', run: () => void startTicketChat(t, 'briefing'), title: 'Conversation (mode Plan) pour préciser le ticket', testid: 'ticket-briefing' },
        {
          label: t.plan.trim() ? 'Refaire le plan' : 'Générer le plan',
          primary: !t.plan.trim(),
          run: () => void startTicketChat(t, 'plan'),
          title: 'Le modèle écrit le plan et les goals, puis passe le ticket à développer',
          testid: 'ticket-plan-generate',
        },
        {
          label: 'Passer à développer',
          primary: !!t.plan.trim(),
          disabled: !t.plan.trim() && !t.goals,
          title: !t.plan.trim() && !t.goals ? 'Il faut d’abord un plan ou des goals' : undefined,
          run: () => void ctx.move('ready'),
          testid: 'ticket-to-ready',
        },
      ]
    case 'ready':
      return [
        { label: 'Revenir à « Nouveau »', run: () => void ctx.move('new') },
        {
          label: 'Commencer le développement',
          primary: true,
          title: 'Crée la branche et le worktree du ticket, puis lance une conversation de développement dans sa fenêtre',
          run: () => void startWorkSession(t, 'dev'),
          testid: 'ticket-start',
        },
      ]
    case 'in_progress':
    case 'fix':
      return [
        ...worktree,
        {
          label: t.status === 'fix' ? 'Session de correction' : 'Nouvelle session de dev',
          run: () => void startWorkSession(t, t.status === 'fix' ? 'correction' : 'dev'),
          testid: 'ticket-session',
        },
        { label: 'Envoyer en test', primary: true, run: () => void ctx.move('review'), testid: 'ticket-to-review' },
      ]
    case 'review':
      return [
        ...worktree,
        { label: 'Ajouter un retour', run: ctx.focusFeedback, testid: 'ticket-feedback' },
        { label: 'Fermer le ticket', primary: true, run: () => void closeTicket(t, ctx.apply), testid: 'ticket-close' },
      ]
    case 'done':
      return [{ label: 'Rouvrir (→ Correction)', run: () => void ctx.move('fix'), testid: 'ticket-reopen' }]
    case 'abandoned':
      return [{ label: 'Rouvrir', run: () => void ctx.move('new'), testid: 'ticket-reopen' }]
  }
}

async function closeTicket(t: Ticket, apply: Apply) {
  if (t.worktree) {
    const d = await ticketDiff(t.id).catch(() => null)
    if (d?.dirty && !confirm('Le worktree a des modifications non commitées : elles seront perdues avec lui. Fermer quand même ?')) return
  }
  await apply(finishTicket(t.id, 'done'))
}

export async function abandonTicket(t: Ticket, apply: Apply) {
  const why = await prompt({ title: `Abandonner le ticket #${t.id}`, label: 'Raison (facultatif)' })
  if (why === null) return
  const deleteBranch = !!t.branch && confirm(`Supprimer aussi la branche ${t.branch} ?`)
  await apply(finishTicket(t.id, 'abandoned', why, deleteBranch))
}

/** Opens a conversation of a ticket: development ones in the window of the worktree. */
async function openChatOf(t: Ticket, chatId: string, role: ChatRole) {
  const inWorktree = role === 'dev' || role === 'correction' || role === 'resolve'
  if (inWorktree && t.worktree && project()?.ticket !== t.id) {
    try {
      const target = (await worktreeProject(t.id)).project
      localStorage.setItem(`webide.llm.active.${target}`, chatId)
      openWorktreeWindow(target, true)
    } catch (e) {
      errorToast(e)
    }
    return
  }
  await openTicketChat(chatId)
}

export function TicketChats(props: { t: Ticket; apply: Apply; onUnlink: (chatId: string) => void; roleLabels: Record<ChatRole, string> }) {
  return (
    <Section title="Conversations">
      <For each={props.t.chatList} fallback={<p class="muted small">Aucune conversation liée.</p>}>
        {(c) => (
          <div class="tk-row">
            <Icon name="sparkle" size={12} />
            <span class={`kb-role r-${c.role}`}>{props.roleLabels[c.role]}</span>
            <button class="link ellipsis small" title={c.title} onClick={() => void openChatOf(props.t, c.chatId, c.role)} data-testid="ticket-chat">
              {c.title || 'Conversation'}
            </button>
            <span class="grow" />
            <button class="icon-btn small" title="Délier" onClick={() => props.onUnlink(c.chatId)}>
              <Icon name="close" size={11} />
            </button>
          </div>
        )}
      </For>
    </Section>
  )
}

const statusNames: Record<string, string> = { A: 'ajouté', M: 'modifié', D: 'supprimé', R: 'renommé', C: 'copié', T: 'type changé', '?': 'non suivi' }

/** Branch, base, worktree and the files changed by the ticket, with their diff. */
export function TicketGit(props: { t: Ticket; apply: Apply }) {
  const t = () => props.t
  const shown = () => !!t().branch || !!t().snapshot || ['in_progress', 'review', 'fix', 'done'].includes(t().status)
  const [tick, setTick] = createSignal(0)
  const [diff] = createResource(
    () => (shown() && (t().branch || t().snapshot) ? { id: t().id, v: ticketVersion(t().id), k: tick(), base: t().base } : null),
    async ({ id }) => {
      try {
        return { d: await ticketDiff(id), error: '' }
      } catch (e) {
        return { d: null as Diff | null, error: (e as Error).message }
      }
    },
  )
  const changeBase = async () => {
    const branches = await request<{ name: string; current: boolean }[]>('git.branches').catch(() => [])
    const v = await prompt({ title: 'Base de comparaison', label: `Branches : ${branches.map((b) => b.name).slice(0, 12).join(', ')}`, value: t().base || 'origin/main' })
    if (v?.trim()) await props.apply(updateTicket(t().id, { base: v.trim() }))
  }
  const totals = () => (diff()?.d?.files ?? []).reduce((a, f) => [a[0] + f.added, a[1] + f.removed], [0, 0])
  return (
    <Show when={shown()}>
      <Section
        title="Git et changements"
        actions={
          <button class="icon-btn small" title="Rafraîchir" onClick={() => setTick((n) => n + 1)}>
            <Icon name="refresh" size={12} />
          </button>
        }
      >
        <div class="tk-git" data-testid="ticket-git">
          <Show when={t().branch} fallback={<p class="muted small">Pas encore de branche : elle est créée au début du développement.</p>}>
            <div class="tk-git-row">
              <Icon name="branch" size={12} />
              <span class="mono" data-testid="ticket-branch">
                {t().branch}
              </span>
              <span class="muted small">comparée à</span>
              <button class="link mono" title="Changer la base de comparaison" onClick={() => void changeBase()} data-testid="ticket-base">
                {t().base || diff()?.d?.base || 'origin/main'}
              </button>
            </div>
            <Show when={t().worktree}>
              <div class="tk-git-row muted small">
                <Icon name="folder" size={12} />
                <span class="mono ellipsis" title={t().worktree}>
                  {t().worktree!.replace(root() + '/', '')}
                </span>
                <Show when={project()?.ticket !== t().id}>
                  <button class="link" onClick={() => void openWorktree(t())}>
                    ouvrir
                  </button>
                </Show>
              </div>
            </Show>
            <Show when={t().setup}>
              <details class="tk-setup" classList={{ error: t().setup === 'error' }}>
                <summary>
                  {t().setup === 'running' ? 'Initialisation du worktree en cours…' : t().setup === 'ok' ? 'Worktree initialisé' : 'Initialisation du worktree en échec'}
                </summary>
                <pre>{t().setupLog || '(pas de sortie)'}</pre>
              </details>
            </Show>
          </Show>
          <Show when={diff()?.error}>
            <p class="warn small">{diff()!.error}</p>
          </Show>
          <Show when={diff()?.d}>
            {(d) => (
              <>
                <div class="tk-git-summary small" data-testid="ticket-diff-summary">
                  <strong>{d().files.length} fichier(s)</strong>
                  <span class="ok">+{totals()[0]}</span>
                  <span class="danger">−{totals()[1]}</span>
                  <Show when={d().source !== 'snapshot'}>
                    <span class="muted">
                      · {d().ahead} commit(s) d'avance{d().behind ? `, ${d().behind} de retard sur ${d().base}` : ''}
                    </span>
                  </Show>
                  <Show when={d().dirty}>
                    <span class="badge warn">modifications non commitées</span>
                  </Show>
                  <Show when={d().source === 'snapshot'}>
                    <span class="badge">{t().status === 'done' ? 'figé à la fermeture' : 'figé à la fusion'}</span>
                  </Show>
                </div>
                <div class="tk-files">
                  <For each={d().files}>{(f) => <ChangedFile t={t()} d={d()} f={f} />}</For>
                </div>
              </>
            )}
          </Show>
          <Show when={t().branch}>
            <GitOps t={t()} tick={tick()} behind={diff()?.d?.behind ?? 0} onDone={() => setTick((n) => n + 1)} />
          </Show>
        </div>
      </Section>
    </Show>
  )
}

function ChangedFile(props: { t: Ticket; d: Diff; f: { path: string; status: string; added: number; removed: number } }) {
  const [open, setOpen] = createSignal(false)
  const [patch] = createResource(
    () => (open() ? { v: ticketVersion(props.t.id), from: props.d.from } : null),
    ({ from }) => filePatch(props.t.id, props.f.path, from, props.d.source).catch((e) => `Erreur : ${(e as Error).message}`),
  )
  const here = () => project()?.ticket === props.t.id
  return (
    <div class="tk-file" data-testid="ticket-diff-file">
      <div class="tk-file-head" onClick={() => setOpen(!open())}>
        <span class="tk-chev" classList={{ open: open() }}>
          <Icon name="chevron" size={11} />
        </span>
        <span class={`tk-fst s-${props.f.status === '?' ? 'U' : props.f.status}`} title={statusNames[props.f.status] ?? props.f.status}>
          {props.f.status === '?' ? 'U' : props.f.status}
        </span>
        <span class="mono ellipsis" title={props.f.path}>
          {props.f.path}
        </span>
        <span class="grow" />
        <span class="ok small">+{props.f.added}</span>
        <span class="danger small">−{props.f.removed}</span>
        <Show when={here() && props.f.status !== 'D'}>
          <button
            class="icon-btn small"
            title="Ouvrir le fichier"
            onClick={(e) => {
              e.stopPropagation()
              openFile(`${root()}/${props.f.path}`)
            }}
          >
            <Icon name="external" size={11} />
          </button>
        </Show>
      </div>
      <Show when={open()}>
        <Show when={patch() !== undefined} fallback={<p class="muted small pad">Chargement…</p>}>
          <PatchView text={patch()!} />
        </Show>
      </Show>
    </div>
  )
}

/** Unified diff of one file, headers left out. */
export function PatchView(props: { text: string }) {
  const lines = () => {
    const out: { cls: string; text: string }[] = []
    let body = false
    for (const l of props.text.split('\n')) {
      if (l.startsWith('@@')) body = true
      if (!body) continue
      out.push({ cls: l.startsWith('@@') ? 'hunk' : l[0] === '+' ? 'add' : l[0] === '-' ? 'del' : '', text: l })
    }
    if (out.length && out[out.length - 1].text === '') out.pop()
    return out
  }
  return (
    <Show when={lines().length} fallback={<p class="muted small pad">{props.text.startsWith('Erreur') ? props.text : 'Pas de différence textuelle (fichier binaire ou mode).'}</p>}>
      <pre class="tk-patch">
        <For each={lines()}>{(l) => <div class={l.cls}>{l.text || ' '}</div>}</For>
      </pre>
    </Show>
  )
}

/** Merge into the base, rebase on it, and the conflicts they leave (docs/kanban.md). */
function GitOps(props: { t: Ticket; tick: number; behind: number; onDone: () => void }) {
  const t = () => props.t
  const [squash, setSquash] = createSignal(false)
  const [busy, setBusy] = createSignal('')
  const [info, { mutate }] = createResource(
    () => ({ id: t().id, v: ticketVersion(t().id), k: props.tick }),
    ({ id }) => gitInfo(id).catch(() => null),
  )
  const run = async (label: string, f: () => Promise<GitInfo>) => {
    setBusy(label)
    try {
      mutate(await f())
      props.onDone()
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy('')
    }
  }
  const merge = async () => {
    const d = await ticketDiff(t().id).catch(() => null)
    if (d?.dirty && !confirm('Le worktree a des modifications non commitées : elles ne seront pas fusionnées. Continuer ?')) return
    await run('merge', () => mergeTicket(t().id, squash()))
  }
  const busyState = (s?: GitOpState) => !!s && (s.rebase || s.merge || s.squash)
  return (
    <Show when={info()}>
      {(i) => (
        <div class="tk-gitops" data-testid="ticket-gitops">
          <Show when={busyState(i().worktree)}>
            <Conflicts t={t()} where="worktree" state={i().worktree!} busy={busy()} run={run} />
          </Show>
          <Show when={busyState(i().main)}>
            <Conflicts t={t()} where="main" state={i().main} busy={busy()} run={run} />
          </Show>
          <div class="tk-git-row">
            <Show
              when={!i().merged}
              fallback={
                <span class="badge ok" data-testid="ticket-merged">
                  <Icon name="check" size={11} /> fusionnée dans {i().into}
                </span>
              }
            >
              <select class="small" value={squash() ? 'squash' : 'merge'} onChange={(e) => setSquash(e.currentTarget.value === 'squash')} title="Mode de fusion">
                <option value="merge">merge --no-ff</option>
                <option value="squash">squash</option>
              </select>
              <button class="btn small" disabled={!!busy() || busyState(i().main)} onClick={() => void merge()} data-testid="ticket-merge">
                <Icon name="branch" size={12} /> {busy() === 'merge' ? 'Fusion…' : `Fusionner dans ${i().into}`}
              </button>
            </Show>
            <Show when={i().worktree && !busyState(i().worktree) && !i().merged}>
              <button
                class="btn small"
                disabled={!!busy()}
                title="git fetch puis rebase de la branche sur sa base, dans le worktree"
                onClick={() => void run('rebase', () => rebaseTicket(t().id))}
                data-testid="ticket-rebase"
              >
                <Icon name="refresh" size={12} /> {busy() === 'rebase' ? 'Rebase…' : `Rebaser${props.behind ? ` (${props.behind} de retard)` : ''}`}
              </button>
            </Show>
          </div>
        </div>
      )}
    </Show>
  )
}

function Conflicts(props: {
  t: Ticket
  where: 'worktree' | 'main'
  state: GitOpState
  busy: string
  run: (label: string, f: () => Promise<GitInfo>) => Promise<void>
}) {
  const what = () => (props.where === 'worktree' ? 'Rebase en cours dans le worktree' : props.state.squash ? 'Fusion (squash) en cours dans le dossier principal' : 'Fusion en cours dans le dossier principal')
  const here = () => (props.where === 'worktree' ? project()?.ticket === props.t.id : !project()?.parent)
  const resolve = () => (props.where === 'worktree' ? startWorkSession(props.t, 'resolve') : startTicketChat(props.t, 'resolve'))
  return (
    <div class="tk-conflicts" data-testid={`ticket-conflicts-${props.where}`}>
      <div class="tk-git-row">
        <Icon name="conflict" size={13} />
        <strong>{what()}</strong>
        <span class="muted small">
          {props.state.conflicts.length ? `${props.state.conflicts.length} fichier(s) en conflit` : 'conflits résolus : continuer pour terminer'}
        </span>
      </div>
      <For each={props.state.conflicts}>
        {(f) => (
          <div class="tk-row mono small">
            <Show when={here()} fallback={<span class="ellipsis">{f}</span>}>
              <button class="link ellipsis" onClick={() => openFile(`${root()}/${f}`)}>
                {f}
              </button>
            </Show>
          </div>
        )}
      </For>
      <div class="tk-git-row">
        <button class="btn small primary" disabled={!!props.busy} onClick={() => void props.run('continue', () => continueGit(props.t.id, props.where))} data-testid="ticket-continue">
          Continuer
        </button>
        <button class="btn small" disabled={!!props.busy} onClick={() => void props.run('abort', () => abortGit(props.t.id, props.where))} data-testid="ticket-abort">
          {props.where === 'worktree' ? 'Abandonner le rebase' : 'Annuler la fusion'}
        </button>
        <Show when={props.state.conflicts.length}>
          <button class="btn small" onClick={() => void resolve()} data-testid="ticket-resolve">
            <Icon name="sparkle" size={12} /> Session de résolution
          </button>
        </Show>
      </div>
    </div>
  )
}
