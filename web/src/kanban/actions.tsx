// Workflow of a ticket: the buttons of each status, its linked conversations and its git
// state (branch, worktree, changes against the base).
import { createResource, createSignal, For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { request } from '../pod/rpc'
import { openFile, project, root, showWorktree } from '../state/project'
import {
  abortGit, continueGit, filePatch, finishTicket, gitInfo, inWorktreeOf, mergeTicket, openPR, rebaseTicket, roleLabels, ticketDiff, ticketVersion, unlinkChat, updateTicket,
  validateStep, worktreeProject, type ChatRole, type Diff, type GitInfo, type GitOpState, type Status, type Ticket,
} from './state'
import { openTicketChat, openWorktree, startTicketChat, startWorkSession } from './sessions'
import { blockerText } from './Lineage'
import { Section, type Apply } from './TicketView'
import { t, tn } from '../i18n'

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
export function ticketActions(tk: Ticket, ctx: Ctx): ActionButton[] {
  const here = inWorktreeOf(tk)
  const blocked = tk.blockers?.length ? tk.blockers.map(blockerText).join(', ') : ''
  const openChildren = (tk.children ?? []).filter((c) => c.status !== 'done' && c.status !== 'abandoned')
  const worktree: ActionButton[] = tk.worktree && !here ? [{ label: t('Open the worktree'), run: () => void openWorktree(tk), testid: 'ticket-open-worktree' }] : []
  switch (tk.status) {
    case 'new':
      return [
        { label: 'Briefing', run: () => void startTicketChat(tk, 'briefing'), title: t('Conversation (Plan mode) to clarify the ticket'), testid: 'ticket-briefing' },
        {
          label: t('Generate the plan'),
          primary: true,
          run: () => void startTicketChat(tk, 'plan'),
          title: t('The model writes the plan and the goals; the ticket then moves to “To do”'),
          testid: 'ticket-plan-generate',
        },
      ]
    case 'todo':
      return [
        { label: t('Back to “New”'), run: () => void ctx.move('new'), testid: 'ticket-to-new' },
        { label: t('Redo the plan'), run: () => void startTicketChat(tk, 'plan'), testid: 'ticket-plan-generate' },
        {
          label: t('Start development'),
          primary: true,
          disabled: !!blocked,
          title: blocked
            ? t('Cannot start yet: {blockers}', { blockers: blocked })
            : tk.parent
              ? t('Starts this step in the worktree of #{id}, then a development conversation in its window', { id: tk.parent })
              : t('Creates the branch and the worktree of the ticket, then starts a development conversation in its window'),
          run: () => void startWorkSession(tk, 'dev'),
          testid: 'ticket-start',
        },
        ...(blocked
          ? [
              {
                label: t('Start anyway…'),
                title: t('Cannot start yet: {blockers}', { blockers: blocked }),
                run: () => {
                  if (confirm(t('Ticket #{id} waits for {blockers}. Start it anyway?', { id: tk.id, blockers: blocked }))) void startWorkSession(tk, 'dev', undefined, true)
                },
                testid: 'ticket-start-force',
              },
            ]
          : []),
      ]
    case 'in_progress':
      return [
        ...worktree,
        { label: t('New dev session'), run: () => void startWorkSession(tk, 'dev'), testid: 'ticket-session' },
        { label: t('Send to testing'), primary: true, run: () => void ctx.move('review'), testid: 'ticket-to-review' },
      ]
    case 'review':
      return [
        ...worktree,
        { label: t('Back to “In progress”'), run: () => void ctx.move('in_progress'), testid: 'ticket-to-progress' },
        { label: t('Add feedback'), run: ctx.focusFeedback, testid: 'ticket-feedback' },
        ...(openChildren.length && !tk.stepDone
          ? [
              {
                label: t('Validate the step'),
                primary: true,
                title: t('Its work is tested: the next step of the lineage (#{id}) may start in this worktree', { id: openChildren[0].id }),
                run: () => void ctx.apply(validateStep(tk.id)),
                testid: 'ticket-step',
              },
            ]
          : []),
        {
          label: t('Close the ticket'),
          primary: !openChildren.length,
          disabled: !!openChildren.length,
          title: openChildren.length ? t('The lineage is not finished: {ids}', { ids: openChildren.map((c) => `#${c.id}`).join(', ') }) : undefined,
          run: () => void closeTicket(tk, ctx.apply),
          testid: 'ticket-close',
        },
      ]
    case 'done':
      return [{ label: t('Reopen (→ To test)'), run: () => void ctx.move('review'), testid: 'ticket-reopen' }]
    case 'abandoned':
      return [{ label: t('Reopen'), run: () => void ctx.move('new'), testid: 'ticket-reopen' }]
  }
}

async function closeTicket(tk: Ticket, apply: Apply) {
  if (tk.worktree) {
    const d = await ticketDiff(tk.id).catch(() => null)
    if (d?.dirty && !confirm(t('The worktree has uncommitted changes: they will be lost with it. Close anyway?'))) return
  }
  await apply(finishTicket(tk.id, 'done'))
}

export async function abandonTicket(tk: Ticket, apply: Apply) {
  const open = (tk.children ?? []).filter((c) => c.status !== 'done' && c.status !== 'abandoned')
  if (open.length && !confirm(t('Its steps {ids} are abandoned with it. Continue?', { ids: open.map((c) => `#${c.id}`).join(', ') }))) return
  if (tk.parent && tk.branch && !confirm(t('Its commits stay on the branch of the lineage (#{id}): revert them there if needed. Continue?', { id: tk.parent }))) return
  const why = await prompt({ title: t('Abandon ticket #{id}', { id: tk.id }), label: t('Reason (optional)') })
  if (why === null) return
  // The branch of a lineage belongs to its root.
  const deleteBranch = !tk.parent && !!tk.branch && confirm(t('Also delete the branch {branch}?', { branch: tk.branch }))
  await apply(finishTicket(tk.id, 'abandoned', why, deleteBranch, open.length > 0))
}

/** Opens a conversation of a ticket: development ones with the worktree of the ticket shown. */
async function openChatOf(tk: Ticket, chatId: string, role: ChatRole) {
  const inWorktree = role === 'dev' || role === 'correction' || role === 'resolve'
  if (inWorktree && tk.worktree && !inWorktreeOf(tk)) {
    try {
      await showWorktree((await worktreeProject(tk.id)).project)
    } catch (e) {
      errorToast(e)
      return
    }
  }
  await openTicketChat(chatId)
}

/** Conversations of some roles linked to a ticket (exclude: ids shown elsewhere). */
export function TicketChats(props: { tk: Ticket; roles: ChatRole[]; title: string; hideEmpty?: boolean; exclude?: Set<string | undefined>; folded?: boolean }) {
  const list = () => props.tk.chatList.filter((c) => props.roles.includes(c.role) && !props.exclude?.has(c.chatId))
  return (
    <Show when={!props.hideEmpty || list().length}>
      <Section title={props.title} folded={props.folded}>
        <For each={list()} fallback={<p class="muted small">{t('No linked conversation.')}</p>}>
          {(c) => (
            <div class="tk-row">
              <Icon name="sparkle" size={12} />
              <span class={`kb-role r-${c.role}`}>{roleLabels[c.role]}</span>
              <button class="link ellipsis small" title={c.title} onClick={() => void openChatOf(props.tk, c.chatId, c.role)} data-testid="ticket-chat">
                {c.title || t('Conversation')}
              </button>
              <span class="grow" />
              <button class="icon-btn small" title={t('Unlink')} onClick={() => void unlinkChat(props.tk.id, c.chatId).catch(errorToast)}>
                <Icon name="close" size={11} />
              </button>
            </div>
          )}
        </For>
      </Section>
    </Show>
  )
}

/** Link to a conversation of the ticket (from a note or a feedback). */
export function ChatLink(props: { tk: Ticket; chatId: string }) {
  const c = () => props.tk.chatList.find((x) => x.chatId === props.chatId)
  return (
    <button class="link small ellipsis tk-chat-link" title={c()?.title} onClick={() => void openChatOf(props.tk, props.chatId, c()?.role ?? 'briefing')} data-testid="ticket-chat-link">
      <Icon name="sparkle" size={11} /> {c()?.title || t('Conversation')}
    </button>
  )
}

const statusNames: Record<string, string> = { A: 'added', M: 'modified', D: 'deleted', R: 'renamed', C: 'copied', T: 'type changed', '?': 'untracked' }

/** Branch, base, worktree and the files changed by the ticket, with their diff. */
export function TicketGit(props: { tk: Ticket; apply: Apply; folded?: boolean }) {
  const tk = () => props.tk
  const [tick, setTick] = createSignal(0)
  const [diff] = createResource(
    () => (tk().branch || tk().snapshot ? { id: tk().id, v: ticketVersion(tk().id), k: tick(), base: tk().base } : null),
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
    const v = await prompt({ title: t('Comparison base'), label: `Branches : ${branches.map((b) => b.name).slice(0, 12).join(', ')}`, value: tk().base || 'origin/main' })
    if (v?.trim()) await props.apply(updateTicket(tk().id, { base: v.trim() }))
  }
  const totals = () => (diff()?.d?.files ?? []).reduce((a, f) => [a[0] + f.added, a[1] + f.removed], [0, 0])
  return (
    <>
      <Section
        title={t('Git and changes')}
        folded={props.folded}
        actions={
          <button class="icon-btn small" title={t('Refresh')} onClick={() => setTick((n) => n + 1)}>
            <Icon name="refresh" size={12} />
          </button>
        }
      >
        <div class="tk-git" data-testid="ticket-git">
          <Show when={tk().branch} fallback={<p class="muted small">{t('No branch yet: it is created when development starts.')}</p>}>
            <div class="tk-git-row">
              <Icon name="branch" size={12} />
              <span class="mono" data-testid="ticket-branch">
                {tk().branch}
              </span>
              <span class="muted small">{t('compared with')}</span>
              <button class="link mono" title={t('Change the comparison base')} onClick={() => void changeBase()} data-testid="ticket-base">
                {tk().base || diff()?.d?.base || 'origin/main'}
              </button>
            </div>
            <Show when={tk().worktree}>
              <div class="tk-git-row muted small">
                <Icon name="folder" size={12} />
                <span class="mono ellipsis" title={tk().worktree}>
                  {tk().worktree!.replace(root() + '/', '')}
                </span>
                <Show when={!inWorktreeOf(tk())}>
                  <button class="link" onClick={() => void openWorktree(tk())}>
                    {t('open')}
                  </button>
                </Show>
              </div>
            </Show>
            <Show when={tk().setup}>
              <details class="tk-setup" classList={{ error: tk().setup === 'error' }}>
                <summary>
                  {tk().setup === 'running' ? t('Setting up the worktree…') : tk().setup === 'ok' ? t('Worktree set up') : t('Worktree setup failed')}
                </summary>
                <pre>{tk().setupLog || t('(no output)')}</pre>
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
                  <strong>{tn(d().files.length, '{n} file', '{n} files')}</strong>
                  <span class="ok">+{totals()[0]}</span>
                  <span class="danger">−{totals()[1]}</span>
                  <Show when={d().source !== 'snapshot'}>
                    <span class="muted">
                      · {tn(d().ahead, '{n} commit ahead', '{n} commits ahead')}
                      {d().behind ? `, ${tn(d().behind, '{n} behind {base}', '{n} behind {base}', { base: d().base })}` : ''}
                    </span>
                  </Show>
                  <Show when={d().dirty}>
                    <span class="badge warn">{t('uncommitted changes')}</span>
                  </Show>
                  <Show when={d().source === 'snapshot'}>
                    <span class="badge">{tk().status === 'done' ? t('frozen when closed') : t('frozen when merged')}</span>
                  </Show>
                </div>
                <div class="tk-files">
                  <For each={d().files}>{(f) => <ChangedFile tk={tk()} d={d()} f={f} />}</For>
                </div>
              </>
            )}
          </Show>
          <Show when={tk().branch && !tk().parent}>
            <GitOps tk={tk()} tick={tick()} behind={diff()?.d?.behind ?? 0} onDone={() => setTick((n) => n + 1)} />
          </Show>
          <Show when={tk().parent && tk().branch}>
            <p class="muted small" data-testid="ticket-lineage-git">
              {t('A step of the lineage of #{id}: merged with it, from its ticket.', { id: tk().parent! })}
            </p>
          </Show>
        </div>
      </Section>
    </>
  )
}

