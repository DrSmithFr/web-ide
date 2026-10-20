// The Database explorer plugin (pod/plugins/database): its tool, the console and table
// views, and the connections of the project kept up to date.
import type { PluginModule } from '../types'
import { bindApi } from './plugin'
import { refreshConnections, watchConnections } from './connections'
import { DatabaseTool } from './DatabaseTool'
import { SqlConsole } from './SqlConsole'
import { TableView } from './TableView'

export const database: PluginModule = {
  activate(api) {
    bindApi(api)
    watchConnections()
    return {
      tools: { database: { component: DatabaseTool } },
      views: { sql: SqlConsole, table: TableView },
      opened: () => void refreshConnections(),
    }
  },
}
