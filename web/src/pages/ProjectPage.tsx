// Main window of a project: menu bar, icon rails, side panels, editor area, bottom tools.
import { createEffect, createSignal, For, type JSX, onCleanup, onMount, Show } from 'solid-js'
import { Dynamic } from 'solid-js/web'
import { RpcError, formatRate, on as onPod, podRates, podState, request } from '../pod/rpc'
import {
  activeLeaf, activeTab, closeTab, conflictedDocs, cycleTab, docsVersion, mutate, navigate as navHistory, openProject, project, relPath, reopenProject, root,
  saveAll, session, setConflictOpener, splitPane, closeProject,
} from '../state/project'
import { defaultPlacement, moveTool, normalizePlacement, showTool, shownIn, toggleTool, toolsIn, zoneOf, type Zone } from '../state/zones'
import { focusEditor, focusPart, setFocusPart, trackFocus } from '../state/focus'
import { settings, updateSettings } from '../state/settings'
import { navigate } from '../app/router'
import { EditorArea } from '../ui/EditorArea'
import { ConsoleTool, ProblemsTool, newConsole, problemCount, setConsoleList } from '../console/consoles'
import { createIn, Explorer } from '../panels/Explorer'
import { GlobalSearch, focusGlobalSearch } from '../panels/GlobalSearch'
import { GitPanel } from '../panels/git/GitPanel'
import { KanbanPanel } from '../kanban/Panel'
import { NewTicketHost } from '../kanban/Board'
import { board, ensureBoard, openBoard, refreshBoard } from '../kanban/state'
import { refreshGit } from '../state/git'
import { DatabaseTool } from '../db/DatabaseTool'
import { AssistantTool } from '../llm/AssistantTool'
import { ConflictsTool, StructureTool } from '../tools/tools'
import { InfoTool } from '../tools/InfoTool'
import { DockerTool } from '../docker/DockerTool'
import { openConflict } from '../conflict/ConflictDialog'
import { openSettings } from '../settings/SettingsModal'
import { actions, registerAction, runAction, shortcutOf } from '../keys/bindings'
import { contextMenu, focusBeforeMenus, prompt, type MenuItem } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { refreshConnections } from '../db/api'
import { toast } from '../ui/toast'
import { t, tn } from '../i18n'
import { ProjectBar } from './ProjectBar'
import { SearchEverywhereHost, searchEverywhere } from '../popups/SearchEverywhere'

/** Tools of the four zones; their placement comes from state/zones. */
export const toolPanels: Record<string, { label: string; icon: string; component: () => JSX.Element; badge?: () => number }> = {
  explorer: { label: 'Explorer', icon: 'files', component: Explorer },
  search: { label: 'Search', icon: 'search', component: GlobalSearch },
  git: { label: 'Git', icon: 'branch', component: GitPanel },
  kanban: { label: 'Kanban', icon: 'kanban', component: KanbanPanel },
  console: { label: 'Console', icon: 'terminal', component: ConsoleTool },
  problems: { label: 'Problems', icon: 'problems', component: ProblemsTool, badge: problemCount },
  docker: { label: 'Docker', icon: 'docker', component: DockerTool },
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
    registerAction('view.closeOthers', () => {
      const l = activeLeaf()
      l.tabs.filter((x) => x !== l.active).forEach((x) => closeTab(l.id, x))
    }),
    registerAction('view.closeAll', () => {
      const l = activeLeaf()
      ;[...l.tabs].forEach((x) => closeTab(l.id, x))
    }),
    // New file or folder: beside the active file, else at the root.
    registerAction('file.newFile', () => void createIn(newEntryDir(), false)),
    registerAction('file.newFolder', () => void createIn(newEntryDir(), true)),
    registerAction('menu.open', () => document.querySelector<HTMLElement>('.menubar .menu-btn')?.click()),
    registerAction('view.nextTab', () => cycleTab(1)),
    registerAction('view.prevTab', () => cycleTab(-1)),
    registerAction('nav.back', () => navHistory(-1)),
    registerAction('nav.forward', () => navHistory(1)),
    registerAction('settings.open', () => openSettings()),
    registerAction('kanban.open', () => openBoard()),
    registerAction('search.everywhere', () => searchEverywhere('all')),
    registerAction('palette.open', () => searchEverywhere('actions')),
    registerAction('nav.gotoFile', () => searchEverywhere('files')),
    registerAction('nav.gotoSymbol', () => searchEverywhere('symbols')),
    registerAction('conflict.resolve', () => {
      const list = conflictedDocs()
      if (!list.length) return false
      openConflict(list[0])
    }),
  ]
  onCleanup(() => offs.forEach((f) => f()))
}