function ChangedFile(props: { tk: Ticket; d: Diff; f: { path: string; status: string; added: number; removed: number } }) {
  const [open, setOpen] = createSignal(false)
  const [patch] = createResource(
    () => (open() ? { v: ticketVersion(props.tk.id), from: props.d.from } : null),
    ({ from }) => filePatch(props.tk.id, props.f.path, from, props.d.source).catch((e) => t('Error: {message}', { message: (e as Error).message })),
  )
  const here = () => inWorktreeOf(props.tk)
  return (
    <div class="tk-file" data-testid="ticket-diff-file">
      <div class="tk-file-head" onClick={() => setOpen(!open())}>
        <span class="tk-chev" classList={{ open: open() }}>
          <Icon name="chevron" size={11} />
        </span>
        <span class={`tk-fst s-${props.f.status === '?' ? 'U' : props.f.status}`} title={t(statusNames[props.f.status] ?? props.f.status)}>
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
            title={t('Open the file')}
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
        <Show when={patch() !== undefined} fallback={<p class="muted small pad">{t('Loading…')}</p>}>
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
    <Show when={lines().length} fallback={<p class="muted small pad">{props.text.startsWith(t('Error: {message}', { message: '' })) ? props.text : t('No text difference (binary file or mode).')}</p>}>
      <pre class="tk-patch">
        <For each={lines()}>{(l) => <div class={l.cls}>{l.text || ' '}</div>}</For>
      </pre>
    </Show>
  )
}

