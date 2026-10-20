// The Kanban plugin's door to the core: the api given at activation, and the parts of it the
// board, the tickets, the milestones and the objectives use by name. Nothing here imports the
// core itself; the assistant and the Agents tool are reached as services.
import type { JSX } from 'solid-js'
import type { NavTarget, PluginApi } from '@ide/api'
import type { Capabilities } from '@ide/capabilities'

export let api: PluginApi

export function bindApi(a: PluginApi) {
  api = a
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const tn: PluginApi['tn'] = (...a) => api.tn(...a)
export const fmtDate: PluginApi['fmtDate'] = (...a) => api.fmtDate(...a)
export const fmtSize: PluginApi['fmtSize'] = (...a) => api.fmtSize(...a)
export const podPath: PluginApi['podPath'] = (...a) => api.podPath(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const EmptyState = (p: Parameters<PluginApi['ui']['EmptyState']>[0]): JSX.Element => api.ui.EmptyState(p)
export const Modal = (p: Parameters<PluginApi['ui']['Modal']>[0]): JSX.Element => api.ui.Modal(p)
export const Markdown = (p: Parameters<PluginApi['ui']['Markdown']>[0]): JSX.Element => api.ui.Markdown(p)
export const HelpLink = (p: Parameters<PluginApi['ui']['HelpLink']>[0]): JSX.Element => api.ui.HelpLink(p)
export const contextMenu: PluginApi['ui']['contextMenu'] = (...a) => api.ui.contextMenu(...a)
export const prompt: PluginApi['ui']['prompt'] = (...a) => api.ui.prompt(...a)
export const pick: PluginApi['ui']['pick'] = (...a) => api.ui.pick(...a)
export const fuzzy: PluginApi['ui']['fuzzy'] = (...a) => api.ui.fuzzy(...a)
export const toast: PluginApi['ui']['toast'] = (...a) => api.ui.toast(...a)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
export const copyText: PluginApi['ui']['copyText'] = (...a) => api.ui.copyText(...a)
export const request = <T = any>(method: string, params?: any): Promise<T> => api.rpc<T>(method, params)
export const on: PluginApi['on'] = (...a) => api.on(...a)
export const openFile = (target: NavTarget | string) => api.openFile(target)
export const relPath = (p: string) => api.project.relPath(p)
export const basename = (p: string) => api.project.basename(p)
export const root = () => api.project.root()
export const project = () => api.project.info()
export const showWorktree = (id: string) => api.project.showWorktree(id)
export const capability = <K extends keyof Capabilities>(name: K) => api.use(name)
