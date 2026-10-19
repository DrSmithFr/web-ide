// The Files plugin's door to the core: the api given at activation, its state in the desk (the
// folders expanded, the options of the Explorer), and the parts of the api the components use
// by name. Nothing here imports the core itself.
import type { JSX } from 'solid-js'
import type { PluginApi } from '../api'

export let api: PluginApi

export interface FilesDesk extends Record<string, unknown> {
  /** The folders expanded in the Explorer. */
  expanded: string[]
  hidden: boolean
  excluded: boolean
  singleClick: boolean
  follow: boolean
}

export let desk: ReturnType<typeof deskOf>
const deskOf = (a: PluginApi) => a.desk<FilesDesk>({ expanded: [], hidden: true, excluded: true, singleClick: false, follow: false })

export function bindApi(a: PluginApi) {
  api = a
  desk = deskOf(a)
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const FileIcon = (p: Parameters<PluginApi['ui']['FileIcon']>[0]): JSX.Element => api.ui.FileIcon(p)
export const contextMenu: PluginApi['ui']['contextMenu'] = (...a) => api.ui.contextMenu(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const toast: PluginApi['ui']['toast'] = (...a) => api.ui.toast(...a)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
export const copyText: PluginApi['ui']['copyText'] = (...a) => api.ui.copyText(...a)
