// Main window of a project: menu bar, icon rails, side panels, editor area, consoles.
import { createSignal, For, type JSX, onCleanup, onMount, Show } from 'solid-js'
import { Dynamic } from 'solid-js/web'
import { RpcError, formatRate, on as onPod, podRates, podState, request } from '../pod/rpc'
import {
  activeLeaf, activeTab, closeTab, conflictedDocs, cycleTab, docsVersion, mutate, navigate as navHistory, openFile, openProject, project, relPath, reopenProject,
  saveAll, session, setConflictOpener, splitPane, closeProject, basename,
} from '../state/project'
import { navigate } from '../app/router'
import { EditorArea } from '../ui/EditorArea'
import { BottomPanel, newConsole, setConsoleList } from '../console/consoles'
import { Explorer } from '../panels/Explorer'
import { GlobalSearch, focusGlobalSearch } from '../panels/GlobalSearch'
import { Connections } from '../panels/Connections'
import { GitPanel } from '../panels/GitPanel'
import { KanbanPanel } from '../kanban/Panel'
import { NewTicketHost } from '../kanban/Board'
import { openBoard } from '../kanban/state'
import { refreshGit } from '../state/git'
import { DatabaseTool } from '../db/DatabaseTool'
import { AssistantTool } from '../llm/AssistantTool'
import { ConflictsTool, ExtensionsTool, PropertiesTool, StructureTool } from '../tools/tools'
import { openConflict } from '../conflict/ConflictDialog'
import { openSettings } from '../settings/SettingsModal'
import { actions, registerAction, runAction, shortcutOf } from '../keys/bindings'
import { contextMenu, pick, prompt, fuzzy, type MenuItem } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { cursorInfo } from '../ui/status'
import { refreshConnections } from '../db/api'
import * as lspc from '../lsp/client'
import { lspLanguage } from '../editor/languages'
import { toast } from '../ui/toast'
import { t, tn } from '../i18n'
import { ProjectBar } from './ProjectBar'

export const leftPanels: Record<string, { label: string; icon: string; component: () => JSX.Element }> = {
  explorer: { label: 'Explorer', icon: 'files', component: Explorer },
  search: { label: 'Search', icon: 'search', component: GlobalSearch },
  git: { label: 'Git', icon: 'branch', component: GitPanel },
  kanban: { label: 'Kanban', icon: 'kanban', component: KanbanPanel },
  connections: { label: 'Connections', icon: 'plug', component: Connections },
}

export const rightPanels: Record<string, { label: string; icon: string; component: () => JSX.Element }> = {
  database: { label: 'Database explorer', icon: 'database', component: DatabaseTool },
  assistant: { label: 'AI assistant', icon: 'sparkle', component: AssistantTool },
  structure: { label: 'Structure', icon: 'outline', component: StructureTool },
  conflicts: { label: 'Conflicts', icon: 'conflict', component: ConflictsTool },
  extensions: { label: 'Extensions', icon: 'puzzle', component: ExtensionsTool },
  properties: { label: 'Properties', icon: 'info', component: PropertiesTool },
}

/** Opens a project, asking for the SSH password / passphrase when the pod needs one. */
export async function openWithAuth(id: string) {
  let creds: { password?: string; passphrase?: string } | undefined
  for (;;) {
    try {
      const r = await openProject(id, creds)
      setConsoleList(r.consoles)
      refreshConnections()
      return r
    } catch (e) {
      if (e instanceof RpcError && e.code === 'auth_required') {
        const v = await prompt({ title: t('SSH connection'), label: e.message, password: true })
        if (v === null) throw e
        creds = e.data?.kind === 'passphrase' ? { passphrase: v } : { password: v }
        continue
      }
      throw e
    }
  }
}

