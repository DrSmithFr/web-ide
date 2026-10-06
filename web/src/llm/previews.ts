// Previews of apps (share_preview): the pod starts the command, serves the app on a
// temporary URL (Tailscale) and stops it with the command. The list is the pod's, shared by
// every window.
import { createRoot, createSignal } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'

export interface PreviewSpec {
  project: string
  title: string
  command: string
  cwd?: string
  port: number
}

export interface Preview extends PreviewSpec {
  id: string
  console: string
  url: string
  publicUrl?: string
  public: boolean
  tailscale: boolean
  started: number
}

const store = createRoot(() => {
  const [list, setList] = createSignal<Preview[]>([])
  const refresh = () => request<Preview[]>('preview.list').then(setList, () => {})
  onPod('preview.changed', (l: Preview[]) => setList(l ?? []))
  onPod('pod.reconnected', refresh)
  return { list, refresh, loaded: false }
})

/** The running previews (loaded on first use). */
export function previews(): Preview[] {
  if (!store.loaded) {
    store.loaded = true
    void store.refresh()
  }
  return store.list()
}

export const sameSpec = (p: PreviewSpec, s: PreviewSpec) => p.project === s.project && p.command === s.command && (p.cwd ?? '') === (s.cwd ?? '') && p.port === s.port

/**
 * Starts (or finds) the preview of a spec and opens it in a new tab. The tab opens during
 * the click, before the pod answers, or pop-up blockers would refuse it.
 */
export async function openPreview(spec: PreviewSpec): Promise<Preview> {
  const tab = window.open('about:blank', '_blank')
  if (tab) tab.opener = null
  try {
    const p = await request<Preview>('preview.open', spec)
    if (tab) tab.location.href = p.url
    else window.open(p.url, '_blank')
    return p
  } catch (e) {
    tab?.close()
    throw e
  }
}

export const closePreview = (id: string) => request('preview.close', { id })
export const setPublic = (id: string, pub: boolean) => request<Preview>('preview.public', { id, public: pub })
