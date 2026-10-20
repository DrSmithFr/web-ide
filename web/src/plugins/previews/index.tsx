// The Previews plugin (pod/plugins/previews): the Port forwards and previews tool, the
// previews service (the assistant's card, the ▶ menu of the Console) and, on the home page, the
// open port forwards of every project.
import type { PluginModule } from '../types'
import { bindApi } from './plugin'
import { ForwardsTool } from './ForwardsTool'
import { OpenForwards } from './OpenForwards'
import { closePreview, openPreview, previews as list, previewUrl, sameSpec, setPublic, watchPreviews } from './previews'

export const previews: PluginModule = {
  activate(api) {
    bindApi(api)
    watchPreviews()
    api.provide('previews', { list, open: openPreview, close: closePreview, setPublic, url: previewUrl, same: sameSpec })
    return { tools: { forwards: { component: ForwardsTool } } }
  },
  home(api) {
    bindApi(api)
    return OpenForwards
  },
}
