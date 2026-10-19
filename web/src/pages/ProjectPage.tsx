// Main window of a project: menu bar, icon rails, side panels, editor area, bottom tools.
import { podPath } from '../app/base'
import { createEffect, createRoot, createSignal, For, type JSX, on as track, onCleanup, onMount, Show, untrack } from 'solid-js'
import { createMutable } from 'solid-js/store'
import { Dynamic } from 'solid-js/web'
import { RpcError, formatRate, on as onPod, podRates, podState } from '../pod/rpc'
import {
  activeLeaf, activeTab, closeTab, conflictedDocs, cycleTab, docsVersion, home, mutate, navigate as navHistory, openProject, project, relPath, reopenProject, root,
  openFile, closeEmptyPane, saveAll, session, setConflictOpener, setSize, sizesFor, splitPane, closeProject, moveTabToDesk, switchDesk,
} from '../state/project'
import { desk, desks, onDeskGone } from '../state/desks'
import { cycleWidth, focusDirection, moveColumnBy, scrollMode, stackTile, tileMode, unstackTile, openTile, registerTileTools, resizeTile, showToolTile, startResize, swapDirection, tileTools, toggleSplit } from '../state/tiles'
import { pluginEnabled, pluginStates, pluginTools, setPluginStates } from '../plugins/registry'
import { tileLauncher } from '../ui/tileLauncher'
import { deskPath, navigate, route } from '../app/router'
import { defaultPlacement, moveTool, normalizePlacement, showTool, shownIn, toggleTool, toolsIn, zoneOf, zones, type Zone } from '../state/zones'
import { focusEditor, focusPart, setFocusPart, trackFocus } from '../state/focus'
import { focusEditorQuietly, isTyping, lockEditor, mobileView, phone, revealCaret, setMobileView } from '../state/mobile'
import { settings, updateSettings } from '../state/settings'
import { goHome } from '../app/home'
import { EditorArea } from '../ui/EditorArea'
import { ConsoleTool, ProblemsTool, navigateConsoles, newConsole, problemCount, refreshConsoles, setConsoleList } from '../console/consoles'
import { loadCommands } from '../console/commands'
import { createIn, Explorer } from '../panels/Explorer'
import { GlobalSearch, focusGlobalSearch } from '../panels/GlobalSearch'
import { GitPanel } from '../panels/git/GitPanel'
import { KanbanPanel } from '../kanban/Panel'
import { NewTicketHost } from '../kanban/Board'
import { CommandsHost } from '../console/CommandsModal'
import { openBoard, openMilestone, openObjective, openTicket } from '../kanban/state'
import { refreshGit } from '../state/git'
import { DatabaseTool } from '../db/DatabaseTool'
import { AssistantTool } from '../llm/AssistantTool'
import { ConflictsTool, StructureTool } from '../tools/tools'
import { InfoTool } from '../tools/InfoTool'
import { ForwardsTool } from '../forwards/ForwardsTool'
import { AgentsTool } from '../agents/AgentsTool'
import { QuestionModal, QuestionsTool } from '../questions/QuestionsTool'
import { questions, refreshQuestions, resetQuestions, showQuestion } from '../questions/state'
import { refreshAgents } from '../agents/state'
import { openConflict } from '../conflict/ConflictDialog'
import { docsIndex, openDoc } from '../state/docs'
import { openSettings } from '../settings/SettingsModal'
import { actionOf, registerAction, runAction, shortcutOf } from '../keys/bindings'
import { onMouseNav } from '../keys/mouse'
import { navigateChats, openChat } from '../llm/assistant'
import { contextMenu, focusBeforeMenus, prompt, type MenuItem } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { refreshConnections } from '../db/api'
import { offerIdeUntrack } from '../state/ideTracked'
import { toast } from '../ui/toast'
import { t, tn } from '../i18n'
import { ProjectBar } from './ProjectBar'
import { SearchEverywhereHost, searchEverywhere } from '../popups/SearchEverywhere'
import { RecentFilesHost, recentFiles } from '../popups/RecentFiles'
import { ClipboardHistoryHost, pasteFromHistory } from '../popups/ClipboardHistory'
import { newDoodle } from '../llm/Composer'
import { DoodleHost } from '../llm/doodle/DoodleModal'

