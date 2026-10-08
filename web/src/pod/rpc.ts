// WebSocket client of the pod: requests with responses, events pushed by the pod,
// automatic reconnection and throughput measurement (sliding window of one second).
import { createSignal } from 'solid-js'
import { lang, onLangChange, t } from '../i18n'

export type PodState = 'connecting' | 'connected' | 'disconnected'

export class RpcError extends Error {
  constructor(public code: string, message: string, public data?: any) {
    super(message)
  }
}

type Pending = { resolve: (v: any) => void; reject: (e: any) => void; method: string }
/** project: the project whose runtime sent the event (a worktree attached to the window, or its own). */
type Handler = (data: any, project?: string) => void

/**
 * Worktree a request runs in, among those attached to the window (state/project): undefined
 * for the project of the window. Set by the open project.
 */
type Scope = (method: string, params: any) => string | undefined
let scope: Scope = () => undefined
export function setScope(f: Scope) {
  scope = f
}

const envelope = (id: number, method: string, params: any, project: string | undefined) =>
  JSON.stringify(project ? { id, method, params, project } : { id, method, params })

const [state, setState] = createSignal<PodState>('connecting')
const [rates, setRates] = createSignal({ down: 0, up: 0 })
export const podState = state
export const podRates = rates

let ws: WebSocket | null = null
let nextId = 1
let clientId = ''
const pending = new Map<number, Pending>()
const handlers = new Map<string, Set<Handler>>()
const queue: string[] = []
let retry = 0
let everConnected = false

// Throughput: samples of [timestamp, bytes] kept for one second.
const down: [number, number][] = []
const up: [number, number][] = []
function sample(list: [number, number][], n: number) {
  list.push([performance.now(), n])
}
function sum(list: [number, number][]) {
  const limit = performance.now() - 1000
  while (list.length && list[0][0] < limit) list.shift()
  let s = 0
  for (const [, n] of list) s += n
  return s
}
setInterval(() => setRates({ down: sum(down), up: sum(up) }), 250)

export function myClientId() {
  return clientId
}

function url() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/ws`
}

function connect() {
  setState('connecting')
  const sock = new WebSocket(url())
  ws = sock
  sock.onopen = () => {
    retry = 0
    setState('connected')
    // First: the messages of the pod are translated for this window.
    sock.send(JSON.stringify({ id: 0, method: 'client.lang', params: { lang: lang() } }))
    for (const m of queue.splice(0)) send(m)
  }
  sock.onmessage = (ev) => {
    const text = ev.data as string
    sample(down, text.length)
    let msg: any
    try {
      msg = JSON.parse(text)
    } catch {
      return
    }
    if (msg.event) {
      if (msg.event === 'hello') {
        clientId = msg.data.clientId
        const reconnect = everConnected
        everConnected = true
        emit(reconnect ? 'pod.reconnected' : 'pod.connected', msg.data)
        return
      }
      emit(msg.event, msg.data, msg.project)
      return
    }
    const p = pending.get(msg.id)
    if (!p) return
    pending.delete(msg.id)
    if (msg.error) p.reject(new RpcError(msg.error.code, msg.error.message, msg.error.data))
    else p.resolve(msg.result)
  }
  sock.onclose = () => {
    if (ws !== sock) return
    ws = null
    setState('disconnected')
    for (const [id, p] of pending) {
      p.reject(new RpcError('disconnected', t('connection to the pod lost')))
      pending.delete(id)
    }
    emit('pod.disconnected', null)
    retry++
    setTimeout(connect, Math.min(5000, 300 * 2 ** Math.min(retry, 4)))
  }
}

function send(text: string) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    sample(up, text.length)
    ws.send(text)
  } else {
    queue.push(text)
  }
}

function emit(event: string, data: any, project?: string) {
  for (const h of handlers.get(event) ?? []) {
    try {
      h(data, project)
    } catch (e) {
      console.error(event, e)
    }
  }
}

export function request<T = any>(method: string, params?: any, signal?: AbortSignal): Promise<T> {
  return requestIn(scope(method, params), method, params, signal)
}

/** A request run in a given worktree of the window (its own project when undefined). */
export function requestIn<T = any>(project: string | undefined, method: string, params?: any, signal?: AbortSignal): Promise<T> {
  const id = nextId++
  return new Promise<T>((resolve, reject) => {
    pending.set(id, { resolve, reject, method })
    send(envelope(id, method, params, project))
    signal?.addEventListener('abort', () => {
      if (pending.delete(id)) {
        send(JSON.stringify({ method: '$/cancel', params: { id } }))
        reject(new RpcError('canceled', t('canceled')))
      }
    })
  })
}

onLangChange((l) => notify('client.lang', { lang: l }))

/** notify sends a request without waiting for its answer. */
export function notify(method: string, params?: any) {
  notifyIn(scope(method, params), method, params)
}

export function notifyIn(project: string | undefined, method: string, params?: any) {
  send(envelope(0, method, params, project))
}

export function on(event: string, h: Handler): () => void {
  let set = handlers.get(event)
  if (!set) handlers.set(event, (set = new Set()))
  set.add(h)
  return () => set!.delete(h)
}

export function startPod() {
  if (!ws) connect()
}

export function formatRate(n: number) {
  if (n < 1024) return t('{n} B/s', { n })
  if (n < 1024 * 1024) return t('{n} KB/s', { n: (n / 1024).toFixed(1) })
  return t('{n} MB/s', { n: (n / 1024 / 1024).toFixed(1) })
}