/** Merge into the base, rebase on it, and the conflicts they leave (docs/kanban.md). */
function GitOps(props: { tk: Ticket; tick: number; behind: number; onDone: () => void }) {
  const tk = () => props.tk
  const [squash, setSquash] = createSignal(false)
  const [busy, setBusy] = createSignal('')
  const [info, { mutate }] = createResource(
    () => ({ id: tk().id, v: ticketVersion(tk().id), k: props.tick }),
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
    const d = await ticketDiff(tk().id).catch(() => null)
    if (d?.dirty && !confirm(t('The worktree has uncommitted changes: they will not be merged. Continue?'))) return
    await run('merge', () => mergeTicket(tk().id, squash()))
  }
  const busyState = (s?: GitOpState) => !!s && (s.rebase || s.merge || s.squash)
  const openChildren = () => (tk().children ?? []).filter((c) => c.status !== 'done' && c.status !== 'abandoned').map((c) => `#${c.id}`)
  return (
    <Show when={info()}>
      {(i) => (
        <div class="tk-gitops" data-testid="ticket-gitops">
          <Show when={busyState(i().worktree)}>
            <Conflicts tk={tk()} where="worktree" state={i().worktree!} busy={busy()} run={run} />
          </Show>
          <Show when={busyState(i().main)}>
            <Conflicts tk={tk()} where="main" state={i().main} busy={busy()} run={run} />
          </Show>
          <div class="tk-git-row">
            <Show
              when={!i().merged}
              fallback={
                <span class="badge ok" data-testid="ticket-merged">
                  <Icon name="check" size={11} /> {t('merged into {branch}', { branch: i().into })}
                </span>
              }
            >
              <select class="small" value={squash() ? 'squash' : 'merge'} onChange={(e) => setSquash(e.currentTarget.value === 'squash')} title={t('Merge mode')}>
                <option value="merge">merge --no-ff</option>
                <option value="squash">squash</option>
              </select>
              <button
                class="btn small"
                disabled={!!busy() || busyState(i().main) || openChildren().length > 0}
                title={openChildren().length ? t('The lineage is not finished: {ids}', { ids: openChildren().join(', ') }) : undefined}
                onClick={() => void merge()}
                data-testid="ticket-merge"
              >
                <Icon name="branch" size={12} /> {busy() === 'merge' ? t('Merging…') : t('Merge into {branch}', { branch: i().into })}
              </button>
            </Show>
            <Show when={i().worktree && !busyState(i().worktree) && !i().merged}>
              <button
                class="btn small"
                disabled={!!busy()}
                title={t('git fetch, then rebase of the branch on its base, in the worktree')}
                onClick={() => void run('rebase', () => rebaseTicket(tk().id))}
                data-testid="ticket-rebase"
              >
                <Icon name="refresh" size={12} /> {busy() === 'rebase' ? t('Rebasing…') : props.behind ? t('Rebase ({n} behind)', { n: props.behind }) : t('Rebase')}
              </button>
            </Show>
          </div>
        </div>
      )}
    </Show>
  )
}