/** Tools of the four zones, the core's then those of the enabled plugins; their placement comes from state/zones. */
export const toolPanels: Record<string, { label: string; icon: string; component: () => JSX.Element; badge?: () => number }> = createMutable({
  explorer: { label: 'Explorer', icon: 'files', component: Explorer },
  search: { label: 'Search', icon: 'search', component: GlobalSearch },
  git: { label: 'Git', icon: 'branch', component: GitPanel },
  kanban: { label: 'Kanban', icon: 'kanban', component: KanbanPanel },
  console: { label: 'Console', icon: 'terminal', component: ConsoleTool },
  problems: { label: 'Problems', icon: 'problems', component: ProblemsTool, badge: problemCount },
  database: { label: 'Database explorer', icon: 'database', component: DatabaseTool },
  assistant: { label: 'Assistant', icon: 'sparkle', component: AssistantTool },
  agents: { label: 'Agents', icon: 'comment', component: AgentsTool },
  questions: { label: 'Questions', icon: 'question', component: QuestionsTool, badge: () => questions().length },
  structure: { label: 'Structure', icon: 'outline', component: StructureTool },
  conflicts: { label: 'Conflicts', icon: 'conflict', component: ConflictsTool },
  info: { label: 'Infos', icon: 'info', component: InfoTool },
})

registerTileTools(toolPanels)

// The tools of the enabled plugins join the zones, the tiles and the keymap; a plugin disabled
// leaves them, its place in the zones kept.
createRoot(() =>
  createEffect(() => {
    const list = pluginTools()
    untrack(() => {
      for (const p of list) {
        toolPanels[p.id] = { label: p.label, icon: p.icon, component: p.component, badge: p.badge }
        tileTools[p.id] = { label: p.label, icon: p.icon, component: p.component }
      }
    })
    const offs = list.map((p) => registerAction(`tool.${p.id}`, () => toggleToolFocus(p.id)))
    onCleanup(() => {
      offs.forEach((off) => off())
      for (const p of list) {
        delete toolPanels[p.id]
        delete tileTools[p.id]
      }
    })
  }),
)

// The port forwards and previews of the core have a tool of their own where the Docker plugin,
// which shows them, is disabled (known but off: before project.open nothing is shown).
const forwardsTool = { label: 'Port forwards and previews', icon: 'docker', component: ForwardsTool }
createRoot(() =>
  createEffect(() => {
    const own = pluginStates().length > 0 && !pluginEnabled('docker')
    untrack(() => {
      if (own) toolPanels.forwards = tileTools.forwards = forwardsTool
      else {
        delete toolPanels.forwards
        delete tileTools.forwards
      }
    })
    if (own) onCleanup(registerAction('tool.forwards', () => toggleToolFocus('forwards')))
  }),
)

