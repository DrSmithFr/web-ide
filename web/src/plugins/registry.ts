// Plugins of the window (docs/design/plugins.md): the states the pod sends (project.open,
// plugins.changed), the page modules of the built-in plugins, activated once when first enabled,
// and the contributions of the enabled ones (tools, tab views, commands).
import { createMemo, createRoot, createSignal } from 'solid-js'
import { on, request } from '../pod/rpc'
import { setPluginActions } from '../keys/bindings'
import { API_VERSION, makeApi } from './api'
import type { PluginContributions, PluginModule, PluginState } from './types'
import { setCapabilityGate } from './capabilities'
import { setPluginProviders } from '../popups/searchProviders'
import { setDiffSources } from '../ui/diffSources'
import { hello } from './hello'
import { exec } from './exec'
import { search } from './search'
import { git } from './git'
import { previews } from './previews'
import { languages } from './languages'
import { docker } from './docker'
import { database } from './database'

const builtins: Record<string, PluginModule> = { database, docker, exec, git, hello, languages, previews, search }

const [states, setStates] = createSignal<PluginState[]>([])
export const pluginStates = states
const active: Record<string, PluginContributions> = {}

// The plugins enabled in the window at the last states.
let running = new Set<string>()

/**
 * Takes the states of the plugins of the project: the enabled ones are activated, and those
 * just enabled (all of them when the project was just opened) refresh.
 */
export function setPluginStates(list: PluginState[] | undefined, opened = false) {
  list ??= []
  for (const s of list) {
    const mod = builtins[s.manifest.id]
    if (s.enabled && mod && !active[s.manifest.id] && s.manifest.apiVersion === API_VERSION) active[s.manifest.id] = mod.activate(makeApi(s.manifest.id))
  }
  setStates(list)
  setPluginActions(
    enabled().flatMap((s) => [
      ...(s.manifest.contributes.tools ?? []).map((x) => ({ id: `tool.${x.id}`, label: x.label, category: 'Tools', inTerminal: true })),
      ...(s.manifest.contributes.commands ?? []).map((c) => ({ id: c.id.includes('.') ? c.id : `${s.manifest.id}.${c.id}`, label: c.label, category: c.category ?? s.manifest.name, keys: c.keys, replaces: c.replaces, inTerminal: c.inTerminal, run: active[s.manifest.id]?.commands?.[c.id] })),
    ]),
  )
  setPluginProviders(enabled().flatMap((s) => active[s.manifest.id].searchProviders ?? []))
  setDiffSources(enabled().flatMap((s) => active[s.manifest.id].diffSources ?? []))
  const now = new Set(enabled().map((s) => s.manifest.id))
  for (const id of now) if (opened || !running.has(id)) active[id].opened?.()
  running = now
}

on('plugins.changed', (e: { plugins: PluginState[] }) => setPluginStates(e?.plugins))
/** The window opened a project (project.open gave the states): each enabled plugin refreshes. */
export const pluginsOpened = (list: PluginState[] | undefined) => setPluginStates(list, true)

/** After a reconnection, once the project is open again. */
export async function refreshPlugins() {
  try {
    pluginsOpened((await request<{ plugins: PluginState[] }>('plugins.list')).plugins)
  } catch {
    /* no project */
  }
}

const enabled = () => states().filter((s) => s.enabled && active[s.manifest.id])

export const pluginEnabled = (id: string) => states().some((s) => s.manifest.id === id && s.enabled)
setCapabilityGate(pluginEnabled)

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

/** The parts of the home page the built-ins give (no project: whether it is enabled is moot). */
export function homeParts() {
  return Object.entries(builtins).flatMap(([id, m]) => (m.home ? [m.home(makeApi(id))] : []))
}

/** The hosts of the enabled plugins (modals), mounted by the project window. */
export const pluginHosts = createRoot(() => createMemo(() => enabled().flatMap((s) => active[s.manifest.id].hosts ?? [])))

/** The tab view of a kind plugin:<plugin>/<view>, when its plugin is enabled. */
export function pluginView(kind: string) {
  const m = /^plugin:([^/]+)\/(.+)$/.exec(kind)
  if (!m || !pluginEnabled(m[1])) return undefined
  return active[m[1]]?.views?.[m[2]]
}

/** The options of the enabled plugins for a conversation of the assistant, by plugin id. */
export function pluginAssistantOptions(): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const s of enabled()) {
    const f = active[s.manifest.id].assistantOptions
    if (f) out[s.manifest.id] = f()
  }
  return out
}

/** The label of a tab view, as its manifest declares it. */
export function pluginViewLabel(kind: string) {
  const m = /^plugin:([^/]+)\/(.+)$/.exec(kind)
  return m ? states().find((s) => s.manifest.id === m[1])?.manifest.contributes.tabViews?.find((v) => v.id === m[2])?.label : undefined
}

export const setPluginEnabled = (id: string, enabled: boolean) => request<{ plugins: PluginState[] }>('plugins.set', { id, enabled }).then((r) => setPluginStates(r.plugins))
