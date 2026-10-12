// The other pods of the account on the local network (pod/internal/lan): their projects
// for the home page, the addresses typed by hand for Settings. The local pod is the gateway:
// the page of each peer runs through us, under /_lan/pod/<key>/ of our origin.
import { createSignal, onCleanup } from 'solid-js'
import { podPath } from '../app/base'
import { on, request } from '../pod/rpc'

export interface LanProject {
  id: string
  name: string
  description?: string
  type: 'local' | 'ssh'
  path: string
}

export interface LanPeer {
  id: string
  name: string
  fingerprint: string
  version: string
  addr: string
  online: boolean
  /** Where the page of the peer runs through this pod (empty: offline). */
  path: string
  projects?: LanProject[] | null
}

export interface LanStatus {
  listen: string
  error?: string
  paired: boolean
  peers: LanPeer[]
  manual: string[]
  /** Addresses that did not lead to a pod of the account, with the reason. */
  failed: { addr: string; manual: boolean; error: string }[]
}

/** The LAN state, kept up to date while the calling component lives (null: the link is off). */
export function useLan() {
  const [lan, setLan] = createSignal<LanStatus | null>(null)
  request<LanStatus>('lan.status').then(setLan, () => setLan(null))
  onCleanup(on('lan.changed', (d: LanStatus) => setLan(d)))
  return [lan, setLan] as const
}

/** The address of a page of a peer, through this pod. */
export const peerURL = (p: LanPeer, path = '/') => p.path + path

/** The pod that serves this page as a gateway, when the page belongs to a pod of the LAN. */
export interface Gateway {
  name: string
}

const [gateway, setGateway] = createSignal<Gateway | null>(null)
// The pod answers {} itself; under the path of a peer, the gateway answers with its name.
// Through the relay the path is not served (the page gets index.html), which reads as no gateway.
fetch(podPath('/_lan/gateway'))
  .then((r) => (r.ok ? r.json() : null))
  .then((d) => d?.name && setGateway(d))
  .catch(() => {})
export { gateway }

/** The home page of the gateway pod. */
export const gatewayHome = '/'
