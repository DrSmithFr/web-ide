// The plugin of the browser tests (pod/internal/plugins/hello): a tool calling its backend, and
// an item of the status bar while the tool has the focus.
import { createResource } from 'solid-js'
import type { PluginModule } from '../types'

export const hello: PluginModule = {
  activate(api) {
    function HelloTool() {
      const [text] = createResource(() => api.rpc<{ text: string }>('greet', { name: 'plugin' }).then((r) => r.text))
      return (
        <div class="panel">
          <div class="panel-head">
            <span class="panel-title">Hello</span>
          </div>
          <div class="panel-body pad" data-testid="hello-text">
            {text()}
          </div>
        </div>
      )
    }
    return {
      tools: { hello: { component: HelloTool } },
      toolStatus: { hello: () => [{ id: 'hello', testid: 'status-hello', text: 'Hello', menu: () => [{ label: 'Hi', action: () => {} }] }] },
    }
  },
}
