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
import { refreshGit } from '../state/git'
import { DatabaseTool } from '../db/DatabaseTool'
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

export const leftPanels: Record<string, { label: string; icon: string; component: () => JSX.Element }> = {
  explorer: { label: 'Explorateur', icon: 'files', component: Explorer },
  search: { label: 'Recherche', icon: 'search', component: GlobalSearch },
  git: { label: 'Git', icon: 'branch', component: GitPanel },
  connections: { label: 'Connexions', icon: 'plug', component: Connections },
}

export const rightPanels: Record<string, { label: string; icon: string; component: () => JSX.Element }> = {
  database: { label: 'Database explorer', icon: 'database', component: DatabaseTool },
  structure: { label: 'Structure', icon: 'outline', component: StructureTool },
  conflicts: { label: 'Conflits', icon: 'conflict', component: ConflictsTool },
  extensions: { label: 'Extensions', icon: 'puzzle', component: ExtensionsTool },
  properties: { label: 'Propriétés', icon: 'info', component: PropertiesTool },
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
        const v = await prompt({ title: 'Connexion SSH', label: e.message, password: true })
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
    placeholder: 'Commande…',
    items: actions.map((a) => ({ label: a.label, detail: a.category, hint: shortcutOf(a.id), value: a.id })),
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
    placeholder: 'Aller au fichier (nom ou chemin, recherche approximative)',
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
    toast('Aucun serveur de langage disponible pour chercher des symboles', 'info')
    return
  }
  const loc = await pick<lspc.Location>({
    placeholder: 'Aller au symbole (classe, fonction, méthode…)',
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
  ['Fichier', ['file.save', 'file.saveAll', 'nav.gotoFile', 'conflict.resolve', 'settings.open']],
  ['Édition', ['edit.undo', 'edit.redo', 'edit.duplicateLine', 'edit.deleteLine', 'edit.toggleComment', 'search.find', 'search.global']],
  ['Navigation', ['nav.back', 'nav.forward', 'nav.gotoLine', 'nav.gotoSymbol', 'nav.fileStructure', 'nav.related', 'nav.test']],
  ['Code', ['lsp.definition', 'lsp.implementation', 'lsp.typeDefinition', 'lsp.superMethod', 'lsp.references', 'lsp.hover']],
  ['Affichage', ['view.splitRight', 'view.splitDown', 'view.closeTab', 'view.toggleLeft', 'view.toggleRight', 'view.toggleBottom', 'console.new', 'palette.open']],
]

function openMenu(e: MouseEvent, ids: string[]) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  const items: MenuItem[] = ids.map((id) => ({
    label: actions.find((a) => a.id === id)?.label ?? id,
    hint: shortcutOf(id),
    action: () => {
      focusActiveEditor()
      requestAnimationFrame(() => runAction(id) || toast('Action indisponible ici', 'info'))
    },
  }))
  contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.bottom + 2 }), items)
}

export function PodStatus() {
  return (
    <div class="pod-status" title={`Pod : ${podState()}`}>
      <span class={`dot dot-${podState()}`} />
      <span class="rate" title="Débit descendant (pod → page)">↓ {formatRate(podRates().down)}</span>
      <span class="rate" title="Débit montant (page → pod)">↑ {formatRate(podRates().up)}</span>
    </div>
  )
}

function MenuBar() {
  const conflicts = () => (docsVersion(), conflictedDocs().length)
  return (
    <header class="menubar">
      <button class="icon-btn" title={`Réglages (${shortcutOf('settings.open')})`} onClick={() => openSettings()}>
        <Icon name="gear" />
      </button>
      <button class="icon-btn" title="Projets" onClick={() => navigate('/')}>
        <Icon name="home" />
      </button>
      <nav class="menus">
        <For each={menus}>
          {([label, ids]) => (
            <button class="menu-btn" onClick={(e) => openMenu(e, ids)}>
              {label}
            </button>
          )}
        </For>
      </nav>
      <span class="project-name" title={project()?.path}>
        {project()?.name}
        <Show when={project()?.type === 'ssh'}>
          <span class="badge">ssh</span>
        </Show>
      </span>
      <span class="grow" />
      <Show when={conflicts()}>
        <button class="badge warn" onClick={() => mutate((s) => (s.right.panel = 'conflicts'))}>
          {conflicts()} conflit(s)
        </button>
      </Show>
      <Show when={cursorInfo()}>
        {(c) => (
          <span class="cursor-info">
            {c().line}:{c().col}
            {c().sel ? ` (${c().sel} car.)` : ''} · {c().lang}
          </span>
        )}
      </Show>
      <PodStatus />
    </header>
  )
}

// ---------- rails & side panels ----------

function Rail(props: { side: 'left' | 'right'; panels: typeof leftPanels }) {
  const current = () => session[props.side].panel
  const toggle = (id: string) => mutate((s) => (s[props.side].panel = s[props.side].panel === id ? null : id))
  return (
    <nav class={`rail rail-${props.side}`} aria-label={props.side === 'left' ? 'Panneaux' : 'Tools'}>
      <For each={Object.entries(props.panels)}>
        {([id, p]) => (
          <button class="rail-btn" classList={{ active: current() === id }} title={p.label} aria-pressed={current() === id} onClick={() => toggle(id)}>
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
  const detach = () => window.open(`/project/${project()!.id}/tool/${id()}`, `tool-${id()}`, 'popup,width=420,height=760')
  return (
    <aside class={`side side-${props.side}`} style={{ width: `${session[props.side].width}px` }}>
      <button class="icon-btn detach" title="Ouvrir dans une fenêtre" onClick={detach}>
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
      setReady(true)
      refreshGit(0)
      document.title = `${project()?.name} · Web IDE`
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
      toast('Le projet a été fermé (modifié ou supprimé)', 'warn')
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
          <Show when={error()} fallback={<p class="muted">Ouverture du projet…</p>}>
            <p class="danger">{error()}</p>
            <button class="btn" onClick={() => navigate('/')}>
              Retour aux projets
            </button>
          </Show>
        </div>
      }
    >
      <div class="app">
        <MenuBar />
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
              <Icon name="terminal" size={13} /> Consoles
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
