// The Docker plugin (pod/plugins/docker): its tool, and the Compose profiles chosen in
// it given to the assistant's Docker tools.
import type { PluginModule } from '../types'
import { bindApi, desk } from './plugin'
import { DockerTool } from './DockerTool'

export const docker: PluginModule = {
  activate(api) {
    bindApi(api)
    return {
      tools: { docker: { component: DockerTool } },
      assistantOptions: () => ({ profiles: desk.get().profiles }),
    }
  },
}
