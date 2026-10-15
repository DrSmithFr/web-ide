// Requests to another pod of the account from the home page: edit, remove, change the icon of a
// project it holds, or create one there. The pod that holds the project decides, with its own
// checks: the page only carries the request, on a side channel of that pod — a DataChannel of
// its tunnel (or routed through the gateway) on the relay origin, the WebSocket its gateway
// serves under /_lan/pod/<key>/ on a pod.
import { lang, t } from '../i18n'
import { RpcError } from './rpc'
import { askPort, type Channel, inTunnel, wsChannel } from './transport'
import type { PodInfo } from '../state/pods'

/** A request of the JSON protocol, to this pod or to another. */
export type Call = <T = any>(method: string, params?: any) => Promise<T>

interface Link {
  ch: Promise<Channel | null>
  pending: Map<number, { resolve: (v: any) => void; reject: (e: Error) => void }>
  idle?: ReturnType<typeof setTimeout>
}

const links = new Map<string, Link>()
let nextId = 1
// A link is kept a moment after its last answer: browsing the folders of a pod asks again and
// again, and a routed channel runs a handshake of its own.
const IDLE = 30_000

function open(pod: PodInfo): Link {
  const raw: Promise<Channel | null> = inTunnel()
    ? askPort('rpc:home', '/pod/' + pod.key, true)
    : Promise.resolve(pod.path ? wsChannel(pod.path) : null)
  const pending: Link['pending'] = new Map()
  const link: Link = { ch: Promise.resolve(null), pending }
  const drop = (why: Error) => {
    if (links.get(pod.key) === link) links.delete(pod.key)
    for (const p of pending.values()) p.reject(why)
    pending.clear()
  }
  // Ready once open: a WebSocket drops what is sent before.
  link.ch = raw.then((c) => {
    if (!c) {
      drop(new RpcError('unreachable', t('{name} cannot be reached from here', { name: pod.name })))
      return null
    }
    c.onMessage((text) => {
      let m: any
      try {
        m = JSON.parse(text)
      } catch {
        return
      }
      const p = m?.id ? pending.get(m.id) : undefined
      if (!p) return
      pending.delete(m.id)
      if (m.error) p.reject(new RpcError(m.error.code, m.error.message, m.error.data))
      else p.resolve(m.result)
      if (!pending.size) {
        clearTimeout(link.idle)
        link.idle = setTimeout(() => {
          if (pending.size) return
          if (links.get(pod.key) === link) links.delete(pod.key)
          c.close()
        }, IDLE)
      }
    })
    return new Promise<Channel | null>((resolve) => {
      c.onClose(() => {
        resolve(null)
        drop(new RpcError('disconnected', t('connection to the pod lost')))
      })
      c.onOpen(() => {
        // The pod answers in the language of the page.
        c.send(JSON.stringify({ id: 0, method: 'client.lang', params: { lang: lang() } }))
        resolve(c)
      })
    })
  })
  return link
}

/** The requests to a pod of the account: one link per pod, closed when idle. */
export function podCall(pod: PodInfo): Call {
  return <T,>(method: string, params?: any) => {
    let link = links.get(pod.key)
    if (!link) links.set(pod.key, (link = open(pod)))
    const l = link
    const id = nextId++
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        l.pending.delete(id)
        reject(new RpcError('timeout', t('{name} did not answer', { name: pod.name })))
      }, 20_000)
      l.pending.set(id, {
        resolve: (v) => (clearTimeout(timer), resolve(v)),
        reject: (e) => (clearTimeout(timer), reject(e)),
      })
      clearTimeout(l.idle)
      void l.ch.then((c) => c?.send(JSON.stringify({ id, method, params: params ?? {} })))
    })
  }
}
