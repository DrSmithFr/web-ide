// The page module of an external plugin, built by the core from its bundle (plugins.bundle):
// each tool and tab view a sandboxed frame running the plugin's module, its commands run in a
// hidden frame of their own. The registry loads it as it loads a built-in.
import { createEffect, on as onSignal, onCleanup, onMount } from 'solid-js'
import { request } from '../pod/rpc'
import { lang } from '../i18n'
import type { TabState } from '../state/project'
import { frameDoc, serveFrame } from './sandbox'
import type { PluginContributions, PluginModule, PluginState } from './types'

type Bundle = { version: string; js: string; catalogs: Record<string, Record<string, string>> }

/** A frame of the plugin showing one of its views (a tool, a tab view). */
function SandboxFrame(props: { state: PluginState; grants: () => string[]; bundle: Bundle; view: string; tab?: TabState }) {
  let frame!: HTMLIFrameElement
  let served: ReturnType<typeof serveFrame> | undefined
  // Served once: the frame keeps its port for its life (a new state is its own business).
  onMount(() => {
    served = serveFrame(frame, {
      plugin: props.state.manifest.id,
      name: props.state.manifest.name,
      grants: props.grants,
      catalogs: props.bundle.catalogs,
      view: props.view,
      tab: props.tab,
    })
  })
  createEffect(onSignal(lang, () => served?.lang(), { defer: true }))
  onCleanup(() => served?.stop())
  return (
    <div class="panel plugin-external-view">
      <iframe
        ref={frame}
        class="plugin-frame"
      data-plugin={props.state.manifest.id}
      data-view={props.view}
      title={props.state.manifest.name}
      sandbox="allow-scripts"
        srcdoc={frameDoc(props.bundle.js)}
      />
    </div>
  )
}

/** The hidden frame running the commands of a plugin, made on its first command. */
function commandRunner(state: PluginState, grants: () => string[], bundle: Bundle) {
  let ready: Promise<(m: unknown) => void> | undefined
  return (command: string) => {
    ready ??= new Promise((resolve) => {
      const frame = document.createElement('iframe')
      frame.setAttribute('sandbox', 'allow-scripts')
      frame.className = 'plugin-frame-hidden'
      frame.dataset.plugin = state.manifest.id
      frame.srcdoc = frameDoc(bundle.js)
      const served = serveFrame(frame, {
        plugin: state.manifest.id,
        name: state.manifest.name,
        grants,
        catalogs: bundle.catalogs,
        view: '',
        onReady: () => resolve(served.send),
      })
      document.body.append(frame)
    })
    void ready.then((send) => send({ command }))
  }
}

/** Loads the bundle of an external plugin and builds its module; current is its state now. */
export async function externalModule(state: PluginState, current: () => PluginState | undefined): Promise<PluginModule> {
  const bundle = await request<Bundle>('plugins.bundle', { id: state.manifest.id })
  const c = state.manifest.contributes
  const grants = () => current()?.external?.grants ?? []
  const run = commandRunner(state, grants, bundle)
  return {
    activate: (): PluginContributions => ({
      tools: Object.fromEntries((c.tools ?? []).map((x) => [x.id, { component: () => <SandboxFrame state={state} grants={grants} bundle={bundle} view={x.id} /> }])),
      views: Object.fromEntries((c.tabViews ?? []).map((v) => [v.id, (props: { tab: TabState }) => <SandboxFrame state={state} grants={grants} bundle={bundle} view={v.id} tab={props.tab} />])),
      commands: Object.fromEntries((c.commands ?? []).map((x) => [x.id, () => run(x.id)])),
    }),
  }
}
