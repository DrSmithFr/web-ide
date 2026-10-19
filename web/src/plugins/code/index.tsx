// The Code plugin, locked (pod/internal/plugins/code): the view of a file buffer, which every file
// tab shows (fileView contribution).
import type { PluginModule } from '../types'
import { bindApi } from './plugin'
import { FileEditor } from './FileEditor'

export const code: PluginModule = {
  activate(api) {
    bindApi(api)
    return { fileView: FileEditor }
  },
}
