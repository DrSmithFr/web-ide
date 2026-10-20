// The other pods of the account (pod/multipod): the pods this pod sees on the LAN, the
// ones the relay reports online, and the ones whose encrypted list the relay cached. The home page
// shows one Projects block per pod; Settings > Remote access says which pod of the cluster opens the
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
  /** The SVG of its icon, as the pod published it. */
  icon?: string
  /** Its last opening (ISO): the home page lists every pod's projects by it. */
  openedAt?: string
}

export interface PodInfo {
  /** The device key of the pod. */
  key: string
  name: string
  version: string
  fingerprint: string
  /** The multi-pod protocol it speaks. A pod of another one is not routed to (Home). */
  protocol?: number
  /** "outdated": it runs an older release, this pod can offer it the version it runs itself.
   *  "newer": this pod is the one to update. */
  mismatch?: string
  /** The cluster it belongs to (the pods linked by the local network). */
  cluster?: string
  /** The relay opens the other pods of the cluster through this one. */
  gateway?: boolean
  online: boolean
  /** Where the page of the pod runs through this pod (a pod of the LAN). */
  path?: string
  /** Time the pod last published its list (a pod offline, greyed). */
  seen?: number
  projects?: PodProject[] | null
  /** The pod published nothing this pod can read: it has no sync key yet. */
  nokey?: boolean
  /** The pod reaches this pod on the LAN, but this pod cannot reach it back (a NAT, WSL2). */
  oneway?: boolean
  /** It published a card a magic packet can wake: offline, the home page offers Wake. */
  wake?: boolean
  /** Its machine lets it shut down and reboot (sudo web-ide-pod allow-shutdown). Absent: a pod
   *  older than the power requests, offered nothing. */
  power?: boolean
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

/** A project of that pod can be opened from this page. A pod that does not speak the same
 *  multi-pod protocol cannot: the two would not understand each other, and the home page offers
 *  to update it instead. */
export const podOpenable = (p: PodInfo) => !p.mismatch && (inTunnel() || !!p.path)
