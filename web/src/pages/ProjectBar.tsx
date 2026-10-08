// Project part of the menu bar: icon, title, and the selector of the worktrees of the
// repository (main folder, tickets, other branches), shown in the same window or in a new one.
import { createEffect, createSignal, Show } from 'solid-js'
import { RpcError, request } from '../pod/rpc'
import { home, project, showWorktree } from '../state/project'
import { gitStatus } from '../state/git'
import { board, ensureBoard, openTicket, openWorktreeWindow, statusLabels, summary, worktreeProject } from '../kanban/state'
import { pick, prompt, type PickItem } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { iconOf, iconURL, useProjectIcon, withBadge } from '../ui/projectIcon'
import { IconEditor } from '../ui/IconEditor'
import { newConsole } from '../console/consoles'
import { t } from '../i18n'

interface Worktree {
  path: string
  branch?: string
  main?: boolean
  project?: string
  ticket?: number
}

type Choice = { kind: 'remove' } | { kind: 'window' } | { kind: 'open-ticket'; id: number } | { kind: 'project'; id: string } | { kind: 'ticket'; id: number } | { kind: 'worktree'; path: string } | { kind: 'branch' }

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
    const anchor = { left: r.left, top: r.bottom + 2 }
    try {
      ensureBoard()
      const list = await request<{ root: string; items: Worktree[] }>('worktrees.list')
      // A worktree belongs to a ticket when a ticket project is opened on it or when its
      // branch is the branch of a ticket.
      const ticketOf = (w: Worktree) => w.ticket || board.tickets.find((tk) => tk.branch && tk.branch === w.branch && tk.worktree)?.id || 0
      const here = project()?.id
      const shown = t('shown')
      const worktrees: PickItem<Choice>[] = []
      const main = list.items.find((w) => w.main)
      worktrees.push({
        label: main?.branch || t('main folder'),
        detail: t('main folder'),
        hint: list.root === here ? shown : '',
        value: { kind: 'project', id: list.root },
      })
      const rest = list.items.filter((w) => !w.main)
      for (const w of rest.filter(ticketOf).sort((a, b) => ticketOf(a) - ticketOf(b))) {
        const n = ticketOf(w)
        const tk = summary(n)
        worktrees.push({
          label: `#${n} ${tk?.title ?? ''}`,
          detail: w.branch,
          hint: w.project && w.project === here ? shown : tk ? statusLabels[tk.status] : '',
          value: { kind: 'ticket', id: n },
        })
      }
      for (const w of rest.filter((w) => !ticketOf(w)))
        worktrees.push({
          label: w.branch || w.path.split('/').pop()!,
          detail: main && w.path.startsWith(main.path + '/') ? w.path.slice(main.path.length + 1) : w.path,
          hint: w.project && w.project === here ? shown : '',
          value: w.project ? { kind: 'project', id: w.project } : { kind: 'worktree', path: w.path },
        })
      const items: PickItem<Choice>[] = []
      const n = ticket()
      if (n) items.push({ label: t('Open the ticket #{id}', { id: n }), value: { kind: 'open-ticket', id: n } })
      items.push(...worktrees)
      items.push({ label: t('Open a branch…'), detail: t('in its own worktree'), value: { kind: 'branch' } })
      items.push({ label: t('Open in a new window…'), detail: t('a worktree in its own window'), value: { kind: 'window' } })
      const removable = rest.filter((w) => !ticketOf(w))
      if (removable.length) items.push({ label: t('Remove a worktree…'), detail: t('the branch is kept'), value: { kind: 'remove' } })
      const c = await pick<Choice>({ placeholder: t('Worktrees and branches'), items, anchor })
      if (!c) return
      if (c.kind === 'open-ticket') openTicket(c.id)
      else if (c.kind === 'branch') await openBranch(list.items, r)
      else if (c.kind === 'remove') await removeWorktree(list.root, removable, r)
      else if (c.kind === 'window') {
        const w = await pick<Choice>({ placeholder: t('Open in a new window…'), items: worktrees, anchor })
        if (w) openWorktreeWindow(await projectOf(w))
      } else await showWorktree(await projectOf(c))
    } catch (err) {
      errorToast(err)
    }
  }

  // Project opened on a worktree chosen in the list.
  const projectOf = async (c: Choice): Promise<string> => {
    if (c.kind === 'project') return c.id
    if (c.kind === 'ticket') return (await worktreeProject(c.id)).project
    if (c.kind === 'worktree') return (await request<{ project: string }>('worktrees.open', { path: c.path })).project
    throw new Error('not a worktree')
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
    await showWorktree(added.project)
    // The setup command of the kanban (npm install…) runs in a console of the new worktree.
    if (added.setup) void newConsole({ kind: 'task', command: ['sh', '-c', added.setup], title: t('Worktree setup') })
  }

  const removeWorktree = async (root: string, worktrees: Worktree[], r: DOMRect) => {
    const here = project()?.id
    const shown = t('shown')
    const w = await pick<Worktree>({
      placeholder: t('Remove a worktree…'),
      anchor: { left: r.left, top: r.bottom + 2 },
      items: worktrees.map((w) => ({ label: w.branch || w.path.split('/').pop()!, detail: w.path, hint: w.project && w.project === here ? shown : '', value: w })),
    })
    if (!w || !confirm(t('Remove the worktree of {branch}? The branch is kept.', { branch: w.branch ?? w.path }))) return
    try {
      await request('worktrees.remove', { path: w.path })
    } catch (e) {
      if (!(e instanceof RpcError && e.code === 'dirty') || !confirm(t('The worktree has uncommitted changes: they will be lost with it. Remove anyway?'))) throw e
      await request('worktrees.remove', { path: w.path, force: true })
    }
    toast(t('Worktree removed'), 'ok')
    // A window opened on it goes back to the main folder (one showing it does by itself).
    if (w.project && w.project === home()?.id) location.assign(`/project/${encodeURIComponent(root)}`)
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
              <span class="mb-ticket" data-testid="worktree-banner" title={tk() ? `#${n()} ${tk()!.title} · ${statusLabels[tk()!.status]}` : undefined}>
                <Icon name="kanban" size={12} />
                <span class="ellipsis">{t('Ticket #{id}', { id: n() })}</span>
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