/** Global actions of a project window. */
export function useProjectActions() {
  setConflictOpener(openConflict)
  const offs = [
    registerAction('file.saveAll', () => void saveAll()),
    registerAction('view.splitRight', () => splitPane(session.activePane, 'row')),
    registerAction('view.splitDown', () => splitPane(session.activePane, 'col')),
    registerAction('view.closeTab', () => {
      const l = activeLeaf()
      if (l.active) closeTab(l.id, l.active)
    }),
    registerAction('view.nextTab', () => cycleTab(1)),
    registerAction('view.prevTab', () => cycleTab(-1)),
    registerAction('nav.back', () => navHistory(-1)),
    registerAction('nav.forward', () => navHistory(1)),
    registerAction('settings.open', () => openSettings()),
    registerAction('kanban.open', () => openBoard()),
    registerAction('palette.open', () => void palette()),
    registerAction('nav.gotoFile', () => void gotoFile()),
    registerAction('nav.gotoSymbol', () => void gotoSymbol()),
    registerAction('conflict.resolve', () => {
      const list = conflictedDocs()
      if (!list.length) return false
      openConflict(list[0])
    }),
  ]
  onCleanup(() => offs.forEach((f) => f()))
}

async function palette() {
  const id = await pick({
    placeholder: t('Command…'),
    items: actions.map((a) => ({ label: t(a.label), detail: t(a.category), hint: shortcutOf(a.id), value: a.id })),
  })
  if (id) {
    focusActiveEditor()
    requestAnimationFrame(() => runAction(id))
  }
}

let fileCache: { at: number; files: string[] } | null = null
async function gotoFile() {
  const files = async () => {
    if (!fileCache || Date.now() - fileCache.at > 15000) fileCache = { at: Date.now(), files: await request<string[]>('search.files') }
    return fileCache.files
  }
  const p = await pick<string>({
    placeholder: t('Go to file (name or path, fuzzy search)'),
    provider: async (q) => {
      const list = await files()
      if (!q) return list.slice(0, 100).map((f) => ({ label: basename(f), detail: relPath(f), value: f }))
      return list
        .map((f) => ({ f, s: fuzzy(q, basename(f)) * 2 + fuzzy(q, relPath(f)) }))
        .filter((x) => x.s > 0)
        .sort((a, b) => b.s - a.s)
        .slice(0, 100)
        .map(({ f }) => ({ label: basename(f), detail: relPath(f), value: f }))
    },
  })
  if (p) openFile(p)
}

async function gotoSymbol() {
  let path = activeTab()?.path ?? ''
  if (!lspLanguage(path)) {
    const st = await request<any[]>('lsp.status').catch(() => [])
    const lang = st.find((s) => s.detected && s.command)?.lang
    const ext: Record<string, string> = { go: '/x.go', php: '/x.php', python: '/x.py', typescript: '/x.ts' }
    path = lang ? ext[lang] : ''
  }
  if (!path) {
    toast(t('No language server available to search symbols'), 'info')
    return
  }
  const loc = await pick<lspc.Location>({
    placeholder: t('Go to symbol (class, function, method…)'),
    noFilter: true,
    provider: async (q, signal) => {
      if (q.length < 2) return []
      try {
        const list = await lspc.workspaceSymbols(path, q, signal)
        return list
          .filter((s) => s.loc)
          .slice(0, 200)
          .map((s) => ({ label: s.name, icon: lspc.symbolKinds[s.kind]?.[1], detail: `${s.container ? s.container + ' · ' : ''}${relPath(s.loc!.path)}`, value: s.loc! }))
      } catch {
        return []
      }
    },
  })
  if (loc) lspc.jump(loc)
}

function focusActiveEditor() {
  document.querySelector<HTMLElement>('.pane.active .ed-content')?.focus()
}

// ---------- menu bar ----------

const menus: [string, string[]][] = [
  ['menu|File', ['file.save', 'file.saveAll', 'nav.gotoFile', 'conflict.resolve', 'settings.open']],
  ['menu|Edit', ['edit.undo', 'edit.redo', 'edit.duplicateLine', 'edit.deleteLine', 'edit.toggleComment', 'search.find', 'search.global']],
  ['menu|Navigate', ['nav.back', 'nav.forward', 'nav.gotoLine', 'nav.gotoSymbol', 'nav.fileStructure', 'nav.related', 'nav.test']],
  ['menu|Code', ['lsp.definition', 'lsp.implementation', 'lsp.typeDefinition', 'lsp.superMethod', 'lsp.references', 'lsp.hover']],
  ['menu|View', ['view.splitRight', 'view.splitDown', 'view.closeTab', 'view.toggleLeft', 'view.toggleRight', 'view.toggleBottom', 'console.new', 'palette.open']],
]

