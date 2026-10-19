// The API a plugin's page module receives (docs/design/plugins.md): the only door to the core,
// so that a built-in plugin uses nothing an external one could not.
import { notifyIn, notifyOn, on, openChannel, request, requestIn, type Channel } from '../pod/rpc'
import { fmtDate, t, tn } from '../i18n'
import { attached, findLeaf, home, isLocal, mutate, openFile, openTab, openTextTab, project, root, session, setActivePane, type TabState } from '../state/project'
import { showTool } from '../state/zones'
import { detachTab, openTile, tileMode } from '../state/tiles'
import { phone } from '../state/mobile'
import { fontFor, settings } from '../state/settings'
import { accentOf, themeById } from '../settings/themes'
import { WorktreeChip } from '../ui/WorktreeChip'
import { dropClasses, dropIndex, moveItem, setDropAt } from '../ui/tabDrop'
import { EditorView } from '../editor/view'
import { capability, provide, type Capabilities } from './capabilities'
import { setDynamicActions } from '../keys/bindings'
import { closePreview, openPreview, previews, previewUrl } from '../llm/previews'
import { ForwardsTab } from '../forwards/ForwardsTab'
import { PreviewsTab } from '../forwards/PreviewsTab'
import { EmptyState } from '../ui/EmptyState'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { contextMenu, Modal, pick, prompt } from '../ui/overlay'
import { copyText } from '../ui/clipboard'
import { setCursorInfo } from '../ui/status'
import { useEditorView } from '../ui/EditorArea'
import { Doc } from '../editor/doc'
import { registerAction, shortcutOf } from '../keys/bindings'
import { parseAnsi, stripAnsi } from '../ui/ansi'

export function makeApi(id: string) {
  const full = (method: string) => (method.includes('.') ? method : `${id}.${method}`)
  return {
    id,
    /**
     * Calls a method of the plugin's backend, in the project of the window: <id>.<method>, or the
     * method as named when it has a prefix (one of the namespaces of the manifest: console.create).
     */
    rpc: <T = any>(method: string, params?: any) => request<T>(full(method), params),
    /** The same in a worktree attached to the window, and a notification without answer. */
    rpcIn: <T = any>(project: string | undefined, method: string, params?: any) => requestIn<T>(project, full(method), params),
    notifyIn: (project: string | undefined, method: string, params?: any) => notifyIn(project, full(method), params),
    /** A channel of its own in the tunnel (a terminal's input and output; null on a WebSocket). */
    channel: { open: openChannel, notify: (ch: Channel | null, method: string, params?: any, project?: string) => notifyOn(ch, full(method), params, project) },
    /** Events of the pod (fs.changed, git.changed, plugins.changed…); returns the unsubscribe. */
    on,
    t,
    tn,
    fmtDate,
    openFile,
    /** The project of the window: local, or on an SSH host; id: its main project (events carry it). */
    project: {
      isLocal,
      id: () => home()?.id,
      /** The worktree shown in the window (the project itself, or one of its worktrees), and its root. */
      current: () => project()?.id,
      root,
      /** The worktrees attached to the window (the project included). */
      attached: () => Object.keys(attached),
    },
    /** Capabilities as services: the plugin's own (provide) and those of the others (use). */
    provide: <K extends keyof Capabilities>(name: K, impl: Capabilities[K]) => provide(id, name, impl),
    use: capability,
    /** Shows one of the plugin's tools in its zone. */
    showTool: (tool: string) => mutate((s) => showTool(s, tool)),
    /** Tiles of the Grid and Scroll modes, detached windows. */
    tiles: { active: tileMode, phone, open: openTile, detach: detachTab },
    /** The look of the IDE for what draws itself (a terminal): its font and colors. */
    theme: {
      font: () => ({ family: settings.font.family, size: fontFor().size }),
      terminal: () => {
        const th = themeById(settings.theme)
        const ui = th.ui
        return { background: ui['bg-2'], foreground: ui.fg, cursor: accentOf(th, settings.accent)[0], selectionBackground: ui['sel-bg'], black: ui['bg-3'] }
      },
      tabSize: () => settings.editor.tabSize,
    },
    /** The SSH settings of the pod (agent, keys), for a form offering an SSH tunnel. */
    ssh: { info: () => request('ssh.info') },
    /**
     * The plugin's state in the desk, saved with it (shown tab, choices…): get() reads it over
     * the defaults, set() changes it.
     */
    desk: <T extends Record<string, unknown>>(defaults: T) => ({
      get: (): T => ({ ...defaults, ...(session.plugins?.[id] as Partial<T> | undefined) }),
      set: (f: (s: T) => void) =>
        mutate((s) => {
          s.plugins ??= {}
          const cur = (s.plugins[id] = { ...defaults, ...s.plugins[id] }) as T
          f(cur)
        }),
    }),
    /** Opens a tab view of the plugin, with its serializable state; key tells two tabs apart. Returns the tab id. */
    openView: (view: string, opts: { title?: string; key?: string; state?: unknown } = {}) =>
      openTab({ kind: `plugin:${id}/${view}`, title: opts.title, key: opts.key, state: opts.state } as Omit<TabState, 'id'>),
    /** Changes the state of a tab of the plugin (saved in the desk, it goes with the tab). */
    setViewState: <T>(tabId: string, f: (s: T) => void) =>
      mutate((s) => {
        const tab = s.tabs[tabId]
        if (tab?.kind.startsWith(`plugin:${id}/`)) f((tab.state ??= {}) as T)
      }),
    /** A read-only text in a tab (a DDL, a definition). */
    openText: openTextTab,
    /** The panes of the editor area: whether a tab is the one shown in the focused pane. */
    panes: {
      isActive: (paneId: string, tabId: string) => session.activePane === paneId && findLeaf(session.layout, paneId)?.active === tabId,
      activate: setActivePane,
    },
    /** The text editor of the IDE, for a view editing a text of its own (a SQL console). */
    editor: { Doc, View: EditorView, useView: useEditorView, setCursor: setCursorInfo },
    /** The keymap: a handler of an action while the view is shown, and the shortcut of an action. */
    keys: { register: registerAction, shortcut: shortcutOf, setDynamic: setDynamicActions },
    /** Port forwards and app previews are the core's, shown wherever a plugin needs them. */
    forwards: { Tab: ForwardsTab },
    previews: { list: previews, open: openPreview, close: closePreview, url: previewUrl, Tab: PreviewsTab },
    ui: { EmptyState, Icon, Modal, toast, errorToast, contextMenu, prompt, pick, copyText, parseAnsi, stripAnsi, WorktreeChip, tabDrop: { dropClasses, dropIndex, moveItem, setDropAt } },
  }
}

export type PluginApi = ReturnType<typeof makeApi>
export type { MenuItem } from '../ui/overlay'
export type { TabState }
