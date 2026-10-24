// The Services plugin (plugins/services): on the home page, the HTTP services this pod shares with
// the other pods of the account. No tool in the projects: the services are the pod's.
import type { PluginModule } from '@ide/types'
import { bindApi } from './plugin'
import { SharedServices } from './SharedServices'

export const services: PluginModule = {
  activate() {
    return {}
  },
  home(api) {
    bindApi(api)
    return SharedServices
  },
}
