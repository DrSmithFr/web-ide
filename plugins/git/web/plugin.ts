// The Git plugin's door to the core: the api given at activation, its state in the desk (the tab
// shown, the heights of the commit form and of the commit detail), and the parts of the api the
// components use by name. Nothing here imports the core itself.
import type { JSX } from 'solid-js'
import type { PluginApi } from '@ide/api'

export let api: PluginApi

export interface GitDesk extends Record<string, unknown> {
  tab: 'commit' | 'history'
  form: number
  detail: number
}

export let desk: ReturnType<typeof deskOf>
const deskOf = (a: PluginApi) => a.desk<GitDesk>({ tab: 'commit', form: 150, detail: 260 })

export function bindApi(a: PluginApi) {
  api = a
  desk = deskOf(a)
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const tn: PluginApi['tn'] = (...a) => api.tn(...a)
export const fmtDate: PluginApi['fmtDate'] = (...a) => api.fmtDate(...a)
export const fmtAgo: PluginApi['fmtAgo'] = (...a) => api.fmtAgo(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const EmptyState = (p: Parameters<PluginApi['ui']['EmptyState']>[0]): JSX.Element => api.ui.EmptyState(p)
export const FileIcon = (p: Parameters<PluginApi['ui']['FileIcon']>[0]): JSX.Element => api.ui.FileIcon(p)
export const contextMenu: PluginApi['ui']['contextMenu'] = (...a) => api.ui.contextMenu(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const pick: PluginApi['ui']['pick'] = (...a) => api.ui.pick(...a)
export const toast: PluginApi['ui']['toast'] = (...a) => api.ui.toast(...a)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
export const copyText: PluginApi['ui']['copyText'] = (...a) => api.ui.copyText(...a)
