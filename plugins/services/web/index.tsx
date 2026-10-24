// The Services plugin (plugins/services): on the home page, the HTTP services this pod shares with
// the other pods of the account, and its links to theirs. No tool in the projects: the services
// are the pod's.
import type { PluginModule } from '@ide/types'
import { bindApi } from './plugin'
import { SharedServices } from './SharedServices'
import { LinkedServices } from './LinkedServices'

export const services: PluginModule = {
  activate() {
    return {}
  },
  home(api) {
    bindApi(api)
    return () => (
      <>
        <SharedServices />
        <LinkedServices />
      </>
    )
  },
}
