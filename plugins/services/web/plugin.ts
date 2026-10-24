// The Services plugin's door to the core: the api the home page gives, and the parts of it the
// component uses by name. Nothing here imports the core itself.
import type { JSX } from 'solid-js'
import type { PluginApi } from '@ide/api'

export let api: PluginApi

export function bindApi(a: PluginApi) {
  api = a
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
export const Icon = (p: Parameters<PluginApi['ui']['Icon']>[0]): JSX.Element => api.ui.Icon(p)
export const errorToast: PluginApi['ui']['errorToast'] = (...a) => api.ui.errorToast(...a)
