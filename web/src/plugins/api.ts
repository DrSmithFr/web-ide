// The API a plugin's page module receives (docs/design/plugins.md): the only door to the core,
// so that a built-in plugin uses nothing an external one could not.
import { on, request } from '../pod/rpc'
import { fmtDate, t, tn } from '../i18n'
import { findLeaf, home, isLocal, mutate, openFile, openTab, openTextTab, session, setActivePane, type TabState } from '../state/project'
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
import { newConsole } from '../console/consoles'

export function makeApi(id: string) {
  return {
    id,
    /** Calls a method of the plugin's backend (<id>.<method>), in the project of the window. */
    rpc: <T = any>(method: string, params?: any) => request<T>(`${id}.${method}`, params),
    /** Events of the pod (fs.changed, git.changed, plugins.changed…); returns the unsubscribe. */
    on,
    t,
    tn,
    fmtDate,
    openFile,
    /** The project of the window: local, or on an SSH host; id: its main project (events carry it). */
    project: { isLocal, id: () => home()?.id },
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
    /** The terminal capability: a terminal or a task in the Console tool. */
    terminal: { open: newConsole },
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
    editor: { Doc, useView: useEditorView, setCursor: setCursorInfo },
    /** The keymap: a handler of an action while the view is shown, and the shortcut of an action. */
    keys: { register: registerAction, shortcut: shortcutOf },
    /** Port forwards and app previews are the core's, shown wherever a plugin needs them. */
    forwards: { Tab: ForwardsTab },
    previews: { list: previews, open: openPreview, close: closePreview, url: previewUrl, Tab: PreviewsTab },
    ui: { EmptyState, Icon, Modal, toast, errorToast, contextMenu, prompt, pick, copyText, parseAnsi, stripAnsi },
  }
}

export type PluginApi = ReturnType<typeof makeApi>
export type { MenuItem } from '../ui/overlay'
export type { TabState }