/** Pull request of the ticket: its branch pushed to origin, opened with gh (docs/kanban.md). */
export function PullRequest(props: { tk: Ticket; apply: Apply; folded?: boolean }) {
  const tk = () => props.tk
  const [busy, setBusy] = createSignal(false)
  const [info] = createResource(
    () => (tk().branch && !tk().pr ? { id: tk().id, v: ticketVersion(tk().id) } : null),
    ({ id }) => gitInfo(id).catch(() => null),
  )
  const open = async () => {
    setBusy(true)
    await props.apply(openPR(tk().id))
    setBusy(false)
  }
  const openChildren = () => (tk().children ?? []).filter((c) => c.status !== 'done' && c.status !== 'abandoned').map((c) => `#${c.id}`)
  return (
    // Shown once it exists or can be made: a remote "origin" and the gh command.
    <Show when={!tk().parent && (tk().pr || info()?.canPR)}>
      <Section title={t('Pull request')} folded={props.folded}>
        <Show
          when={tk().pr}
          fallback={
            <div class="tk-git-row">
              <button
                class="btn small"
                classList={{ primary: tk().status === 'review' }}
                disabled={busy() || openChildren().length > 0}
                title={
                  openChildren().length
                    ? t('The lineage is not finished: {ids}', { ids: openChildren().join(', ') })
                    : t('Pushes the branch {branch} to origin, then opens its pull request with gh', { branch: tk().branch! })
                }
                onClick={() => void open()}
                data-testid="ticket-pr"
              >
                <Icon name="branch" size={12} /> {busy() ? t('Opening the pull request…') : t('Create the pull request')}
              </button>
            </div>
          }
        >
          <div class="tk-git-row">
            <Icon name="branch" size={12} />
            <a class="link mono ellipsis" href={tk().pr} target="_blank" rel="noopener" data-testid="ticket-pr-link">
              {tk().pr}
            </a>
          </div>
        </Show>
      </Section>
    </Show>
  )
}

