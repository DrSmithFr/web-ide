// Transport of the front end to the pod: a WebSocket on 127.0.0.1, or a MessagePort to the
// bootstrap page of the relay (which owns the WebRTC connection). Both carry the same JSON
// protocol, one whole message at a time.
import { base } from '../app/base'

export interface Channel {
  send(text: string): void
  onMessage(cb: (text: string) => void): void
  onOpen(cb: () => void): void
  onClose(cb: () => void): void
  close(): void
  /** Extra channel of the tunnel (term:<id>, xfer:<n>): null on a WebSocket. */
  openChannel?(label: string): Promise<Channel | null>
}

export function wsChannel(): Channel {
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
 */
export function askPort(label: string): Promise<Channel | null> {
  if (window.parent === window) return Promise.resolve(null)
  const chan = new MessageChannel()
  window.parent.postMessage({ kind: 'port', label, port: chan.port2 }, '*', [chan.port2])
  return Promise.resolve(portChannel(chan.port1))
}

/** The app runs in the iframe of the bootstrap page of the relay (not on 127.0.0.1). */
export function inTunnel(): boolean {
  return window.parent !== window
}
