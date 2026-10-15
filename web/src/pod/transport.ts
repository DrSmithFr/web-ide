// Transport of the front end to the pod: a WebSocket on 127.0.0.1, or a MessagePort to the
// bootstrap page of the relay (which owns the WebRTC connection). Both carry the same JSON
// protocol, one whole message at a time.
import { base as pageBase } from '../app/base'

export interface Channel {
  send(text: string): void
  onMessage(cb: (text: string) => void): void
  onOpen(cb: () => void): void
  onClose(cb: () => void): void
  close(): void
  /** Extra channel of the tunnel (term:<id>, xfer:<n>): null on a WebSocket. */
  openChannel?(label: string): Promise<Channel | null>
}

/** The WebSocket of the pod the page runs on, or of the pod served under base (another pod of
 * the account, through this one). */
export function wsChannel(base = pageBase): Channel {
  const handlers = { open: () => {}, close: () => {}, message: (_: string) => {} }
  let sock: WebSocket | null = null
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  sock = new WebSocket(`${proto}//${location.host}${base}/ws`)
  sock.onopen = () => handlers.open()
  sock.onmessage = (ev) => handlers.message(ev.data as string)
  sock.onclose = () => handlers.close()
  return {
    send: (text) => {
      if (sock && sock.readyState === WebSocket.OPEN) sock.send(text)
    },
    onMessage: (cb) => (handlers.message = cb),
    onOpen: (cb) => (handlers.open = cb),
    onClose: (cb) => (handlers.close = cb),
    close: () => sock?.close(),
  }
}

/** A channel over a MessagePort: the bootstrap page of the relay wires it to a DataChannel. */
export function portChannel(port: MessagePort): Channel {
  const handlers = { open: () => {}, close: () => {}, message: (_: string) => {} }
  // A port is open from the start, unlike a WebSocket: the open callback must still reach
  // the handler registered after the channel was created (rpc.ts calls onOpen later).
  let opened = false
  port.onmessage = (e) => handlers.message(e.data as string)
  // The close event is not in the DOM types of MessagePort, but the browser dispatches it
  // when the other end closes.
  const p = port as any
  p.addEventListener('close', () => handlers.close())
  opened = true
  return {
    send: (text) => port.postMessage(text),
    onMessage: (cb) => (handlers.message = cb),
    onOpen: (cb) => {
      handlers.open = cb
      if (opened) cb()
    },
    onClose: (cb) => (handlers.close = cb),
    close: () => port.close(),
    openChannel: (label) => askPort(label),
  }
}

/**
 * One channel of the tunnel, asked to the bootstrap page that owns the PeerConnection: we
 * hand it one end of a MessageChannel, it wires the other end to a DataChannel of the
 * given label. Null outside the bootstrap page (127.0.0.1), where there is no parent.
 *
 * base names the pod the channel is for: on the relay origin, one window reaches every pod of
 * the account and the bootstrap page picks the tunnel of that pod (its own, or the gateway's
 * with the labels prefixed by @<key>/). side asks a channel beside the rpc channel of the page:
 * the bootstrap never wires it as the app's own connection, and closing it never drops the
 * tunnel. A pod serves one rpc channel per window, so a side channel of a pod reached on its own
 * tunnel carries a label of its own ('rpc:push') and rides the window of the gateway on a routed one.
 */
export function askPort(label: string, base = pageBase, side = false): Promise<Channel | null> {
  if (window.parent === window) return Promise.resolve(null)
  const chan = new MessageChannel()
  window.parent.postMessage({ kind: 'port', label, base, side, port: chan.port2 }, '*', [chan.port2])
  return Promise.resolve(portChannel(chan.port1))
}

/** The app runs in the iframe of the bootstrap page of the relay (not on 127.0.0.1). */
export function inTunnel(): boolean {
  return window.parent !== window
}

/** The moments of its start the app tells the journal of the bootstrap page (#75): the page
 * writes them with its own clock and its own words, so a load is read from end to end. */
export type BootStep = 'start' | 'rpc' | 'home'
const told = new Set<BootStep>()
export function traceBoot(step: BootStep) {
  if (!inTunnel() || told.has(step)) return
  told.add(step)
  window.parent.postMessage({ kind: 'trace', step }, '*')
}

/** The journal of the connection, as the bootstrap page kept it: '' outside the relay origin,
 * or from a bootstrap page older than #75 (it does not answer). */
export function bootJournal(): Promise<string> {
  if (!inTunnel()) return Promise.resolve('')
  const chan = new MessageChannel()
  return new Promise((resolve) => {
    const done = setTimeout(() => resolve(''), 1500)
    chan.port1.onmessage = (e) => {
      clearTimeout(done)
      resolve(String(e.data ?? ''))
      chan.port1.close()
    }
    window.parent.postMessage({ kind: 'journal', port: chan.port2 }, '*', [chan.port2])
  })
}

/** The base of the entry pod of the bootstrap page ('/pod/<key>', '' when it has none): its home
 * page is the one of the window. Null outside the relay origin, or from a bootstrap page that
 * does not answer. */
export function entryBase(): Promise<string | null> {
  if (!inTunnel()) return Promise.resolve(null)
  const chan = new MessageChannel()
  return new Promise((resolve) => {
    const done = setTimeout(() => resolve(null), 1500)
    chan.port1.onmessage = (e) => {
      clearTimeout(done)
      resolve(typeof e.data === 'string' ? e.data : null)
      chan.port1.close()
    }
    window.parent.postMessage({ kind: 'entry', port: chan.port2 }, '*', [chan.port2])
  })
}
