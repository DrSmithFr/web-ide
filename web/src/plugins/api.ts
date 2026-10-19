// The API a plugin's page module receives (docs/design/plugins.md): the only door to the core,
// so that a built-in plugin uses nothing an external one could not.
import { on, request } from '../pod/rpc'
import { t } from '../i18n'
import { isLocal, mutate, openFile, openTab, session, type TabState } from '../state/project'
import { closePreview, openPreview, previews, previewUrl } from '../llm/previews'
import { ForwardsTab } from '../forwards/ForwardsTab'
import { PreviewsTab } from '../forwards/PreviewsTab'
import { EmptyState } from '../ui/EmptyState'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { contextMenu, prompt } from '../ui/overlay'
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
    openFile,
    /** The project of the window: local, or on an SSH host. */
    project: { isLocal },
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
    /** Opens a tab view of the plugin, with its serializable state; key tells two tabs apart. */
    openView: (view: string, opts: { title?: string; key?: string; state?: unknown } = {}) =>
      openTab({ kind: `plugin:${id}/${view}`, title: opts.title, key: opts.key, state: opts.state } as Omit<TabState, 'id'>),
    /** Port forwards and app previews are the core's, shown wherever a plugin needs them. */
    forwards: { Tab: ForwardsTab },
    previews: { list: previews, open: openPreview, close: closePreview, url: previewUrl, Tab: PreviewsTab },
    ui: { EmptyState, Icon, toast, errorToast, contextMenu, prompt, parseAnsi, stripAnsi },
  }
}

export type PluginApi = ReturnType<typeof makeApi>
export type { MenuItem } from '../ui/overlay'
