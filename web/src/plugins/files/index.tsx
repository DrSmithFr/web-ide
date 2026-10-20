// The Files plugin, locked (pod/plugins/files): the Explorer, the Files tab of Search
// Everywhere, and the files service (reveal a file, create a file or a folder).
import type { PluginModule } from '../types'
import { api as theApi, bindApi } from './plugin'
import { createIn, Explorer, revealInExplorer, watchFolders } from './Explorer'
import { filesProvider } from './search'

/** Where New file creates: the folder of the active file, else the root. */
function newEntryDir() {
  const path = theApi.activeFile() ?? ''
  return path && theApi.project.relPath(path) !== path ? path.slice(0, path.lastIndexOf('/')) : theApi.project.root()
}

export const files: PluginModule = {
  activate(api) {
    bindApi(api)
    watchFolders()
    api.provide('files', { reveal: revealInExplorer, createIn })
    api.keys.register('file.newFile', () => void createIn(newEntryDir(), false))
    api.keys.register('file.newFolder', () => void createIn(newEntryDir(), true))
    return { tools: { explorer: { component: Explorer } }, searchProviders: [filesProvider] }
  },
}
