// The Exec plugin (pod/internal/plugins/exec): the Console tool, the saved commands, and the
// terminal and commands capabilities other parts of the IDE use.
import type { PluginModule } from '../types'
import { api, bindApi } from './plugin'
import { ConsoleTool, navigateConsoles, newConsole, refreshConsoles, setConsoleList, TermView, consoles, watchConsoles } from './consoles'
import { commands, loadCommands, show, watchCommands } from './commands'
import { CommandsHost, openCommands } from './CommandsModal'

export const exec: PluginModule = {
  activate(a) {
    bindApi(a)
    watchConsoles()
    watchCommands()
    a.provide('terminal', { consoles, open: newConsole, show, TermView, setList: setConsoleList, navigate: navigateConsoles })
    a.provide('commands', { list: commands, load: loadCommands, edit: openCommands })
    return {
      tools: { console: { component: ConsoleTool } },
      hosts: [CommandsHost],
      commands: {
        // A new terminal: a tile of its own in the Grid and Scroll modes.
        'console.new': () => {
          if (api.tiles.active() && !api.tiles.phone()) void newConsole({ show: false }).then((c) => c && api.tiles.open({ kind: 'console', console: c.id }))
          else void newConsole()
        },
      },
      opened: () => {
        void loadCommands()
        void refreshConsoles()
      },
    }
  },
}
