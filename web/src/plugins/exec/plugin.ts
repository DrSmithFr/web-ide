// The Exec plugin's door to the core: the api given at activation, its state in the desk (the
// console shown, the order of the console tabs), and the parts of the api the components use by
// name. Nothing here imports the core itself.
import type { JSX } from 'solid-js'
import type { PluginApi } from '../api'

export let api: PluginApi

export interface ExecDesk extends Record<string, unknown> {
  /** The console shown in the Console tool. */
  active: string | null
  /** The order of the console tabs. */
  order: string[]
}

export let desk: ReturnType<typeof deskOf>
const deskOf = (a: PluginApi) => a.desk<ExecDesk>({ active: null, order: [] })

export function bindApi(a: PluginApi) {
  api = a
  desk = deskOf(a)
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const EmptyState = (p: Parameters<PluginApi['ui']['EmptyState']>[0]): JSX.Element => api.ui.EmptyState(p)
export const Modal = (p: Parameters<PluginApi['ui']['Modal']>[0]): JSX.Element => api.ui.Modal(p)
export const WorktreeChip = (p: Parameters<PluginApi['ui']['WorktreeChip']>[0]): JSX.Element => api.ui.WorktreeChip(p)
export const contextMenu: PluginApi['ui']['contextMenu'] = (...a) => api.ui.contextMenu(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
export const copyText: PluginApi['ui']['copyText'] = (...a) => api.ui.copyText(...a)
