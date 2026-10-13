// The other pods of the account (pod/internal/multipod): the pods this pod sees on the LAN, the
// ones the relay reports online, and the ones whose encrypted list the relay cached. The home page
// shows one Projects block per pod; Settings > Remote access says which pod of the site opens the
// others.
import { createSignal, onCleanup } from 'solid-js'
import { on, request } from '../pod/rpc'
import { inTunnel } from '../pod/transport'

export interface PodProject {
  id: string
  name: string
  description?: string
  type: 'local' | 'ssh'
  path: string
}

export interface PodInfo {
  /** The device key of the pod. */
  key: string
  name: string
  version: string
  fingerprint: string
  /** The site it belongs to (the pods linked by the local network). */
  site?: string
  /** The relay opens the other pods of the site through this one. */
  gateway?: boolean
  online: boolean
  /** Where the page of the pod runs through this pod (a pod of the LAN). */
  path?: string
  /** Time the pod last published its list (a pod offline, greyed). */
  seen?: number
  projects?: PodProject[] | null
  /** The pod published nothing this pod can read: it has no sync key yet. */
  nokey?: boolean
}

/** The pods of the account, kept up to date while the calling component lives (null: not paired). */
export function usePods() {
  const [pods, setPods] = createSignal<PodInfo[] | null>(null)
  request<PodInfo[]>('pods.list').then(setPods, () => setPods(null))
  onCleanup(on('pods.changed', (d: PodInfo[]) => setPods(d)))
  return [pods, setPods] as const
}

/** The page of a pod: under /_lan/pod/<key>/ on the origin of the pod that serves it, under
 * /pod/<key>/ on the relay origin, where one origin carries every pod of the account. */
export const podURL = (p: PodInfo, path = '/') => (inTunnel() ? '/pod/' + p.key : (p.path ?? '')) + path

/** A project of that pod can be opened from this page. */
export const podOpenable = (p: PodInfo) => inTunnel() || !!p.path
