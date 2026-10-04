// Main window of a project: menu bar, icon rails, side panels, editor area, bottom tools.
import { createEffect, createSignal, For, type JSX, onCleanup, onMount, Show } from 'solid-js'
import { Dynamic } from 'solid-js/web'
import { RpcError, formatRate, on as onPod, podRates, podState, request } from '../pod/rpc'
import {
  activeLeaf, activeTab, closeTab, conflictedDocs, cycleTab, docsVersion, mutate, navigate as navHistory, openFile, openProject, project, relPath, reopenProject,
  saveAll, session, setConflictOpener, splitPane, closeProject, basename,
} from '../state/project'
import { defaultPlacement, moveTool, normalizePlacement, shownIn, toggleTool, toolsIn, type Zone } from '../state/zones'
import { focusPart, setFocusPart, trackFocus } from '../state/focus'
import { settings, updateSettings } from '../state/settings'
import { navigate } from '../app/router'
import { EditorArea } from '../ui/EditorArea'
import { ConsoleTool, ProblemsTool, newConsole, problemCount, setConsoleList } from '../console/consoles'
import { Explorer } from '../panels/Explorer'
import { GlobalSearch, focusGlobalSearch } from '../panels/GlobalSearch'
import { GitPanel } from '../panels/GitPanel'
import { KanbanPanel } from '../kanban/Panel'
import { NewTicketHost } from '../kanban/Board'
import { board, ensureBoard, openBoard, refreshBoard } from '../kanban/state'
import { refreshGit } from '../state/git'
import { DatabaseTool } from '../db/DatabaseTool'
import { AssistantTool } from '../llm/AssistantTool'
import { ConflictsTool, StructureTool } from '../tools/tools'
import { InfoTool } from '../tools/InfoTool'
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

/** Tools of the four zones; their placement comes from state/zones. */
export const toolPanels: Record<string, { label: string; icon: string; component: () => JSX.Element; badge?: () => number }> = {
  explorer: { label: 'Explorer', icon: 'files', component: Explorer },
  search: { label: 'Search', icon: 'search', component: GlobalSearch },
  git: { label: 'Git', icon: 'branch', component: GitPanel },
  kanban: { label: 'Kanban', icon: 'kanban', component: KanbanPanel },
  console: { label: 'Console', icon: 'terminal', component: ConsoleTool },
  problems: { label: 'Problems', icon: 'problems', component: ProblemsTool, badge: problemCount },
  database: { label: 'Database explorer', icon: 'database', component: DatabaseTool },
  assistant: { label: 'AI assistant', icon: 'sparkle', component: AssistantTool },
  structure: { label: 'Structure', icon: 'outline', component: StructureTool },
  conflicts: { label: 'Conflicts', icon: 'conflict', component: ConflictsTool },
  info: { label: 'Infos', icon: 'info', component: InfoTool },
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
  ['menu|View', ['view.splitRight', 'view.splitDown', 'view.closeTab', 'view.toggleLeft', 'view.toggleRight', 'view.toggleBottom', 'view.resetTools', 'view.visualFocus', 'view.focusOutline', 'console.new', 'palette.open']],
]

// Menu entries switching a setting, shown with a check box.
const menuChecks: Record<string, () => boolean> = {
  'view.visualFocus': () => settings.visualFocus,
  'view.focusOutline': () => settings.focusOutline,
}