function openMenu(e: MouseEvent, ids: string[]) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  const items: MenuItem[] = ids.map((id) => ({
    label: t(actions.find((a) => a.id === id)?.label ?? id),
    hint: shortcutOf(id),
    action: () => {
      focusActiveEditor()
      requestAnimationFrame(() => runAction(id) || toast(t('Action not available here'), 'info'))
    },
  }))
  contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.bottom + 2 }), items)
}

export function PodStatus() {
  return (
    <div class="pod-status" title={`Pod: ${t(podState())}`}>
      <span class={`dot dot-${podState()}`} />
      <span class="rate" title={t('Download rate (pod → page)')}>↓ {formatRate(podRates().down)}</span>
      <span class="rate" title={t('Upload rate (page → pod)')}>↑ {formatRate(podRates().up)}</span>
    </div>
  )
}

function MenuBar() {
  const conflicts = () => (docsVersion(), conflictedDocs().length)
  return (
    <header class="menubar">
      <button class="icon-btn" title={t('Projects')} onClick={() => navigate('/')}>
        <Icon name="home" />
      </button>
      <ProjectBar />
      <nav class="menus">
        <For each={menus}>
          {([label, ids]) => (
            <button class="menu-btn" onClick={(e) => openMenu(e, ids)}>
              {t(label)}
            </button>
          )}
        </For>
      </nav>
      <span class="grow" />
      <Show when={conflicts()}>
        <button class="badge warn" onClick={() => mutate((s) => (s.right.panel = 'conflicts'))}>
          {tn(conflicts(), '{n} conflict', '{n} conflicts')}
        </button>
      </Show>
      <Show when={cursorInfo()}>
        {(c) => (
          <span class="cursor-info">
            {c().line}:{c().col}
            {c().sel ? ` (${tn(c().sel, '{n} char', '{n} chars')})` : ''} · {c().lang}
          </span>
        )}
      </Show>
      <PodStatus />
      <button class="icon-btn" title={`${t('Settings')} (${shortcutOf('settings.open')})`} onClick={() => openSettings()}>
        <Icon name="gear" />
      </button>
    </header>
  )
}

// ---------- rails & side panels ----------

function Rail(props: { side: 'left' | 'right'; panels: typeof leftPanels }) {
  const current = () => session[props.side].panel
  const toggle = (id: string) => mutate((s) => (s[props.side].panel = s[props.side].panel === id ? null : id))
  return (
    <nav class={`rail rail-${props.side}`} aria-label={props.side === 'left' ? t('Panels') : t('Tools')}>
      <For each={Object.entries(props.panels)}>
        {([id, p]) => (
          <button class="rail-btn" classList={{ active: current() === id }} title={t(p.label)} aria-pressed={current() === id} onClick={() => toggle(id)}>
            <Icon name={p.icon} size={18} />
          </button>
        )}
      </For>
    </nav>
  )
}

function Resizer(props: { onDrag: (delta: number) => void; dir: 'x' | 'y' }) {
  const down = (e: PointerEvent) => {
    e.preventDefault()
    let last = props.dir === 'x' ? e.clientX : e.clientY
    const move = (ev: PointerEvent) => {
      const cur = props.dir === 'x' ? ev.clientX : ev.clientY
      props.onDrag(cur - last)
      last = cur
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      document.body.classList.remove('resizing')
    }
    document.body.classList.add('resizing')
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }
  return <div class={`resizer resizer-${props.dir}`} onPointerDown={down} />
}

