// Push notifications, on the relay origin. The app runs in the iframe of the bootstrap page,
// controlled by the Service Worker of the relay: the browser subscribes there with the VAPID
// key of the relay, registers its endpoint with the relay (signed by its device key) and gives
// the pod the id the relay answered and the keys the pod encrypts for. The relay never sees
// a title or a project name: it forwards what the pod encrypted.
import { lang } from '../i18n'
import type { PodInfo } from '../state/pods'
import { on, request } from './rpc'
import { askPort, inTunnel } from './transport'

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

// What this device subscribed with, kept on the origin: every pod of the account must know the
// subscription (a project of another pod notifies too), and a pod that shows up later is
// re-subscribed from here when the browser next connects to it.
interface PushRecord {
  sub: string
  p256dh: string
  auth: string
  endpoint: string
  kinds: Record<string, boolean>
}
const RECORD_KEY = 'webide.push'

function loadRecord(): PushRecord | null {
  try {
    const r = JSON.parse(localStorage.getItem(RECORD_KEY) ?? '')
    return r && r.sub && r.p256dh && r.auth ? r : null
  } catch {
    return null
  }
}
const saveRecord = (r: PushRecord) => localStorage.setItem(RECORD_KEY, JSON.stringify(r))
const clearRecord = () => localStorage.removeItem(RECORD_KEY)

/** The messages that (re)install this device on a pod: the subscription, then the switches the
 * user chose (a pod that never saw this device turns every kind on). */
const subscribeMessages = (device: string, rec: PushRecord) => [
  { method: 'push.subscribe', params: { device, sub: rec.sub, p256dh: rec.p256dh, auth: rec.auth } },
  ...Object.entries(rec.kinds).map(([kind, value]) => ({ method: 'push.settings', params: { device, kind, on: value } })),
]

let nextId = 1

/** One request on a side channel of another pod, then the channel is closed. The pod answers on
 * the channel it was asked on; one that is offline or refuses the channel is dropped after a
 * moment rather than holding the others. */
async function podRequest(key: string, messages: { method: string; params: any }[]) {
  const ch = await askPort('rpc:push', '/pod/' + key, true)
  if (!ch) return
  let done = () => {}
  const answered = new Promise<void>((res) => (done = res))
  let left = messages.length
  ch.onMessage((text) => {
    let m: any
    try {
      m = JSON.parse(text)
    } catch {
      return
    }
    if (m?.id && --left <= 0) done()
  })
  ch.onClose(done)
  // The pod translates the texts of its notifications for this client.
  ch.send(JSON.stringify({ id: 0, method: 'client.lang', params: { lang: lang() } }))
  for (const m of messages) ch.send(JSON.stringify({ id: nextId++, method: m.method, params: m.params }))
  await Promise.race([answered, new Promise((res) => setTimeout(res, 8000))])
  ch.close()
}

/** Repeats messages on every other pod of the account (pods.list never holds the pod that
 * answers it), one side channel each, in turn. A pod that fails never breaks the others. */
async function fanOut(messages: (p: PodInfo) => { method: string; params: any }[]) {
  if (!inTunnel()) return
  const pods = await request<PodInfo[]>('pods.list').catch(() => null)
  for (const p of pods ?? []) {
    try {
      await podRequest(p.key, messages(p))
    } catch {
      // Offline, refused, or no gateway for it: the next pod is asked all the same.
    }
  }
}

/** Enables push on this device: the permission, the subscription of the browser, its
 * registration with the relay, then with this pod and every pod of the account. */
export async function enablePush() {
  if ((await Notification.requestPermission()) !== 'granted') throw new Error('Notifications are blocked for this site.')
  const r = await fetch('/_relay/push/key')
  const { key, error } = await r.json().catch(() => ({}))
  if (!r.ok || !key) throw new Error(error || 'push notifications are off on this relay')
  const reg = await navigator.serviceWorker.ready
  const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64d(key) }))
  const json = sub.toJSON()
  const { out, device } = await signed('/_relay/push/subscribe', 'subscribe', sub.endpoint, { endpoint: sub.endpoint })
  const rec: PushRecord = {
    sub: out.sub,
    p256dh: json.keys?.p256dh ?? b64u(sub.getKey('p256dh')!),
    auth: json.keys?.auth ?? b64u(sub.getKey('auth')!),
    endpoint: sub.endpoint,
    kinds: loadRecord()?.kinds ?? {},
  }
  saveRecord(rec)
  await request('push.subscribe', { device, sub: rec.sub, p256dh: rec.p256dh, auth: rec.auth })
  await fanOut(() => subscribeMessages(device, rec))
}

/** Disables push on this device: the relay and every pod of the account forget the subscription. */
export async function disablePush(relaySub?: string) {
  const device = relayDevice()
  if (relaySub) await signed('/_relay/push/unsubscribe', 'unsubscribe', relaySub, { sub: relaySub }).catch(() => {})
  await request('push.unsubscribe', { device })
  await fanOut(() => [{ method: 'push.unsubscribe', params: { device } }])
  clearRecord()
  const reg = await navigator.serviceWorker.getRegistration()
  await (await reg?.pushManager.getSubscription())?.unsubscribe()
}

/** Turns one kind on or off for this device: remembered here, sent to this pod and to every
 * other pod of the account. */
export async function pushSetting(kind: string, on: boolean) {
  const device = relayDevice()
  const rec = loadRecord()
  const kinds = { ...(rec?.kinds ?? {}), [kind]: on }
  if (rec) saveRecord({ ...rec, kinds })
  await request('push.settings', { device, kind, on })
  await fanOut(() => [{ method: 'push.settings', params: { device, kind, on } }])
}

/** A pod that shows up later gets the subscription this device already holds: when this window
 * connects to a pod that never saw it, the record is replayed there. */
export function installPush() {
  const catchUp = async () => {
    const rec = loadRecord()
    const device = relayDevice()
    if (!rec || !device) return
    let st: { available?: boolean; subscribed?: boolean }
    try {
      st = await request('push.status', { device })
    } catch {
      return
    }
    if (!st?.available || st.subscribed) return
    for (const m of subscribeMessages(device, rec)) await request(m.method, m.params).catch(() => {})
  }
  // This window's own pod first (the request waits in the queue of the connection), then every
  // reconnection: a pod the browser reaches for the first time gets the record replayed.
  void catchUp()
  on('pod.connected', catchUp)
  on('pod.reconnected', catchUp)
}