function newEntryDir() {
  const path = activeTab()?.kind === 'file' ? activeTab()!.path! : ''
  return path && relPath(path) !== path ? path.slice(0, path.lastIndexOf('/')) : root()
}

// Where the keyboard lands in a tool given the focus: its terminal, its prompt, the selected
// row of its tree, its first field, else its first button.
// A tool just shown may still be loading: the preferred targets are awaited a few frames.
const focusTargets = ['.xterm-helper-textarea', '.ai-composer textarea', '[role=tree] [aria-selected=true]', '[role=tree] [tabindex]', '.tree-row.active', '.tree-row', 'input:not([type=checkbox]), textarea']
const fallbackTarget = '.panel-body button:not(:disabled), .tool-body button:not(:disabled), [tabindex="0"]:not(.detach), button:not(:disabled):not(.detach)'

function focusZone(zone: Zone, tries = 10) {
  const el = document.querySelector<HTMLElement>(`.zone-${zone}`)
  if (!el) return
  const visible = (x: HTMLElement | null) => x && x.offsetParent !== null
  const target = focusTargets.map((s) => el.querySelector<HTMLElement>(s)).find(visible)
  if (!target && tries > 0) return void requestAnimationFrame(() => focusZone(zone, tries - 1))
  ;(target ?? [...el.querySelectorAll<HTMLElement>(fallbackTarget)].find(visible) ?? el).focus()
  // The rows of a tool still loading can be rendered again: the focus is given back if it was lost.
  if (tries > 0) requestAnimationFrame(() => document.activeElement === document.body && focusPart() === zone && focusZone(zone, tries - 1))
}

/** Shortcut of a tool: shows it and gives it the focus; hides it when it has the focus already. */
function toggleToolFocus(id: string) {
  const z = zoneOf(session, id)
  if (!z) return false
  if (shownIn(session, z) === id && focusPart() === z) {
    mutate((s) => toggleTool(s, id))
    setFocusPart('editor')
    focusEditor()
    return
  }
  mutate((s) => showTool(s, id))
  setFocusPart(z)
  requestAnimationFrame(() => focusZone(z))
}

/** Escape in a tool gives the focus back to the editor (not in a terminal, nor when the tool used the key). */
function escapeToEditor(e: KeyboardEvent) {
  if (e.key !== 'Escape' || e.defaultPrevented || e.ctrlKey || e.altKey || e.shiftKey || e.metaKey) return
  const target = e.target as HTMLElement
  if (!target.closest?.('.zone') || target.closest('.xterm')) return
  e.preventDefault()
  setFocusPart('editor')
  focusEditor()
}

// ---------- menu bar ----------

