// Plugins of the window (docs/design/plugins.md): the states the pod sends (project.open,
// plugins.changed), the page modules of the built-in plugins, activated once when first enabled,
// and the contributions of the enabled ones (tools, tab views, commands).
import { createMemo, createRoot, createSignal } from 'solid-js'
import { on, request } from '../pod/rpc'
import { setPluginActions } from '../keys/bindings'
import { makeApi } from './api'
import type { PluginContributions, PluginModule, PluginState } from './types'
import { hello } from './hello'

const builtins: Record<string, PluginModule> = { hello }

const [states, setStates] = createSignal<PluginState[]>([])
export const pluginStates = states
const active: Record<string, PluginContributions> = {}

/** Takes the states of the plugins of the project: the enabled ones are activated. */
export function setPluginStates(list: PluginState[] | undefined) {
  list ??= []
  for (const s of list) {
    const mod = builtins[s.manifest.id]
    if (s.enabled && mod && !active[s.manifest.id]) active[s.manifest.id] = mod.activate(makeApi(s.manifest.id))
  }
  setStates(list)
  setPluginActions(
    enabled().flatMap((s) => [
      ...(s.manifest.contributes.tools ?? []).map((x) => ({ id: `tool.${x.id}`, label: x.label, category: 'Tools', inTerminal: true })),
      ...(s.manifest.contributes.commands ?? []).map((c) => ({ id: `${s.manifest.id}.${c.id}`, label: c.label, category: c.category ?? s.manifest.name, run: active[s.manifest.id]?.commands?.[c.id] })),
    ]),
  )
}

on('plugins.changed', (e: { plugins: PluginState[] }) => setPluginStates(e?.plugins))
on('pod.reconnected', () => void request<{ plugins: PluginState[] }>('plugins.list').then((r) => setPluginStates(r.plugins), () => {}))

const enabled = () => states().filter((s) => s.enabled && active[s.manifest.id])

export const pluginEnabled = (id: string) => states().some((s) => s.manifest.id === id && s.enabled)

/** The tools of the enabled plugins that have a page module. */
export const pluginTools = createRoot(() =>
  createMemo(() =>
    enabled().flatMap((s) =>
      (s.manifest.contributes.tools ?? []).flatMap((x) => {
        const c = active[s.manifest.id].tools?.[x.id]
        return c ? [{ ...x, component: c.component, badge: c.badge }] : []
      }),
    ),
  ),
)

/** The tab view of a kind plugin:<plugin>/<view>, when its plugin is enabled. */
export function pluginView(kind: string) {
  const m = /^plugin:([^/]+)\/(.+)$/.exec(kind)
  if (!m || !pluginEnabled(m[1])) return undefined
  return active[m[1]]?.views?.[m[2]]
}

/** The label of a tab view, as its manifest declares it. */
export function pluginViewLabel(kind: string) {
  const m = /^plugin:([^/]+)\/(.+)$/.exec(kind)
  return m ? states().find((s) => s.manifest.id === m[1])?.manifest.contributes.tabViews?.find((v) => v.id === m[2])?.label : undefined
}

export const setPluginEnabled = (id: string, enabled: boolean) => request<{ plugins: PluginState[] }>('plugins.set', { id, enabled }).then((r) => setPluginStates(r.plugins))
