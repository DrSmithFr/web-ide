// The API a plugin's page module receives (docs/design/plugins.md): the only door to the core,
// so that a built-in plugin uses nothing an external one could not.
import { on, request } from '../pod/rpc'
import { t } from '../i18n'
import { openFile, openTab, type TabState } from '../state/project'
import { closePreview, openPreview, previews, previewUrl } from '../llm/previews'
import { ForwardsTab } from '../docker/ForwardsTab'
import { PreviewsTab } from '../docker/PreviewsTab'
import { EmptyState } from '../ui/EmptyState'
import { Icon } from '../ui/icons'
import { toast } from '../ui/toast'

export function makeApi(id: string) {
  return {
    id,
    /** Calls a method of the plugin's backend (<id>.<method>), in the project of the window. */
    rpc: <T = any>(method: string, params?: any) => request<T>(`${id}.${method}`, params),
    /** Events of the pod (fs.changed, git.changed, plugins.changed…); returns the unsubscribe. */
    on,
    t,
    openFile,
    /** Opens a tab view of the plugin, with its serializable state; key tells two tabs apart. */
    openView: (view: string, opts: { title?: string; key?: string; state?: unknown } = {}) =>
      openTab({ kind: `plugin:${id}/${view}`, title: opts.title, key: opts.key, state: opts.state } as Omit<TabState, 'id'>),
    /** Port forwards and app previews are the core's, shown wherever a plugin needs them. */
    forwards: { Tab: ForwardsTab },
    previews: { list: previews, open: openPreview, close: closePreview, url: previewUrl, Tab: PreviewsTab },
    ui: { EmptyState, Icon, toast },
  }
}

export type PluginApi = ReturnType<typeof makeApi>