// Actions of each menu; '-' draws a separator.
const menus: [string, string[]][] = [
  ['menu|File', ['file.newFile', 'file.newFolder', '-', 'file.save', 'file.saveAll', '-', 'nav.gotoFile', '-', 'view.closeTab', 'view.closeOthers', 'view.closeAll', '-', 'conflict.resolve', '-', 'settings.open']],
  ['menu|Edit', ['edit.undo', 'edit.redo', '-', 'edit.duplicateLine', 'edit.deleteLine', 'edit.toggleComment', '-', 'edit.nextOccurrence', 'edit.allOccurrences', '-', 'search.find', 'search.global']],
  ['menu|Navigate', ['search.everywhere', '-', 'nav.back', 'nav.forward', '-', 'nav.gotoLine', 'nav.gotoSymbol', 'nav.fileStructure', '-', 'nav.related', 'nav.test']],
  ['menu|Code', ['lsp.definition', 'lsp.implementation', 'lsp.typeDefinition', 'lsp.superMethod', 'lsp.references', 'lsp.hover', '-', 'lsp.rename', 'lsp.format', '-', 'edit.fold', 'edit.unfold', 'edit.foldAll', 'edit.unfoldAll']],
  ['menu|View', ['view.splitRight', 'view.splitDown', '-', 'view.toggleLeft', 'view.toggleRight', 'view.toggleBottom', 'view.resetTools', '-', 'view.visualFocus', 'view.focusOutline', 'view.focusDim', '-', 'view.whitespace', '-', 'console.new', 'palette.open']],
  ['menu|Tools', ['tool.explorer', 'tool.search', 'tool.git', 'tool.kanban', '-', 'tool.assistant', 'tool.database', 'tool.structure', 'tool.conflicts', 'tool.info', '-', 'tool.console', 'tool.problems', 'tool.docker']],
]

// Menu entries switching a setting, shown with a check box.
const menuChecks: Record<string, () => boolean> = {
  'view.visualFocus': () => settings.visualFocus,
  'view.focusOutline': () => settings.focusOutline,
  'view.focusDim': () => settings.focusDim,
  'view.whitespace': () => settings.editor.showWhitespace,
}

function openMenu(e: MouseEvent, ids: string[]) {
  const btn = e.currentTarget as HTMLElement
  const r = btn.getBoundingClientRect()
  // The action runs where the focus was (the menu buttons do not take it), the editor otherwise.
  const prev = focusBeforeMenus()
  const items: MenuItem[] = ids.map((id) =>
    id === '-'
      ? { separator: true, label: '' }
      : {
          label: t(actions.find((a) => a.id === id)?.label ?? id),
          hint: shortcutOf(id),
          checked: menuChecks[id]?.(),
          action: () => {
            if (prev && prev !== document.body && prev.isConnected && !prev.closest('.menubar')) prev.focus()
            else focusEditor()
            requestAnimationFrame(() => runAction(id) || toast(t('Action not available here'), 'info'))
          },
        },
  )
  // Left and Right open the menu beside.
  const onSide = (dir: -1 | 1) => {
    const all = [...document.querySelectorAll<HTMLElement>('.menubar .menu-btn')]
    all[(all.indexOf(btn) + dir + all.length) % all.length]?.click()
  }
  contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.bottom + 2 }), items, { onSide })
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
            <button class="menu-btn" onMouseDown={(e) => e.preventDefault()} onClick={(e) => openMenu(e, ids)}>
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
      role="toolbar"
      aria-orientation="vertical"
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
    registerAction('view.focusDim', () => updateSettings((s) => (s.focusDim = !s.focusDim), 'Dim out of focus')),
    registerAction('view.whitespace', () => updateSettings((s) => (s.editor.showWhitespace = !s.editor.showWhitespace), 'Whitespace')),
    registerAction('console.new', () => void newConsole()),
    registerAction('view.toggleBottom', () => mutate((s) => toggleTool(s, 'console'))),
    ...Object.keys(toolPanels).map((id) => registerAction(`tool.${id}`, () => toggleToolFocus(id))),
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
  document.addEventListener('keydown', escapeToEditor)
  onCleanup(() => {
    offs.forEach((f) => f())
    document.removeEventListener('keydown', escapeToEditor)
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
      <div class="app" classList={{ 'visual-focus': settings.visualFocus, 'focus-outline': settings.focusOutline, 'focus-dim': settings.focusDim }}>
        <MenuBar />
        <NewTicketHost />
        <SearchEverywhereHost />
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
