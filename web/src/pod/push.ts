// Push notifications, on the relay origin. The app runs in the iframe of the bootstrap page,
// controlled by the Service Worker of the relay: the browser subscribes there with the VAPID
// key of the relay, registers its endpoint with the relay (signed by its device key) and gives
// the pod the id the relay answered and the keys the pod encrypts for. The relay never sees
// a title or a project name: it forwards what the pod encrypted.
import { on, request } from './rpc'

/** The browser device of this window: the bootstrap page of the relay read it for the
 * handshake ('' on the pod origin, where there is no push). */
export function relayDevice(): string {
  try {
    return window.parent !== window ? ((window.parent as any).identity?.device ?? '') : ''
  } catch {
    return ''
  }
}

/** Tells the pod whether this window is shown: a device with a visible window gets no push
 * notification (the page shows its own toasts). */
function report() {
  const device = relayDevice()
  if (device) request('window.visible', { device, visible: !document.hidden }).catch(() => {})
}

export function installVisibility() {
  document.addEventListener('visibilitychange', report)
  on('pod.reconnected', report)
  // The first report waits in the queue of the connection: through the tunnel, the hello of the
  // pod reaches the bootstrap page before the app listens, so pod.connected is not seen there.
  report()
}

/** What is missing for push here ('' when nothing): iOS shows notifications only from the app
 * installed on the home screen. */
export function pushMissing(): string {
  if (!('Notification' in window) || !('PushManager' in window) || !('serviceWorker' in navigator))
    return 'This browser shows notifications only from the installed app: add the page to the home screen, then open it from there.'
  return ''
}

const b64u = (b: ArrayBuffer | Uint8Array) =>
  btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
const b64d = (s: string) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0))

interface Identity {
  openStore(): Promise<IDBDatabase>
  unlock(db: IDBDatabase): Promise<{ rec: any; key: CryptoKey } | null>
  sign(key: CryptoKey, msg: string): Promise<string>
}

// The identity module of the relay, imported at runtime (the same file as the bootstrap page's).
const IDENTITY_PATH = '/_relay/identity.js'

/** A request to the relay signed by the device key, with its certificate. */
async function signed(path: string, action: string, target: string, body: Record<string, string>) {
  const id = (await import(/* @vite-ignore */ IDENTITY_PATH)) as unknown as Identity
  const d = await id.unlock(await id.openStore())
  if (!d?.rec?.cert) throw new Error('This device is not paired on this origin, or its passkey was not unlocked.')
  const created = Date.now()
  const sig = await id.sign(d.key, `web-ide push v1|${action}|${target}|${created}`)
  const r = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...body, cert: d.rec.cert, created, sig }) })
  const out = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(out.error || r.statusText)
  return { out, device: d.rec.key as string }
}

/** Enables push on this device: the permission, the subscription of the browser, its
 * registration with the relay, then with the pod. */
export async function enablePush() {
  if ((await Notification.requestPermission()) !== 'granted') throw new Error('Notifications are blocked for this site.')
  const r = await fetch('/_relay/push/key')
  const { key, error } = await r.json().catch(() => ({}))
  if (!r.ok || !key) throw new Error(error || 'push notifications are off on this relay')
  const reg = await navigator.serviceWorker.ready
  const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64d(key) }))
  const json = sub.toJSON()
  const { out, device } = await signed('/_relay/push/subscribe', 'subscribe', sub.endpoint, { endpoint: sub.endpoint })
  await request('push.subscribe', { device, sub: out.sub, p256dh: json.keys?.p256dh ?? b64u(sub.getKey('p256dh')!), auth: json.keys?.auth ?? b64u(sub.getKey('auth')!) })
}

/** Disables push on this device: the relay and the pod forget the subscription. */
export async function disablePush(relaySub?: string) {
  const device = relayDevice()
  if (relaySub) await signed('/_relay/push/unsubscribe', 'unsubscribe', relaySub, { sub: relaySub }).catch(() => {})
  await request('push.unsubscribe', { device })
  const reg = await navigator.serviceWorker.getRegistration()
  await (await reg?.pushManager.getSubscription())?.unsubscribe()
}
