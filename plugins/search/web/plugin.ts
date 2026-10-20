// The Search plugin's door to the core: the api given at activation, and t() by name.
import type { PluginApi } from '@ide/api'

export let api: PluginApi

export function bindApi(a: PluginApi) {
  api = a
}

export const t: PluginApi['t'] = (...a) => api.t(...a)