function Conflicts(props: {
  tk: Ticket
  where: 'worktree' | 'main'
  state: GitOpState
  busy: string
  run: (label: string, f: () => Promise<GitInfo>) => Promise<void>
}) {
  const what = () => (props.where === 'worktree' ? t('Rebase in progress in the worktree') : props.state.squash ? t('Merge (squash) in progress in the main folder') : t('Merge in progress in the main folder'))
  const here = () => (props.where === 'worktree' ? inWorktreeOf(props.tk) : !project()?.parent)
  const resolve = () => (props.where === 'worktree' ? startWorkSession(props.tk, 'resolve') : startTicketChat(props.tk, 'resolve'))
  return (
    <div class="tk-conflicts" data-testid={`ticket-conflicts-${props.where}`}>
      <div class="tk-git-row">
        <Icon name="conflict" size={13} />
        <strong>{what()}</strong>
        <span class="muted small">
          {props.state.conflicts.length ? tn(props.state.conflicts.length, '{n} file in conflict', '{n} files in conflict') : t('conflicts resolved: continue to finish')}
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
        <button class="btn small primary" disabled={!!props.busy} onClick={() => void props.run('continue', () => continueGit(props.tk.id, props.where))} data-testid="ticket-continue">
          {t('Continue')}
        </button>
        <button class="btn small" disabled={!!props.busy} onClick={() => void props.run('abort', () => abortGit(props.tk.id, props.where))} data-testid="ticket-abort">
          {props.where === 'worktree' ? t('Abort the rebase') : t('Abort the merge')}
        </button>
        <Show when={props.state.conflicts.length}>
          <button class="btn small" onClick={() => void resolve()} data-testid="ticket-resolve">
            <Icon name="sparkle" size={12} /> {t('Resolution session')}
          </button>
        </Show>
      </div>
    </div>
  )
}
