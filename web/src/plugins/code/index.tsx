// The Code plugin, locked (pod/plugins/code): the view of a file buffer, which every file
// tab shows (fileView contribution), and its items of the status bar.
import type { PluginModule } from '../types'
import { bindApi } from './plugin'
import { FileEditor } from './FileEditor'
import { fileStatus } from './status'

export const code: PluginModule = {
  activate(api) {
    bindApi(api)
    return { fileView: FileEditor, fileStatus }
  },
}
