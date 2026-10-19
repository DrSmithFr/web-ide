// The Database explorer's door to the core: the api given at activation, and the parts of it
// the components use by name (t, icons, dialogs…). Nothing here imports the core itself.
import type { JSX } from 'solid-js'
import type { PluginApi, TabState } from '../api'

export let api: PluginApi

export function bindApi(a: PluginApi) {
  api = a
}

/** The state of a console tab (plugin:database/sql) or a table tab (plugin:database/table). */
export interface ViewState {
  connId: string
  db: string
  table?: string
  /** Text of a console, saved with its tab. */
  text?: string
}
export const viewState = (tab: TabState) => (tab.state ?? {}) as ViewState

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const tn: PluginApi['tn'] = (...a) => api.tn(...a)
export const fmtDate: PluginApi['fmtDate'] = (...a) => api.fmtDate(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const EmptyState = (p: Parameters<PluginApi['ui']['EmptyState']>[0]): JSX.Element => api.ui.EmptyState(p)
export const Modal = (p: Parameters<PluginApi['ui']['Modal']>[0]): JSX.Element => api.ui.Modal(p)
export const contextMenu: PluginApi['ui']['contextMenu'] = (...a) => api.ui.contextMenu(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const pick: PluginApi['ui']['pick'] = (...a) => api.ui.pick(...a)
export const toast: PluginApi['ui']['toast'] = (...a) => api.ui.toast(...a)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
export const copyText: PluginApi['ui']['copyText'] = (...a) => api.ui.copyText(...a)
