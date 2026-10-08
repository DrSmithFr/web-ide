// Client of the pod: requests with responses, events pushed by the pod, automatic
// reconnection and throughput measurement (sliding window of one second). It runs over a
// Transport (see pod/transport.ts): a WebSocket on 127.0.0.1, or the DataChannel of the
// relay through the bootstrap page.
import { createSignal } from 'solid-js'
import { lang, onLangChange, t } from '../i18n'
import { askPort, type Channel, wsChannel } from './transport'

export type { Channel }

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

let chan: Channel | null = null
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

/** One message read on any channel of the window (the answer of a request comes back on
 * the channel it was sent on). */
function handleMessage(text: string) {
  if (!text) return
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

function connect() {
  setState('connecting')
  const c = chan ?? wsChannel()
  chan = c
  c.onOpen(() => {
    retry = 0
    setState('connected')
    // First: the messages of the pod are translated for this window.
    c.send(JSON.stringify({ id: 0, method: 'client.lang', params: { lang: lang() } }))
    for (const m of queue.splice(0)) send(m)
  })
  c.onMessage(handleMessage)
  c.onClose(() => {
    if (chan !== c) return
    chan = null
    setState('disconnected')
    for (const [id, p] of pending) {
      p.reject(new RpcError('disconnected', t('connection to the pod lost')))
      pending.delete(id)
    }
    emit('pod.disconnected', null)
    retry++
    setTimeout(connect, Math.min(5000, 300 * 2 ** Math.min(retry, 4)))
  })
}

function send(text: string, on?: Channel) {
  const c = on ?? chan
  if (c) {
    sample(up, text.length)
    c.send(text)
  } else {
    queue.push(text)
  }
}

function emit(event: string, data: any, project?: string) {
  for (const h of handlers.get(event) ?? []) {
    try {
      h(data, project)
    } catch (e) {
      console.error(e)
    }
  }
}

// A request bigger than this goes on its own channel (xfer:<id>), so a 2 MB file read or
// save never delays a keystroke on the rpc channel.
export const bigMessage = 128 * 1024

export function request<T = any>(method: string, params?: any, signal?: AbortSignal): Promise<T> {
  return requestIn(scope(method, params), method, params, signal)
}

/** A request run in a given worktree of the window (its own project when undefined). */
export function requestIn<T = any>(project: string | undefined, method: string, params?: any, signal?: AbortSignal): Promise<T> {
  const id = nextId++
  return new Promise<T>((resolve, reject) => {
    pending.set(id, { resolve, reject, method })
    const text = envelope(id, method, params, project)
    if (text.length > bigMessage) void sendBig(id, text)
    else send(text)
    signal?.addEventListener('abort', () => {
      if (pending.delete(id)) {
        send(JSON.stringify({ method: '$/cancel', params: { id } }))
        reject(new RpcError('canceled', t('canceled')))
      }
    })
  })
}

/** Sends one big request on its own channel; null on a WebSocket, where the rpc channel
 * carries everything. */
async function sendBig(id: number, text: string) {
  const ch = await openChannel('xfer:' + id)
  send(text, ch ?? undefined)
}

onLangChange((l) => notify('client.lang', { lang: l }))

/** notify sends a request without waiting for its answer. */
export function notify(method: string, params?: any) {
  notifyIn(scope(method, params), method, params)
}

export function notifyIn(project: string | undefined, method: string, params?: any) {
  send(envelope(0, method, params, project))
}

/** notify on one channel of the tunnel (the terminal input goes on its term:<id> channel). */
export function notifyOn(ch: Channel | null, method: string, params?: any, project?: string) {
  send(envelope(0, method, params, project), ch ?? undefined)
}

export function on(event: string, h: Handler): () => void {
  let set = handlers.get(event)
  if (!set) handlers.set(event, (set = new Set()))
  set.add(h)
  return () => set!.delete(h)
}

/**
 * Starts the connection of the window: the DataChannel of the tunnel when the app runs in
 * the iframe of the bootstrap page (the relay origin), else the WebSocket of the pod. Each
 * project window gets its own channel.
 */
export function startPod() {
  if (chan) return
  void (async () => {
    chan = (await askPort('rpc')) ?? wsChannel()
    connect()
  })()
}

/**
 * Opens one channel of the tunnel (term:<id>, xfer:<n>): one per terminal and per big
 * transfer. Null on a WebSocket, where the rpc channel carries everything.
 */
export async function openChannel(label: string): Promise<Channel | null> {
  const ch = await chan?.openChannel?.(label)
  if (!ch) return null
  ch.onMessage(handleMessage)
  return ch
}

export function formatRate(n: number) {
  if (n < 1024) return t('{n} B/s', { n })
  if (n < 1024 * 1024) return t('{n} KB/s', { n: (n / 1024).toFixed(1) })
  return t('{n} MB/s', { n: (n / 1024 / 1024).toFixed(1) })
}
