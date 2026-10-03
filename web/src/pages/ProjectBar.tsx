// Project part of the menu bar: icon, title, and the selector of the worktrees of the
// repository (main folder, tickets, other branches) opened each in its own window.
import { createEffect, createSignal, Show } from 'solid-js'
import { RpcError, request } from '../pod/rpc'
import { project } from '../state/project'
import { gitStatus } from '../state/git'
import { board, ensureBoard, openTicket, openWorktreeWindow, statusLabels, summary, worktreeProject } from '../kanban/state'
import { pick, prompt, type PickItem } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { iconOf, iconURL, useProjectIcon, withBadge } from '../ui/projectIcon'
import { IconEditor } from '../ui/IconEditor'
import { t } from '../i18n'

interface Worktree {
  path: string
  branch?: string
  main?: boolean
  project?: string
  ticket?: number
}

type Choice = { kind: 'remove' } | { kind: 'open-ticket'; id: number } | { kind: 'project'; id: string } | { kind: 'ticket'; id: number } | { kind: 'worktree'; path: string } | { kind: 'branch' }

export function ProjectBar() {
  const owner = useProjectIcon(project)
  const [editing, setEditing] = createSignal(false)
  const svg = () => {
    const s = iconOf(owner()?.owner ?? '')
    return s && project()?.parent ? withBadge(s) : s
  }
  const ticket = () => project()?.ticket
  const branch = () => gitStatus()?.branch ?? ''
  createEffect(() => {
    const name = owner()?.name ?? project()?.name
    if (name) document.title = [name, ticket() ? `#${ticket()}` : branch()].filter(Boolean).join(' · ')
  })

  const open = async (e: MouseEvent) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    try {
      ensureBoard()
      const list = await request<{ root: string; items: Worktree[] }>('worktrees.list')
      // A worktree belongs to a ticket when a ticket project is opened on it or when its
      // branch is the branch of a ticket.
      const ticketOf = (w: Worktree) => w.ticket || board.tickets.find((tk) => tk.branch && tk.branch === w.branch && tk.worktree)?.id || 0
      const here = project()?.id
      const items: PickItem<Choice>[] = []
      const n = ticket()
      if (n) items.push({ label: t('Open the ticket #{id}', { id: n }), value: { kind: 'open-ticket', id: n } })
      const main = list.items.find((w) => w.main)
      items.push({
        label: main?.branch || t('main folder'),
        detail: t('main folder'),
        hint: list.root === here ? t('this window') : '',
        value: { kind: 'project', id: list.root },
      })
      const rest = list.items.filter((w) => !w.main)
      for (const w of rest.filter(ticketOf).sort((a, b) => ticketOf(a) - ticketOf(b))) {
        const n = ticketOf(w)
        const tk = summary(n)
        items.push({
          label: `#${n} ${tk?.title ?? ''}`,
          detail: w.branch,
          hint: w.project && w.project === here ? t('this window') : tk ? statusLabels[tk.status] : '',
          value: { kind: 'ticket', id: n },
        })
      }
      for (const w of rest.filter((w) => !ticketOf(w)))
        items.push({
          label: w.branch || w.path.split('/').pop()!,
          detail: main && w.path.startsWith(main.path + '/') ? w.path.slice(main.path.length + 1) : w.path,
          hint: w.project && w.project === here ? t('this window') : '',
          value: w.project ? { kind: 'project', id: w.project } : { kind: 'worktree', path: w.path },
        })
      items.push({ label: t('Open a branch…'), detail: t('in its own worktree'), value: { kind: 'branch' } })
      const removable = rest.filter((w) => !ticketOf(w))
      if (removable.length) items.push({ label: t('Remove a worktree…'), detail: t('the branch is kept'), value: { kind: 'remove' } })
      const c = await pick<Choice>({ placeholder: t('Worktrees and branches'), items, anchor: { left: r.left, top: r.bottom + 2 } })
      if (!c) return
      if (c.kind === 'open-ticket') openTicket(c.id)
      else if (c.kind === 'project') {
        if (c.id !== here) openWorktreeWindow(c.id)
      } else if (c.kind === 'ticket') openWorktreeWindow((await worktreeProject(c.id)).project)
      else if (c.kind === 'worktree') openWorktreeWindow((await request<{ project: string }>('worktrees.open', { path: c.path })).project)
      else if (c.kind === 'branch') await openBranch(list.items, r)
      else await removeWorktree(list.root, rest.filter((w) => !ticketOf(w)), r)
    } catch (err) {
      errorToast(err)
    }
  }

  // A branch is checked out in a new worktree: the main folder and its changes stay as they are.
  const openBranch = async (worktrees: Worktree[], r: DOMRect) => {
    const used = new Set(worktrees.map((w) => w.branch))
    const branches = await request<{ name: string; remote?: boolean; upstream?: string }[]>('git.branches')
    const local = new Set(branches.filter((b) => !b.remote).map((b) => b.name))
    const choice = await pick<string>({
      placeholder: t('Open a branch…'),
      anchor: { left: r.left, top: r.bottom + 2 },
      items: [
        { label: t('+ New branch…'), value: '\0new' },
        ...branches
          .filter((b) => !used.has(b.name) && !(b.remote && local.has(b.name.replace(/^[^/]+\//, ''))))
          .map((b) => ({ label: b.name, detail: b.remote ? t('remote') : b.upstream ? `→ ${b.upstream}` : '', value: b.name })),
      ],
    })
    if (!choice) return
    let create = false
    let name = choice
    if (choice === '\0new') {
      const v = await prompt({ title: t('New branch'), label: t('Created from {branch}', { branch: worktrees.find((w) => w.main)?.branch ?? 'HEAD' }) })
      if (!v?.trim()) return
      name = v.trim()
      create = true
    }
    const added = await request<{ project: string; setup: string }>('worktrees.add', { branch: name, create })
    openWorktreeWindow(added.project, false, !!added.setup)
  }

  const removeWorktree = async (root: string, worktrees: Worktree[], r: DOMRect) => {
    const here = project()?.id
    const w = await pick<Worktree>({
      placeholder: t('Remove a worktree…'),
      anchor: { left: r.left, top: r.bottom + 2 },
      items: worktrees.map((w) => ({ label: w.branch || w.path.split('/').pop()!, detail: w.path, hint: w.project && w.project === here ? t('this window') : '', value: w })),
    })
    if (!w || !confirm(t('Remove the worktree of {branch}? The branch is kept.', { branch: w.branch ?? w.path }))) return
    try {
      await request('worktrees.remove', { path: w.path })
    } catch (e) {
      if (!(e instanceof RpcError && e.code === 'dirty') || !confirm(t('The worktree has uncommitted changes: they will be lost with it. Remove anyway?'))) throw e
      await request('worktrees.remove', { path: w.path, force: true })
    }
    toast(t('Worktree removed'), 'ok')
    // This window was on it: back to the main folder.
    if (w.project && w.project === here) location.assign(`/project/${encodeURIComponent(root)}`)
  }

  return (
    <div class="mb-project">
      <button class="mb-icon" title={t('Change the icon')} onClick={() => setEditing(true)} data-testid="menubar-icon">
        <Show when={svg()}>
          <img src={iconURL(svg())} width="20" height="20" alt="" />
        </Show>
      </button>
      <span class="mb-title" title={project()?.path}>
        {owner()?.name ?? project()?.name}
        <Show when={project()?.type === 'ssh'}>
          <span class="badge">ssh</span>
        </Show>
      </span>
      <button class="mb-branch" classList={{ wt: !!ticket() }} title={t('Worktrees and branches')} onClick={open} data-testid="branch-selector">
        <Show
          when={ticket()}
          fallback={
            <>
              <Icon name="branch" size={12} />
              <span class="ellipsis">{branch() || t('no git')}</span>
            </>
          }
        >
          {(n) => {
            ensureBoard()
            const tk = () => summary(n())
            return (
              <span class="mb-ticket" data-testid="worktree-banner">
                <Icon name="kanban" size={12} />
                <span class="ellipsis">{t('Ticket #{id} {title}', { id: n(), title: tk()?.title ?? '' })}</span>
                <Show when={tk()}>
                  <span class={`kb-status st-${tk()!.status}`}>{statusLabels[tk()!.status]}</span>
                </Show>
              </span>
            )
          }}
        </Show>
        <Icon name="chevron" size={10} />
      </button>
      <Show when={editing() && owner()}>
        <IconEditor id={owner()!.owner} name={owner()!.name} spec={owner()!.spec} onClose={() => setEditing(false)} />
      </Show>
    </div>
  )
}
