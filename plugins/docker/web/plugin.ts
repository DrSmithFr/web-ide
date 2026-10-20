// The Docker plugin's door to the core: the api given at activation, and the few parts of it
// the components use by name (t, icons, menus…). Nothing here imports the core itself.
import type { JSX } from 'solid-js'
import type { PluginApi } from '@ide/api'

export let api: PluginApi

/** The state of the Docker tool in the desk: shown tab, chosen Compose profiles, tab of the detail pane. */
export interface DockerDesk extends Record<string, unknown> {
  tab: 'project' | 'host' | 'disk'
  profiles: string[]
  detail: 'infos' | 'logs'
}

export let desk: ReturnType<typeof deskOf>
const deskOf = (a: PluginApi) => a.desk<DockerDesk>({ tab: 'project', profiles: [], detail: 'infos' })

export function bindApi(a: PluginApi) {
  api = a
  desk = deskOf(a)
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const EmptyState = (p: Parameters<PluginApi['ui']['EmptyState']>[0]): JSX.Element => api.ui.EmptyState(p)
export const contextMenu: PluginApi['ui']['contextMenu'] = (...a) => api.ui.contextMenu(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const toast: PluginApi['ui']['toast'] = (...a) => api.ui.toast(...a)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
export const parseAnsi: PluginApi['ui']['parseAnsi'] = (...a) => api.ui.parseAnsi(...a)
export const stripAnsi: PluginApi['ui']['stripAnsi'] = (...a) => api.ui.stripAnsi(...a)
