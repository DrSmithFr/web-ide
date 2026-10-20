// The Language servers plugin's door to the core: the api given at activation, its state in the
// desk (the tab of the Problems tool), and the parts of the api the components use by name.
// Nothing here imports the core itself.
import type { JSX } from 'solid-js'
import type { PluginApi } from '@ide/api'

export let api: PluginApi

export interface LanguagesDesk extends Record<string, unknown> {
  /** The tab of the Problems tool. */
  tab: 'problems' | 'output'
}

export let desk: ReturnType<typeof deskOf>
const deskOf = (a: PluginApi) => a.desk<LanguagesDesk>({ tab: 'problems' })

export function bindApi(a: PluginApi) {
  api = a
  desk = deskOf(a)
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const EmptyState = (p: Parameters<PluginApi['ui']['EmptyState']>[0]): JSX.Element => api.ui.EmptyState(p)
export const toast: PluginApi['ui']['toast'] = (...a) => api.ui.toast(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const pick: PluginApi['ui']['pick'] = (...a) => api.ui.pick(...a)