function openMenu(e: MouseEvent, ids: string[]) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  const items: MenuItem[] = ids.map((id) => ({
    label: t(actions.find((a) => a.id === id)?.label ?? id),
    hint: shortcutOf(id),
    checked: menuChecks[id]?.(),
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

function Rail(props: { side: 'left' | 'right' }) {
  const bottom: Zone = props.side === 'left' ? 'bottomLeft' : 'bottomRight'
  return (
    <nav class={`rail rail-${props.side}`} classList={{ 'rail-dragging': !!dragged() }} aria-label={props.side === 'left' ? t('Panels') : t('Tools')}>
      <RailGroup zone={props.side} />
      <span class="grow" />
      <RailGroup zone={bottom} />
    </nav>
  )
}

// Icon dragged between the groups of the rails, and where it would land.
const [dragged, setDragged] = createSignal<string | null>(null)
const [dropAt, setDropAt] = createSignal<{ zone: Zone; index: number } | null>(null)

function RailGroup(props: { zone: Zone }) {
  const current = () => shownIn(session, props.zone)
  const list = () => toolsIn(session, props.zone).filter((id) => toolPanels[id])
  // Insertion index among the icons of the group, the dragged one left out.
  const indexAt = (el: HTMLElement, y: number) => {
    const btns = [...el.querySelectorAll<HTMLElement>('.rail-btn')].filter((b) => b.dataset.id !== dragged())
    const i = btns.findIndex((b) => y < b.getBoundingClientRect().top + b.offsetHeight / 2)
    return i < 0 ? btns.length : i
  }
  const marker = (id: string) => {
    const d = dropAt()
    if (!d || d.zone !== props.zone) return false
    return list().filter((x) => x !== dragged())[d.index] === id
  }
  return (
    <div
      class="rail-group"
      data-zone={props.zone}
      classList={{ 'drop-end': dropAt()?.zone === props.zone && dropAt()!.index === list().filter((x) => x !== dragged()).length }}
      onDragOver={(e) => {
        if (!dragged()) return
        e.preventDefault()
        setDropAt({ zone: props.zone, index: indexAt(e.currentTarget, e.clientY) })
      }}
      onDrop={(e) => {
        e.preventDefault()
        const id = dragged()
        if (id) mutate((s) => moveTool(s, id, props.zone, indexAt(e.currentTarget, e.clientY)))
        setDragged(null)
        setDropAt(null)
      }}
    >
      <For each={list()}>
        {(id) => (
          <button
            class="rail-btn"
            classList={{ active: current() === id, focused: current() === id && focusPart() === props.zone, 'drop-before': marker(id), dragging: dragged() === id }}
            data-id={id}
            draggable="true"
            title={t(toolPanels[id].label)}
            aria-pressed={current() === id}
            onClick={() => {
              mutate((s) => toggleTool(s, id))
              if (current() === id) setFocusPart(props.zone)
            }}
            onDragStart={(e) => {
              e.dataTransfer!.effectAllowed = 'move'
              e.dataTransfer!.setData('text/plain', id)
              setDragged(id)
            }}
            onDragEnd={() => {
              setDragged(null)
              setDropAt(null)
            }}
          >
            <Icon name={toolPanels[id].icon} size={18} />
            <Show when={toolPanels[id].badge?.()}>
              <span class="rail-badge" />
            </Show>
          </button>
        )}
      </For>
    </div>
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

/** One zone with its tool, a side panel when `width` is given. */
function ZonePanel(props: { zone: Zone; width?: number; style?: JSX.CSSProperties }) {
  const id = () => shownIn(session, props.zone)!
  const detach = () => window.open(`/project/${project()!.id}/tool/${id()}`, `tool-${id()}`, id() === 'assistant' ? 'popup,width=1100,height=820' : 'popup,width=420,height=760')
  const side = props.zone === 'left' || props.zone === 'right'
  return (
    <aside class={`zone zone-${props.zone}`} classList={{ side, [`side-${props.zone}`]: side, focused: focusPart() === props.zone }} data-tool={id()} data-focus={props.zone} style={props.width ? { width: `${props.width}px` } : props.style}>
      <button class="icon-btn detach" title={t('Open in a window')} onClick={detach}>
        <Icon name="external" size={13} />
      </button>
      <Show when={toolPanels[id()]} keyed>
        {(p) => <Dynamic component={p.component} />}
      </Show>
    </aside>
  )
}

function BottomStrip() {
  let el!: HTMLDivElement
  const left = () => shownIn(session, 'bottomLeft')
  const right = () => shownIn(session, 'bottomRight')
  const both = () => !!left() && !!right()
  return (
    <>
      <Resizer dir="y" onDrag={(d) => mutate((s) => (s.bottom.height = Math.max(100, Math.min(innerHeight - 200, s.bottom.height - d))))} />
      <div class="bottom" ref={el} style={{ height: `${session.bottom.height}px` }}>
        <Show when={left()}>
          <ZonePanel zone="bottomLeft" style={{ flex: both() ? `0 0 ${session.bottom.split * 100}%` : '1 1 0' }} />
        </Show>
        <Show when={both()}>
          <Resizer dir="x" onDrag={(d) => mutate((s) => (s.bottom.split = Math.max(0.15, Math.min(0.85, s.bottom.split + d / el.offsetWidth))))} />
        </Show>
        <Show when={right()}>
          <ZonePanel zone="bottomRight" style={{ flex: '1 1 0' }} />
        </Show>
      </div>
    </>
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
      // A worktree just created from the menu bar: run the setup command of the kanban.
      if (new URLSearchParams(location.search).has('setup')) {
        history.replaceState(null, '', location.pathname)
        ensureBoard()
        await refreshBoard()
        const cmd = board.meta.setup?.trim()
        if (cmd) void newConsole({ kind: 'task', command: ['sh', '-c', cmd], title: t('Worktree setup') })
      }
      setReady(true)
      refreshGit(0)
      window.name = `project-${props.id}` // see openWorktreeWindow
    } catch (e) {
      setError((e as Error).message)
    }
  })
  useProjectActions()
  // A closed zone gives the focus back to the editor.
  createEffect(() => {
    const part = focusPart()
    if (part !== 'editor' && !shownIn(session, part)) setFocusPart('editor')
  })
  const offs = [
    registerAction('view.toggleLeft', () => mutate((s) => (s.left.panel = s.left.panel ? null : (toolsIn(s, 'left')[0] ?? null)))),
    registerAction('view.toggleRight', () => mutate((s) => (s.right.panel = s.right.panel ? null : (toolsIn(s, 'right')[0] ?? null)))),
    registerAction('view.resetTools', () => mutate((s) => (s.placement = normalizePlacement(defaultPlacement)))),
    registerAction('view.visualFocus', () => updateSettings((s) => (s.visualFocus = !s.visualFocus), 'Visual focus')),
    registerAction('view.focusOutline', () => updateSettings((s) => (s.focusOutline = !s.focusOutline), 'Focus outline')),
    registerAction('console.new', () => void newConsole()),
    registerAction('view.toggleBottom', () => mutate((s) => toggleTool(s, 'console'))),
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
      <div class="app" classList={{ 'visual-focus': settings.visualFocus, 'focus-outline': settings.focusOutline }}>
        <MenuBar />
        <NewTicketHost />
        <div class="workbench" onPointerDown={trackFocus} onFocusIn={trackFocus}>
          <Rail side="left" />
          <div class="work">
            <div class="work-top">
              <Show when={shownIn(session, 'left')}>
                <ZonePanel zone="left" width={session.left.width} />
                <Resizer dir="x" onDrag={(d) => mutate((s) => (s.left.width = Math.max(160, Math.min(700, s.left.width + d))))} />
              </Show>
              <main class="center" data-focus="editor">
                <EditorArea />
              </main>
              <Show when={shownIn(session, 'right')}>
                <Resizer dir="x" onDrag={(d) => mutate((s) => (s.right.width = Math.max(200, Math.min(800, s.right.width - d))))} />
                <ZonePanel zone="right" width={session.right.width} />
              </Show>
            </div>
            <Show when={shownIn(session, 'bottomLeft') || shownIn(session, 'bottomRight')}>
              <BottomStrip />
            </Show>
          </div>
          <Rail side="right" />
        </div>
      </div>
    </Show>
  )
}