/** Opens a project, asking for the SSH password / passphrase when the pod needs one. */
export async function openWithAuth(id: string, desk = 1) {
  let creds: { password?: string; passphrase?: string } | undefined
  for (;;) {
    try {
      const r = await openProject(id, creds, desk)
      setPluginStates(r.plugins)
      setConsoleList(r.consoles)
      refreshConnections()
      offerIdeUntrack(r.ideTracked)
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
      else closeEmptyPane(l.id) // an empty tile
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
    registerAction('nav.previousConversation', () => void navigateChats(-1)),
    registerAction('nav.nextConversation', () => void navigateChats(1)),
    registerAction('nav.previousConsole', () => navigateConsoles(-1)),
    registerAction('nav.nextConsole', () => navigateConsoles(1)),
    // The side buttons of the mouse move in the history of the focused part.
    onMouseNav((dir) => {
      const part = focusPart()
      const tool = part === 'editor' ? 'editor' : shownIn(session, part)
      if (tool === 'editor') navHistory(dir)
      else if (tool === 'assistant') void navigateChats(dir)
      else if (tool === 'console') navigateConsoles(dir)
    }),
    registerAction('settings.open', () => openSettings()),
    registerAction('kanban.open', () => openBoard()),
    registerAction('help.docs', () => openDoc(docsIndex)),
    registerAction('help.guide', () => openDoc('docs/guide.md')),
    registerAction('help.glossary', () => openDoc('GLOSSARY.md')),
    registerAction('help.shortcuts', () => openSettings('keys')),
    registerAction('search.everywhere', () => searchEverywhere('all')),
    registerAction('palette.open', () => searchEverywhere('actions')),
    registerAction('nav.gotoFile', () => searchEverywhere('files')),
    registerAction('nav.gotoSymbol', () => searchEverywhere('symbols')),
    registerAction('nav.recentFiles', () => recentFiles('recent')),
    registerAction('nav.switcher', () => recentFiles('switcher')),
    registerAction('edit.pasteHistory', () => pasteFromHistory()),
    registerAction('assistant.doodle', () => {
      mutate((s) => showTool(s, 'assistant'))
      newDoodle()
    }),
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
const focusTargets = ['.xterm-helper-textarea', '.ai-composer .ed-content', '[role=tree] [aria-selected=true]', '[role=tree] [tabindex]', '.tree-row.active', '.tree-row', 'input:not([type=checkbox]), textarea']
const fallbackTarget = '.panel-body button:not(:disabled), .tool-body button:not(:disabled), [tabindex="0"]:not(.detach), button:not(:disabled):not(.detach)'

function focusZone(zone: Zone, tries = 10, from = document.activeElement) {
  const el = document.querySelector<HTMLElement>(`.zone-${zone}`)
  if (!el) return
  // A phone does not focus a field: the keyboard would open (the user touches it to type).
  const visible = (x: HTMLElement | null) => x && x.offsetParent !== null && !(phone() && isTyping(x))
  const target = focusTargets.map((s) => el.querySelector<HTMLElement>(s)).find(visible)
  // While it waits, the user may put the focus elsewhere (a modal opened…): it stays there.
  const moved = () => document.activeElement !== from && document.activeElement !== document.body
  if (!target && tries > 0) return void requestAnimationFrame(() => !moved() && focusZone(zone, tries - 1, from))
  ;(target ?? (phone() ? el : ([...el.querySelectorAll<HTMLElement>(fallbackTarget)].find(visible) ?? el))).focus()
  // The rows of a tool still loading can be rendered again: the focus is given back if it is
  // lost in the next frames.
  if (tries > 0) keepZoneFocus(zone, 30)
}

function keepZoneFocus(zone: Zone, frames: number) {
  requestAnimationFrame(() => {
    if (focusPart() !== zone) return
    if (document.activeElement === document.body) focusZone(zone, 0)
    else if (frames > 0) keepZoneFocus(zone, frames - 1)
  })
}

/** Shortcut of a tool: shows it and gives it the focus; hides it when it has the focus already. */
function toggleToolFocus(id: string) {
  if (!toolPanels[id]) return false
  // A Grid desk has no zones: the tool is a tile.
  if (tileMode() && !phone()) return showToolTile(id)
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

/** Tools of the switcher, in the order of the rails. */
const switcherTools = () =>
  zones.flatMap((z) => toolsIn(session, z)).filter((id) => toolPanels[id]).map((id) => ({ id, label: toolPanels[id].label, icon: toolPanels[id].icon, shortcut: shortcutOf(`tool.${id}`) }))

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
  ['menu|Edit', ['edit.undo', 'edit.redo', '-', 'edit.pasteHistory', '-', 'edit.duplicateLine', 'edit.deleteLine', 'edit.toggleComment', '-', 'edit.nextOccurrence', 'edit.allOccurrences', '-', 'search.find', 'search.global']],
  ['menu|Navigate', ['search.everywhere', 'nav.recentFiles', '-', 'nav.back', 'nav.forward', '-', 'nav.gotoLine', 'nav.gotoSymbol', 'nav.fileStructure', '-', 'nav.related', 'nav.test']],
  ['menu|Code', ['lsp.definition', 'lsp.implementation', 'lsp.typeDefinition', 'lsp.superMethod', 'lsp.references', 'lsp.hover', '-', 'lsp.rename', 'lsp.format', '-', 'edit.fold', 'edit.unfold', 'edit.foldAll', 'edit.unfoldAll']],
  ['menu|View', ['view.splitRight', 'view.splitDown', '-', 'view.toggleLeft', 'view.toggleRight', 'view.toggleBottom', 'view.resetTools', '-', 'view.visualFocus', 'view.focusOutline', 'view.focusDim', '-', 'view.whitespace', 'view.wordWrap', 'view.markdown', '-', 'console.new', 'palette.open']],
  ['menu|Tools', ['tool.explorer', 'tool.search', 'tool.git', 'tool.kanban', '-', 'tool.assistant', 'tool.agents', 'tool.questions', 'tool.database', 'tool.structure', 'tool.conflicts', 'tool.info', '-', 'tool.console', 'tool.problems', '@tools']],
  ['menu|Help', ['help.docs', 'help.guide', 'help.glossary', '-', 'help.shortcuts']],
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
  // @tools: the tools of the enabled plugins, and the core's that show only without them.
  ids = ids.flatMap((id) => (id === '@tools' ? [...pluginTools().map((p) => `tool.${p.id}`), ...(toolPanels.forwards ? ['tool.forwards'] : [])] : [id]))
  const items: MenuItem[] = ids.map((id) =>
    id === '-'
      ? { separator: true, label: '' }
      : {
          label: t(actionOf(id)?.label ?? id),
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
  // Phone: three rows, the project and the status, the menus, the icons of the views.
  const head = () => (
    <>
      <button class="icon-btn" title={t('Projects')} onClick={() => void goHome()}>
        <Icon name="home" />
      </button>
      <ProjectBar />
    </>
  )
  const tail = () => (
    <>
      <Show when={conflicts()}>
        <button class="badge warn" onClick={() => mutate((s) => (s.right.panel = 'conflicts'))}>
          {tn(conflicts(), '{n} conflict', '{n} conflicts')}
        </button>
      </Show>
      <PodStatus />
      <button class="icon-btn" title={`${t('Settings')} (${shortcutOf('settings.open')})`} onClick={() => openSettings()}>
        <Icon name="gear" />
      </button>
    </>
  )
  const menuButtons = () => (
    <For each={menus}>
      {([label, ids]) => (
        <button class="menu-btn" onMouseDown={(e) => e.preventDefault()} onClick={(e) => openMenu(e, ids)}>
          {t(label)}
        </button>
      )}
    </For>
  )
  return (
    <Show
      when={!phone()}
      fallback={
        <header class="menubar phone" data-testid="phone-bar">
          <div class="menubar-row">
            {head()}
            <span class="grow" />
            {tail()}
          </div>
          <nav class="menus">{menuButtons()}</nav>
          <MobileRail />
        </header>
      }
    >
    <header class="menubar">
      {head()}
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
      {tail()}
    </header>
    </Show>
  )
}

/** Phone: the editor and every tool in one row of icons; the view shown is highlighted. */
function MobileRail() {
  let el!: HTMLDivElement
  const tools = () => (['left', 'right', 'bottomLeft', 'bottomRight'] as Zone[]).flatMap((z) => toolsIn(session, z)).filter((id) => toolPanels[id])
  // The view shown gets the focus: the editor without the keyboard (a double tap opens it).
  const show = (id: string) => {
    setMobileView(id)
    if (id === 'editor') {
      setFocusPart('editor')
      requestAnimationFrame(focusEditorQuietly)
      return
    }
    mutate((s) => showTool(s, id))
    const z = zoneOf(session, id)
    if (z) {
      setFocusPart(z)
      requestAnimationFrame(() => focusZone(z))
    }
  }
  createEffect(() => {
    const id = mobileView()
    queueMicrotask(() => el?.querySelector<HTMLElement>(`[data-id="${id}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' }))
  })
  return (
    <div class="mobile-rail" ref={el} role="toolbar" aria-label={t('Views')} data-testid="mobile-rail">
      <button class="rail-btn" classList={{ active: mobileView() === 'editor' }} data-id="editor" title={t('Editor')} aria-pressed={mobileView() === 'editor'} onClick={() => show('editor')}>
        <Icon name="file" size={18} />
      </button>
      <span class="mobile-rail-sep" />
      <For each={tools()}>
        {(id) => (
          <button class="rail-btn" classList={{ active: mobileView() === id }} data-id={id} title={t(toolPanels[id].label)} aria-pressed={mobileView() === id} onClick={() => show(id)}>
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

/**
 * Phone: the view follows what the user opens. A tool shown by any way (shortcut, menu, the
 * assistant) comes to the front, a file opened brings the editor back, a tool closed too.
 */
function MobileSync() {
  // Another view: the editor is locked again.
  createEffect(() => mobileView() !== 'editor' && lockEditor())
  // Becoming a phone (a narrower window), the part in use stays in front.
  let wasPhone = phone()
  createEffect(() => {
    const now = phone()
    if (now && !wasPhone) {
      const part = focusPart()
      setMobileView(part !== 'editor' ? (shownIn(session, part) ?? 'editor') : 'editor')
    }
    wasPhone = now
  })
  // The tools restored with the session stay behind the editor.
  let prev: Record<string, string | null> | undefined
  createEffect(() => {
    const now = Object.fromEntries(zones.map((z) => [z, shownIn(session, z)]))
    if (phone() && prev) {
      for (const z of zones) if (now[z] && now[z] !== prev![z]) setMobileView(now[z]!)
      if (mobileView() !== 'editor' && !Object.values(now).includes(mobileView())) setMobileView('editor')
    }
    prev = now
  })
  let lastTab: string | undefined
  let started = false
  createEffect(() => {
    const tab = activeTab()
    const key = tab ? `${tab.kind}:${tab.path ?? tab.title}` : undefined
    if (phone() && started && key && key !== lastTab) setMobileView('editor')
    // Another file: the editor is locked again.
    if (started && key !== lastTab) lockEditor()
    lastTab = key
    started = true
  })
  return null
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
  const detach = () => window.open(podPath(`/project/${project()!.id}/tool/${id()}`), `tool-${id()}`, id() === 'assistant' ? 'popup,width=1100,height=820' : 'popup,width=420,height=760')
  const side = props.zone === 'left' || props.zone === 'right'
  return (
    <aside class={`zone zone-${props.zone}`} classList={{ side, [`side-${props.zone}`]: side, focused: focusPart() === props.zone }} data-tool={id()} data-focus={props.zone} tabIndex={phone() ? -1 : undefined} style={props.width ? { width: `${props.width}px` } : props.style}>
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
      <Resizer dir="y" onDrag={(d) => setSize('bottom', Math.max(100, Math.min(innerHeight - 200, sizesFor().bottom - d)))} />
      <div class="bottom" ref={el} style={{ height: `${sizesFor().bottom}px` }}>
        <Show when={left()}>
          <ZonePanel zone="bottomLeft" style={{ flex: both() ? `0 0 ${sizesFor().split * 100}%` : '1 1 0' }} />
        </Show>
        <Show when={both()}>
          <Resizer dir="x" onDrag={(d) => setSize('split', Math.max(0.15, Math.min(0.85, sizesFor().split + d / el.offsetWidth)))} />
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

  // The desk of the URL (the home page links to the desk last shown on this device).
  const urlDesk = () => {
    const r = route()
    return r.name === 'project' ? (r.desk ?? 1) : 1
  }
  onMount(async () => {
    try {
      await openWithAuth(props.id, urlDesk())
      // Opened from a ticket to run its conversation: show the assistant.
      if (new URLSearchParams(location.search).has('assistant')) {
        mutate((s) => (s.right.panel = 'assistant'))
        history.replaceState(null, '', location.pathname)
      }
      // A ticket link of Claude Code: /project/<id>?ticket=<n>.
      const ticket = Number(new URLSearchParams(location.search).get('ticket'))
      if (ticket > 0) {
        history.replaceState(null, '', location.pathname)
        openTicket(ticket)
      }
      const milestone = Number(new URLSearchParams(location.search).get('milestone'))
      if (milestone > 0) {
        history.replaceState(null, '', location.pathname)
        openMilestone(milestone)
      }
      const objective = Number(new URLSearchParams(location.search).get('objective'))
      if (objective > 0) {
        history.replaceState(null, '', location.pathname)
        openObjective(objective)
      }
      // A push notification of a conversation: /project/<id>?chat=<id> shows it in the assistant.
      const chatId = new URLSearchParams(location.search).get('chat')
      if (chatId) {
        history.replaceState(null, '', location.pathname)
        mutate((s) => (s.right.panel = 'assistant'))
        void openChat(chatId).catch(() => {})
      }
      // A push notification of a question of an agent: /project/<id>?questions shows the first one.
      await resetQuestions()
      if (new URLSearchParams(location.search).has('questions')) {
        history.replaceState(null, '', location.pathname)
        mutate((s) => showTool(s, 'questions'))
        showQuestion()
      }
      // Opened from a file link of Claude Code while no window had the project.
      const open = new URLSearchParams(location.search).get('open')
      if (open) {
        history.replaceState(null, '', location.pathname)
        const [, path, line] = /^(.*?)(?::(\d+))?$/.exec(open)!
        void openFile(Number(line) > 0 ? { path, line: Number(line) - 1 } : path)
      }
      // The URL names the desk shown (a removed desk of an old link opened desk 1).
      if (urlDesk() !== desk()) navigate(deskPath(props.id, desk()), true)
      setReady(true)
      refreshGit(0)
      void loadCommands() // their shortcuts and Search Everywhere entries
      window.name = `project-${props.id}` // see openWorktreeWindow
    } catch (e) {
      setError((e as Error).message)
    }
  })
  useProjectActions()
  // The URL is the desk shown: a desk link, the history or a shortcut switches the window.
  createEffect(
    track(urlDesk, (n) => {
      if (!ready() || n === desk()) return
      switchDesk(n).catch((e) => {
        toast((e as Error).message, 'warn')
        navigate(deskPath(props.id, desk()), true)
      })
    }),
  )
  onDeskGone(() => {
    toast(t('This desk was removed: back to desk 1'), 'warn')
    navigate(deskPath(props.id, 1), true)
  })
  const goDesk = (n: number) => {
    if (!desks.desks.some((d) => d.n === n)) return false
    navigate(deskPath(props.id, n))
  }
  const sendToDesk = (n: number) => {
    const l = activeLeaf()
    if (!l?.active || n === desk() || !desks.desks.some((d) => d.n === n)) return false
    void moveTabToDesk(l.id, l.active, n).catch((e) => toast((e as Error).message, 'warn'))
  }
  // A closed zone gives the focus back to the editor.
  createEffect(() => {
    const part = focusPart()
    if (part !== 'editor' && !shownIn(session, part)) setFocusPart('editor')
  })
  const offs = [
    registerAction('view.toggleLeft', () => mutate((s) => (s.left.panel = s.left.panel ? null : (toolsIn(s, 'left')[0] ?? null)))),
    registerAction('view.toggleRight', () => mutate((s) => (s.right.panel = s.right.panel ? null : (toolsIn(s, 'right')[0] ?? null)))),
    registerAction('view.resetTools', () => mutate((s) => (s.placement = normalizePlacement(defaultPlacement())))),
    registerAction('view.visualFocus', () => updateSettings((s) => (s.visualFocus = !s.visualFocus), 'Visual focus')),
    registerAction('view.focusOutline', () => updateSettings((s) => (s.focusOutline = !s.focusOutline), 'Focus outline')),
    registerAction('view.focusDim', () => updateSettings((s) => (s.focusDim = !s.focusDim), 'Dim out of focus')),
    registerAction('view.whitespace', () => updateSettings((s) => (s.editor.showWhitespace = !s.editor.showWhitespace), 'Whitespace')),
    registerAction('console.new', () => {
      if (tileMode() && !phone()) void newConsole({ show: false }).then((c) => c && openTile({ kind: 'console', console: c.id }))
      else void newConsole()
    }),
    registerAction('tile.launcher', () => void tileLauncher()),
    registerAction('tile.toggleSplit', () => toggleSplit()),
    registerAction('tile.resize', () => startResize()),
    registerAction('tile.growWidth', () => resizeTile('row')),
    registerAction('tile.shrinkWidth', () => resizeTile('row', -12)),
    registerAction('tile.growHeight', () => resizeTile('col')),
    registerAction('tile.shrinkHeight', () => resizeTile('col', -12)),
    // The columns of the Scroll mode.
    registerAction('tile.column.left', () => scrollMode() && moveColumnBy(-1)),
    registerAction('tile.column.right', () => scrollMode() && moveColumnBy(1)),
    registerAction('tile.column.width', () => scrollMode() && cycleWidth()),
    registerAction('tile.stack', () => scrollMode() && stackTile()),
    registerAction('tile.unstack', () => scrollMode() && unstackTile()),
    ...(['left', 'right', 'up', 'down'] as const).flatMap((d) => [registerAction(`tile.focus.${d}`, () => focusDirection(d)), registerAction(`tile.swap.${d}`, () => swapDirection(d))]),
    ...[1, 2, 3, 4, 5, 6, 7, 8, 9].flatMap((n) => [registerAction(`desk.go.${n}`, () => goDesk(n)), registerAction(`desk.send.${n}`, () => sendToDesk(n))]),
    registerAction('view.toggleBottom', () => mutate((s) => toggleTool(s, 'console'))),
    ...Object.keys(toolPanels).map((id) => registerAction(`tool.${id}`, () => toggleToolFocus(id))),
    registerAction('search.global', () => {
      mutate((s) => (s.left.panel = 'search'))
      requestAnimationFrame(focusGlobalSearch)
    }),
    onPod('pod.reconnected', async () => {
      await reopenProject()
      void refreshConsoles()
      void refreshAgents()
      void refreshQuestions()
      refreshConnections()
    }),
    // A worktree shown besides goes by itself (state/project).
    onPod('project.closed', (e: { id: string }) => {
      if (e.id !== home()?.id) return
      toast(t('The project was closed (changed or deleted)'), 'warn')
      void goHome()
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
            <button class="btn" onClick={() => void goHome()}>
              {t('Back to the projects')}
            </button>
          </Show>
        </div>
      }
    >
      <div class="app" classList={{ 'visual-focus': settings.visualFocus, 'focus-outline': settings.focusOutline, 'focus-dim': settings.focusDim, phone: phone() }}>
        <MenuBar />
        <MobileSync />
        <NewTicketHost />
        <CommandsHost />
        <SearchEverywhereHost />
        <RecentFilesHost tools={switcherTools} onTool={toggleToolFocus} />
        <ClipboardHistoryHost />
        <QuestionModal />
        <DoodleHost external onSettings={() => openSettings()} />
        <Show when={phone()}>
          {/* One view at a time, full screen: the editor stays mounted under the tools. */}
          <div class="workbench phone-body" onPointerDown={trackFocus} onFocusIn={trackFocus}>
            <main class="center" data-focus="editor" classList={{ hidden: mobileView() !== 'editor' }} onFocusIn={() => setTimeout(revealCaret, 300)}>
              <EditorArea />
            </main>
            <Show when={mobileView() !== 'editor' && zoneOf(session, mobileView())}>{(z) => <ZonePanel zone={z()} />}</Show>
          </div>
        </Show>
        <Show when={!phone() && tileMode()}>
          {/* Grid desk: the tree of tiles is the whole window (state/tiles.ts). */}
          <div class="workbench grid-desk" onPointerDown={trackFocus} onFocusIn={trackFocus}>
            <main class="center" data-focus="editor">
              <EditorArea />
            </main>
          </div>
        </Show>
        <Show when={!phone() && !tileMode()}>
        <div class="workbench" onPointerDown={trackFocus} onFocusIn={trackFocus}>
          <Rail side="left" />
          <div class="work">
            <div class="work-top">
              <Show when={shownIn(session, 'left')}>
                <ZonePanel zone="left" width={sizesFor().left} />
                <Resizer dir="x" onDrag={(d) => setSize('left', Math.max(160, Math.min(700, sizesFor().left + d)))} />
              </Show>
              <main class="center" data-focus="editor">
                <EditorArea />
              </main>
              <Show when={shownIn(session, 'right')}>
                <Resizer dir="x" onDrag={(d) => setSize('right', Math.max(200, Math.min(800, sizesFor().right - d)))} />
                <ZonePanel zone="right" width={sizesFor().right} />
              </Show>
            </div>
            <Show when={shownIn(session, 'bottomLeft') || shownIn(session, 'bottomRight')}>
              <BottomStrip />
            </Show>
          </div>
          <Rail side="right" />
        </div>
        </Show>
      </div>
    </Show>
  )
}