function SidePanel(props: { side: 'left' | 'right'; panels: typeof leftPanels }) {
  const id = () => session[props.side].panel!
  const detach = () => window.open(`/project/${project()!.id}/tool/${id()}`, `tool-${id()}`, id() === 'assistant' ? 'popup,width=1100,height=820' : 'popup,width=420,height=760')
  return (
    <aside class={`side side-${props.side}`} style={{ width: `${session[props.side].width}px` }}>
      <button class="icon-btn detach" title={t('Open in a window')} onClick={detach}>
        <Icon name="external" size={13} />
      </button>
      <Show when={props.panels[id()]} keyed>
        {(p) => <Dynamic component={p.component} />}
      </Show>
    </aside>
  )
}

export function ProjectPage(props: { id: string }) {
  const [error, setError] = createSignal('')
  const [ready, setReady] = createSignal(false)

  onMount(async () => {
    try {
      await openWithAuth(props.id)
      // Opened from a ticket to run its conversation: show the assistant.
      if (new URLSearchParams(location.search).has('assistant')) {
        mutate((s) => (s.right.panel = 'assistant'))
        history.replaceState(null, '', location.pathname)
      }
      setReady(true)
      refreshGit(0)
      window.name = `project-${props.id}` // see openWorktreeWindow
    } catch (e) {
      setError((e as Error).message)
    }
  })
  useProjectActions()
  const offs = [
    registerAction('view.toggleLeft', () => mutate((s) => (s.left.panel = s.left.panel ? null : 'explorer'))),
    registerAction('view.toggleRight', () => mutate((s) => (s.right.panel = s.right.panel ? null : 'database'))),
    registerAction('console.new', () => void newConsole()),
    registerAction('view.toggleBottom', () => mutate((s) => (s.bottom.open = !s.bottom.open))),
    registerAction('search.global', () => {
      mutate((s) => (s.left.panel = 'search'))
      requestAnimationFrame(focusGlobalSearch)
    }),
    onPod('pod.reconnected', async () => {
      await reopenProject()
      request('console.list').then(setConsoleList).catch(() => {})
      refreshConnections()
    }),
    onPod('project.closed', () => {
      toast(t('The project was closed (changed or deleted)'), 'warn')
      navigate('/')
    }),
  ]
  onCleanup(() => {
    offs.forEach((f) => f())
    closeProject()
  })

  return (
    <Show
      when={ready()}
      fallback={
        <div class="center-msg">
          <Show when={error()} fallback={<p class="muted">{t('Opening the project…')}</p>}>
            <p class="danger">{error()}</p>
            <button class="btn" onClick={() => navigate('/')}>
              {t('Back to the projects')}
            </button>
          </Show>
        </div>
      }
    >
      <div class="app">
        <MenuBar />
        <NewTicketHost />
        <div class="workbench">
          <Rail side="left" panels={leftPanels} />
          <Show when={session.left.panel && leftPanels[session.left.panel]}>
            <SidePanel side="left" panels={leftPanels} />
            <Resizer dir="x" onDrag={(d) => mutate((s) => (s.left.width = Math.max(160, Math.min(700, s.left.width + d))))} />
          </Show>
          <main class="center">
            <EditorArea />
          </main>
          <Show when={session.right.panel && rightPanels[session.right.panel]}>
            <Resizer dir="x" onDrag={(d) => mutate((s) => (s.right.width = Math.max(200, Math.min(800, s.right.width - d))))} />
            <SidePanel side="right" panels={rightPanels} />
          </Show>
          <Rail side="right" panels={rightPanels} />
        </div>
        <Show
          when={session.bottom.open}
          fallback={
            <button class="bottom-toggle" onClick={() => mutate((s) => (s.bottom.open = true))} title={`Consoles (${shortcutOf('view.toggleBottom')})`}>
              <Icon name="terminal" size={13} /> {t('Consoles')}
            </button>
          }
        >
          <Resizer dir="y" onDrag={(d) => mutate((s) => (s.bottom.height = Math.max(100, Math.min(innerHeight - 200, s.bottom.height - d))))} />
          <div class="bottom" style={{ height: `${session.bottom.height}px` }}>
            <BottomPanel />
          </div>
        </Show>
      </div>
    </Show>
  )
}
