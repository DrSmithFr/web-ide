// The Search plugin (pod/internal/plugins/search): the Search tool and the Text tab of Search
// Everywhere.
import type { PluginModule } from '../types'
import { bindApi } from './plugin'
import { focusGlobalSearch, GlobalSearch } from './GlobalSearch'
import { textProvider } from './text'

export const search: PluginModule = {
  activate(api) {
    bindApi(api)
    return {
      tools: { search: { component: GlobalSearch } },
      searchProviders: [textProvider],
      commands: {
        // Shows the Search tool with its query selected.
        'search.global': () => {
          api.showTool('search')
          requestAnimationFrame(focusGlobalSearch)
        },
      },
    }
  },
}
