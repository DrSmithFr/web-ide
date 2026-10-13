// Bootstrap page of the relay. It opens the WebRTC tunnel to the pod (chosen by #pod=),
// proves the identity INSIDE the tunnel (the channel-binding handshake: the proof is bound to
// both DTLS fingerprints, so the relay never sees the token and cannot swap the SDP), then
// hands the app to the pod: the Service Worker answers the page's fetches through the tunnel,
// and the JSON protocol goes through a MessagePort.
//
// A paired device signs the binding string with its device key and checks the pod's
// certificate against the account key it pinned. An unpaired device falls back to the token
// mode (M1), which a paired pod refuses. Recent browsers only: WebCrypto Ed25519 and WebAuthn
// PRF are required, nothing is attempted without them.
import * as id from './identity.js'
import { keyOf, nextGateway, pickGateway, pickSite, routeOf } from './routes.js'

const params = new URLSearchParams(location.hash.slice(1) || location.search.slice(1))
// The pod of the link, else the last one opened here: the installed app (PWA) starts at /.
let pod = params.get('pod') || (params.get('join') ? null : remembered())
const token = params.get('t') || params.get('token') || ''
const join = params.get('join')
// A path of the app to open instead of its home page (the link of a notification).
let openPath = safePath(params.get('open'))

const fr = (navigator.languages?.[0] ?? navigator.language ?? 'en').startsWith('fr')
const words = {
  connecting: fr ? 'Connexion…' : 'Connecting…',
  nopod: fr ? 'Aucun pod indiqué. Ouvrez <relay>/#pod=<id>&t=<token>.' : 'No pod given. Open <relay>/#pod=<id>&t=<token>.',
  nopodonline: fr ? 'Aucun pod en ligne sur le relay. Pods de ce compte : ' : 'No pod online at the relay. Pods of this account: ',
  offline: fr ? 'Ce pod n’est pas en ligne sur le relay.' : 'This pod is not online at the relay.',
  loading: fr ? 'Chargement de l’IDE…' : 'Loading the IDE…',
  update: fr ? 'Mettez ce pod à jour : les versions ne correspondent pas.' : 'Update this pod: the versions do not match.',
  failed: fr ? 'La connexion n’a pas pu être établie.' : 'The connection could not be established.',
  closed: fr ? 'Ce pod a fermé la connexion.' : 'This pod closed the connection.',
  reconnecting: fr ? 'Reconnexion par ' : 'Reconnecting through ',
  wrongpod: fr ? 'Ce pod n’est pas celui demandé.' : 'This pod is not the one asked for.',
  sw: fr ? 'Service Worker : ' : 'Service Worker: ',
  needs: fr ? 'Ce navigateur manque : ' : 'This browser is missing: ',
  device: fr ? 'Cet appareil' : 'This device',
  compare: fr ? 'Comparez cette empreinte avec celle affichée de l’autre côté avant d’approuver.' : 'Compare this fingerprint with the one shown on the other side before approving.',
  phrase: fr ? 'Phrase de récupération (24 mots), jamais enregistrée.' : 'Recovery phrase (24 words), never stored.',
  pair: fr ? 'Aucun appareil sur cette origine : entrez la phrase pour l’apparier.' : 'No device on this origin: type the recovery phrase to pair it.',
  waiting: fr ? 'En attente de l’approbation d’un administrateur…' : 'Waiting for an admin to approve…',
  paired: fr ? 'Apparié.' : 'Paired.',
  unlock: fr ? 'Déverrouillage de la clé de l’appareil…' : 'Unlocking the device key…',
}
// The texts the page shows for what the pod or the identity module answers: the handshake
// codes (tunnel.Code*) and the error keys of the identity module, the same words as the pod's
// catalog (pod/internal/i18n/fr.json). The page has its own words, not the app catalog.
const texts = {
  unpaired: fr ? 'Le pod n’est pas apparié avec un compte.' : 'The pod is not paired with an account.',
  cert: fr ? 'Ce pod refuse le mode token : un certificat est nécessaire.' : 'This pod refuses the token mode: a certificate is needed.',
  foreign: fr ? 'Certificat d’un autre compte.' : 'Certificate of another account.',
  revoked: fr ? 'Appareil révoqué.' : 'Device revoked.',
  proof: fr ? 'La preuve ne correspond pas.' : 'The proof does not match.',
  'no certificate': fr ? 'Pas de certificat.' : 'No certificate.',
  'certificate of another account': fr ? 'Certificat d’un autre compte.' : 'Certificate of another account.',
  'the certificate signature does not match': fr ? 'La signature du certificat ne correspond pas.' : 'The certificate signature does not match.',
  'device revoked': fr ? 'Appareil révoqué.' : 'Device revoked.',
  'no revocation list': fr ? 'Pas de liste de révocation.' : 'No revocation list.',
  'revocation list of another account': fr ? 'Liste de révocation d’un autre compte.' : 'Revocation list of another account.',
  'the revocation list signature does not match': fr ? 'La signature de la liste de révocation ne correspond pas.' : 'The revocation list signature does not match.',
  'the approval did not come': fr ? "L'approbation n'est pas venue." : 'The approval did not come.',
  'the relay is not reachable': fr ? 'Le relay est injoignable.' : 'The relay is not reachable.',
  'the wordlist is not reachable': fr ? 'La liste de mots est injoignable.' : 'The wordlist is not reachable.',
  'the passkey did not give its PRF (WebAuthn PRF is required)': fr ? 'La clé d’accès n’a pas donné sa PRF (WebAuthn PRF est nécessaire).' : 'The passkey did not give its PRF (WebAuthn PRF is required).',
}
const lang = fr ? 'fr' : 'en'
const text = (key) => texts[key] ?? key
const status = (html) => { const el = document.getElementById('msg'); if (el) el.innerHTML = html }
let failed = false
let lastError = null
const fail = (m) => { failed = true; lastError = m; status('<div class="err">' + m + '</div>') }

function remembered() {
  try {
    return localStorage.getItem('webide.relay.pod')
  } catch {
    return null
  }
}
function remember(id) {
  try {
    localStorage.setItem('webide.relay.pod', id)
  } catch {}
}
function rememberedSite() {
  try {
    return localStorage.getItem('webide.relay.site')
  } catch {
    return null
  }
}
function rememberSite(id) {
  try {
    if (id) localStorage.setItem('webide.relay.site', id)
  } catch {}
}
// Only a path of this origin: a link must not turn the frame into another site.
function safePath(p) {
  return p && p.startsWith('/') && !p.startsWith('//') && !p.startsWith('/\\') ? p : null
}

function rand() {
  const b = new Uint8Array(16)
  crypto.getRandomValues(b)
  return [...b].map((x) => x.toString(16).padStart(2, '0')).join('')
}

// The pod frames every message into 16 KB fragments with a 1-byte header (1 = more, 0 =
// last), so a 2 MB transfer stays under the DataChannel cap. Both sides must frame.
const FRAG = 16384
function frame(dc, bytes) {
  for (let i = 0; i < bytes.length; i += FRAG) {
    const part = bytes.subarray(i, i + FRAG)
    const frag = new Uint8Array(1 + part.length)
    frag[0] = i + FRAG < bytes.length ? 1 : 0
    frag.set(part, 1)
    dc.send(frag)
  }
}
function bytes(d) {
  if (d instanceof Uint8Array) return d
  if (d instanceof ArrayBuffer) return new Uint8Array(d)
  return new TextEncoder().encode(String(d))
}
function unframe(data, buf) {
  const b = bytes(data)
  if (!b.length) return { msg: null, buf }
  buf = (buf ?? []).concat([...b.subarray(1)])
  if (b[0] & 1) return { msg: null, buf }
  return { msg: new Uint8Array(buf), buf: null }
}

// HMAC-SHA256, the same binding as the pod: role|fpBrowser|fpPod|nonceBrowser|noncePod.
async function mac(role, fpB, fpP, nonceB, nonceP) {
  const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(token), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign'])
  const sig = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(id.binding(role, fpB, fpP, nonceB, nonceP)))
  return [...new Uint8Array(sig)].map((x) => x.toString(16).padStart(2, '0')).join('')
}
const fingerprint = (desc) => {
  const m = desc?.sdp?.match(/a=fingerprint:sha-256 ([0-9a-fA-F:]+)/)
  return m ? m[1].replace(/:/g, '').toUpperCase() : ''
}

// One tunnel per pod this window reaches. A pod the relay sees gets its own PeerConnection; a
// pod it does not see (a pod of the LAN of a gateway) rides the tunnel of its gateway, its
// channels labelled @<key>/<label> (relay.md, *Multi-pod*).
const tunnels = new Map()
let entry = null // the tunnel of the gateway this window entered through
let entryKey = '' // its device key, and the pod id the relay knows it by
let entryPod = ''
let entrySite = ''
let shownKey = '' // the pod the frame shows (a pod of the site, not always the gateway)
let appPort = null
let entryMode = 'token'
let pods = [] // the pods the relay reports: {key, podId, name, site, gateway, preferred}
let podsPrev = [] // the pods list as it was before the last one
let link = null // the identity link, kept for the pods list and the failover
const buffers = new Map()

function read(dc, e) {
  const r = unframe(e.data, buffers.get(dc))
  buffers.set(dc, r.buf)
  return r.msg
}

// A channel read as a sequence of whole messages: the handshake and the bridges are written as
// linear flows, not as a state machine spread over onmessage callbacks.
function reader(dc) {
  const q = []
  let wait = null
  let closed = false
  dc.onmessage = (e) => {
    const m = read(dc, e)
    if (!m) return
    if (wait) {
      const r = wait
      wait = null
      r(m)
    } else q.push(m)
  }
  dc.onclose = () => {
    closed = true
    if (wait) {
      const r = wait
      wait = null
      r(null)
    }
  }
  return {
    next: () =>
      closed
        ? Promise.resolve(null)
        : q.length
          ? Promise.resolve(q.shift())
          : new Promise((r) => (wait = r)),
  }
}

const opened = (dc) => (dc.readyState === 'open' ? Promise.resolve() : new Promise((r) => (dc.onopen = r)))
const decode = (m) => new TextDecoder().decode(m)

// --- the device of this origin ---

let db = null
let rec = null
let key = null
let account = ''
let revoked = null

const deviceName = () => navigator.userAgentData?.platform || navigator.platform || 'browser'

// What the e2e suite reads (the device id, its fingerprint, the account).
function show() {
  window.identity = { device: rec?.key ?? '', account, fingerprint: rec?.fp ?? '', cert: rec?.cert ?? null }
}

// The revocation list is checked against the pinned account before it is believed: the relay
// is only the courier, it cannot sign one. The link proves possession of this device's key with
// the nonce the relay sends: only then does it learn which pods are online.
async function applyRevocations(l) {
  l.send({ type: 'register', account, name: deviceName(), cert: rec?.cert, lang, prove: !!key })
  if (key) {
    const err = await id.prove(l, key, location.host)
    if (err) return fail(text(err.message ?? err.type))
  }
  const m = await l.recv()
  if (m.type === 'error') return fail(text(m.message))
  if (m.list) {
    const err = await id.verifyRevocations(m.list, account)
    if (err) return fail(text(err))
  }
  revoked = m.list ?? null
  if (revoked) await id.save(db, rec.key, { ...rec, revocations: revoked })
  return ''
}

// Milestone 3: a link with no pod in it enters the account through a gateway. The relay says who
// is online and which pod it elected for each site — it is only the courier, the names come from
// the certificates this device already verified. The site used last wins, else the first site.
async function enterSite(l) {
  const m = await l.recv('pods')
  pods = m.pods ?? []
  if (!pods.length) {
    l.send({ type: 'devices' })
    const d = await l.recv('devices')
    const names = (d.devices ?? []).filter((x) => x.cert?.kind === id.KindPod).map((x) => x.cert.name)
    if (!names.length) return fail(words.nopod)
    return fail(words.nopodonline + names.map((n) => '<b>' + n + '</b>').join(', '))
  }
  const chosen = enterGateway()
  rememberSite(chosen.site)
  pod = chosen.podId
  remember(pod)
  // The Service Worker answers the fetches of the app by finding the bootstrap page by its
  // "pod=": the pod picked here goes in the hash (a hash does not reload the page).
  location.hash = 'pod=' + pod
  status('<div class="spin"></div>' + words.connecting + ' <b>' + (chosen.name || chosen.podId) + '</b>')
  return ''
}

// The gateway of the site this window enters (routes.js, tested by scripts/routes-test.mjs).
function enterGateway() {
  const { site, pod } = pickSite(pods, rememberedSite())
  entrySite = site
  return pod ?? pods[0]
}

// Joining by an invite link (#join=<code>), usually opened by a phone from the QR. The device
// makes its own key, shows its fingerprint, posts the key to the code and waits for the
// approval. The certificate that comes back must verify against the account it names: the
// relay cannot sign one, so a signature that does not match is a swapped key.
async function joinFlow(code, wl) {
  const deviceSeed = crypto.getRandomValues(new Uint8Array(32))
  const deviceId = await id.publicFromSeed(deviceSeed)
  rec = id.newRecord(deviceId)
  rec.fp = await id.fingerprint(deviceId, wl)
  show()
  status(`${words.device} <b>${rec.fp}</b><br><span class="small">${words.compare}</span>`)
  const l = await id.link(null, lang)
  l.send({ type: 'register', account: '', name: deviceName(), lang })
  const ready = await l.recv()
  if (ready.type === 'error') return fail(text(ready.message))
  l.send({ type: 'pairAttach', code, key: deviceId, name: deviceName(), kind: id.KindBrowser })
  const p = await l.recv()
  if (p.type === 'error') return fail(text(p.message))
  status(`${words.device} <b>${rec.fp}</b><br><span class="small">${words.waiting}</span>`)
  const ok = await l.recv()
  if (ok.type === 'error') return fail(text(ok.message))
  const c = ok.pairing?.cert
  if (!c) return fail(text('the approval did not come'))
  const err = await id.verifyCert(c, c.account, null)
  if (err) return fail(text(err))
  const prf = await id.passkey(rec, true)
  rec.id = prf.id
  rec.account = c.account
  rec.cert = c
  rec.device = await id.seal(prf.prf, deviceSeed)
  await id.save(db, deviceId, rec)
  key = await id.keyFromSeed(deviceSeed, false)
  account = c.account
  status(words.paired)
  return ''
}

function showPhraseForm() {
  const f = document.getElementById('pair')
  if (!f) return
  status(words.pair)
  f.hidden = false
  const input = document.getElementById('phrase')
  input.placeholder = words.phrase // the bootstrap page has its own words, not the app catalog
  // The form must not submit: that would reload the page and lose the tunnel.
  f.onsubmit = async (e) => {
    e.preventDefault()
    if (!input.value.trim()) return
    try {
      const d = await id.enroll(db, input.value.trim(), await id.wordlist(), deviceName(), id.KindBrowser)
      rec = d.rec
      rec.fp = await id.fingerprint(rec.key, await id.wordlist())
      key = d.key
      account = rec.account
      show()
      status(`${words.paired} <b>${rec.fp}</b>`)
      await enter('cert')
    } catch (err) {
      fail(text(err.message))
    }
  }
}

// The link of the account: the proof of possession, the revocation list, the pods the relay
// reports, and — when the link named no pod — the gateway this browser enters through.
async function enter(mode) {
  entryMode = mode
  const l = await id.link(onPush, lang)
  link = l
  if (account) {
    const err = await applyRevocations(l)
    if (err) return err
    if (!pod) {
      const err = await enterSite(l)
      if (err) return err
    } else {
      const m = await l.recv('pods')
      pods = m.pods ?? []
    }
  }
  if (!pod) return fail(words.nopod)
  const p = pods.find((p) => p.podId === pod)
  entryKey = p?.key ?? ''
  entryPod = pod
  entrySite = p?.site ?? ''
  shownKey = entryKey
  entry = await connect(entryKey, pod, mode)
  openApp()
  return ''
}

// What the identity link pushes: the revocation list, and the pods of the account as the relay
// sees them (a gateway that left or came back changes that list).
function onPush(m) {
  if (m.type === 'revocations' && m.list) revoked = m.list
  if (m.type === 'pods') {
    podsPrev = pods // the list before this one: who was with whom before the gateway left
    pods = m.pods ?? []
    syncSite()
  }
}

// A site is named after the pods of the local network, so its id moves when a pod joins or leaves
// it. The site this window entered through must follow that: a failover looks for the pods of the
// site as the relay names it now.
function syncSite() {
  const e = pods.find((p) => p.key === entryKey)
  if (e?.site) entrySite = e.site
}

async function main() {
  const missing = await id.support()
  if (missing.length) return fail(words.needs + missing.join(', '))

  try {
    const reg = await navigator.serviceWorker.register('/sw.js', { scope: '/' })
    // The app is loaded through the Service Worker: wait until it controls the page, else
    // the first fetch goes to the relay and answers 404.
    await reg.ready
    if (!reg.active) await reg.update()
    await reg.ready
  } catch (e) {
    fail(words.sw + e.message)
  }

  db = await id.openStore()
  const wl = await id.wordlist()

  // An invite link carries the pod its author was on: the joining device pairs first, then
  // opens that pod. Without a pod it stops at the pairing.
  if (join) {
    const err = await joinFlow(join, wl)
    if (err) return
    if (!pod) return fail(words.nopod)
  } else {
    status('<div class="spin"></div>' + words.connecting + (pod ? ' <b>' + pod + '</b>' : ''))
    const d = await id.unlock(db)
    if (d) {
      rec = d.rec
      rec.fp = await id.fingerprint(rec.key, wl)
      key = d.key
      account = rec.account ?? ''
      show()
    } else if (!token) {
      // Nothing paired on this origin and no token: the phrase form is the only way in. It
      // ends on the same link as an unlocked device, so it can pick a gateway too.
      showPhraseForm()
      return
    }
  }
  await enter(rec?.cert ? 'cert' : 'token')
}

// --- one tunnel per pod ---

// The tunnel of a pod registered at the relay: a PeerConnection of its own, with the handshake v2
// run inside it (the proof is bound to both DTLS fingerprints, so the relay never sees the token
// nor can it swap the SDP). A pod the relay does not see rides the tunnel of its gateway.
function connect(key, podId, mode) {
  const t = { key, podId, mode, session: rand(), chans: new Map() }
  tunnels.set(key, t)
  t.ready = new Promise((resolve, reject) => {
    t.resolve = resolve
    t.reject = reject
  })
  t.ready.catch(() => {}) // a tunnel nobody waited for must not shout in the console
  const url = (location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/_relay/signal?pod=' + encodeURIComponent(podId) + '&session=' + t.session
  const sig = new WebSocket(url)
  t.sig = sig
  sig.onmessage = (e) => {
    const m = JSON.parse(e.data)
    if (m.type === 'ice') start(t, m).catch((err) => drop(t, err.message))
    else if (m.type === 'answer') t.pc?.setRemoteDescription({ type: 'answer', sdp: m.sdp }).catch(() => {})
    else if (m.type === 'candidate') { try { t.pc?.addIceCandidate(JSON.parse(m.candidate)) } catch {} }
    else if (m.type === 'error') drop(t, text(m.message))
    else if (m.type === 'bye') {
      // The pod is gone: the relay says so on the signaling link. A refusal also travels on the
      // DataChannel (the handshake error), which can arrive first: give it that moment.
      setTimeout(() => { if (!t.dropped) drop(t, lastError ?? words.closed) }, 600)
    }
  }
  sig.onerror = () => drop(t, words.offline)
  return t.ready
}

// The tunnel that carries the channels of a pod: its own when the relay reports it online, else
// the one of the gateway of its site, with every label prefixed by @<key>/ (the gateway opens
// them on its own link to the peer). Null when no tunnel can reach that pod.
async function route(key) {
  const { via, prefix } = routeOf(pods, key || entryKey, entryKey)
  if (!via) {
    // A link that names no pod (the token mode, before the pods are known): the page rides the
    // tunnel this window opened.
    return { t: await ensure(entryKey, entryPod), prefix }
  }
  const own = tunnels.get(via)
  if (own) {
    try {
      return { t: await own.ready, prefix }
    } catch {
      return null
    }
  }
  return { t: await ensure(via, pods.find((p) => p.key === via)?.podId), prefix }
}

// The tunnel of a pod, opened when this window does not have one yet. Null when it cannot be
// reached: the pod is not at the relay and this window has no gateway for it.
async function ensure(key, podId) {
  if (key === entryKey) {
    if (!entry || entry.dropped) {
      try {
        entry = await connect(key, podId ?? entryPod, entryMode)
      } catch {
        return null
      }
    }
    return entry
  }
  if (!podId) {
    if (!entry || entry.dropped) {
      try {
        entry = await connect(entryKey, entryPod, entryMode)
      } catch {
        return null
      }
    }
    return entry
  }
  try {
    return await connect(key, podId, entryMode)
  } catch {
    return null
  }
}

async function start(t, ice) {
  const pc = new RTCPeerConnection({ iceServers: [{ urls: ice.urls, username: ice.username, credential: ice.credential }] })
  t.pc = pc
  window.tunnels = tunnels
  window.tunnel = { pc, session: t.session, pod: t.podId } // read by the e2e suite (getStats)
  const rpc = pc.createDataChannel('rpc', { ordered: true })
  t.rpc = rpc
  rpc.onclose = () => drop(t, words.closed)
  pc.oniceconnectionstatechange = () => { if (pc.iceConnectionState === 'failed') drop(t, words.failed) }
  pc.onicecandidate = (e) => sendCandidate(t, e.candidate)
  const offer = await pc.createOffer()
  await pc.setLocalDescription(offer)
  t.sig.send(JSON.stringify({ type: 'offer', session: t.session, sdp: offer.sdp }))
  const r = await handshake(t, rpc, t.mode, t.key)
  if (r.ok) {
    t.done = true
    t.rpcReader = r.rd
    t.resolve(t)
    if (t.key === entryKey) entry = t
    declare()
    return
  }
  if (r.unpaired && token) {
    // The pod has no account: fall back to the token mode, on a fresh session.
    close(t)
    return connect(t.key, t.podId, 'token').then(() => {}, () => {})
  }
  drop(t, r.error)
}

// The handshake of one rpc channel. want is the device key of the pod expected at the other end:
// a tunnel must not answer with another pod of the account, even one the relay routed it to.
// fpB/fpP are the DTLS fingerprints the proof is bound to; a channel that rides another tunnel
// (a routed pod) is bound to the fingerprints of the tunnel it rides on.
async function handshake(t, dc, mode, want, fpB, fpP) {
  await opened(dc)
  fpB = fpB ?? fingerprint(t.pc.localDescription)
  fpP = fpP ?? fingerprint(t.pc.remoteDescription)
  // A channel that rides this tunnel is bound to the fingerprints of the tunnel itself.
  t.fpB = fpB
  t.fpP = fpP
  const rd = reader(dc)
  const nonceB = rand()
  send(dc, mode === 'token'
    ? { id: 0, method: '$hello', params: { protocol: 'web-ide-rpc', version: '1', nonce: nonceB } }
    : { id: 0, method: '$hello', params: { protocol: 'web-ide-rpc', version: '2', nonce: nonceB, cert: rec.cert } })
  const hello = JSON.parse(decode(await rd.next()))
  if (!hello) return { error: words.closed }
  const hp = hello.params || {}
  if (hp.error === 'version') return { error: words.update }
  if (hp.error === 'unpaired' && token) return { unpaired: true }
  if (hp.error) return { error: text(hp.error) }
  if (mode === 'token') {
    if ((await mac('pod', fpB, fpP, nonceB, hp.nonce)) !== hp.proof) return { error: words.failed }
    send(dc, { id: 0, method: '$auth', params: { proof: await mac('browser', fpB, fpP, nonceB, hp.nonce) } })
  } else {
    const err = await id.verifyCert(hp.cert, account, revoked)
    if (err) return { error: text(err) }
    if (want && hp.cert?.key !== want) return { error: words.wrongpod }
    // The proof is signed by the device key of the peer, the one its certificate carries.
    if (!(await id.verify(hp.cert.key, hp.sig, id.binding('pod', fpB, fpP, nonceB, hp.nonce)))) return { error: words.failed }
    send(dc, { id: 0, method: '$auth', params: { sig: await id.sign(key, id.binding('browser', fpB, fpP, nonceB, hp.nonce)) } })
  }
  const ready = JSON.parse(decode(await rd.next()) ?? '{}')
  return ready.method === '$ready' ? { ok: true, rd } : { error: lastError ?? words.failed }
}

function sendCandidate(t, c) {
  if (!c || !t.sig || t.sig.readyState !== WebSocket.OPEN) return
  t.sig.send(JSON.stringify({ type: 'candidate', session: t.session, candidate: c }))
}

function close(t) {
  try { t.pc?.close() } catch {}
  try { t.sig?.close() } catch {}
  t.pc = null
  t.sig = null
}

// One PeerConnection per window and pod adds up: a tunnel no channel of this window uses any
// more is closed after two idle minutes (relay.md, *Routing*). The tunnel this window entered
// through and the one of the page it shows are kept: the next fetch of the page must not pay
// for a new handshake.
const IDLE = 2 * 60 * 1000
const used = (t) => (t.used = Date.now())
const held = (t, on) => {
  t.refs = (t.refs ?? 0) + (on ? 1 : -1)
  return used(t)
}
setInterval(() => {
  const now = Date.now()
  for (const t of [...tunnels.values()]) {
    for (const [key, e] of t.routed ?? []) {
      // A window opened on the gateway for a pod this page does not show any more.
      if (key !== shownKey && key !== entryKey && now - (e.used ?? 0) >= IDLE) {
        try { e.dc.close() } catch {}
        t.routed.delete(key)
      }
    }
    if (t.dropped || t.refs || t.key === entryKey || t.key === shownKey || now - (t.used ?? 0) < IDLE) continue
    tunnels.delete(t.key)
    t.dropped = true
    close(t)
    declare()
  }
}, 15 * 1000)

// A tunnel that ended: its channels are gone, the app will ask for them again. The gateway this
// window entered through is the one case where the browser must go looking for another one.
function drop(t, msg) {
  if (t.dropped) return
  t.dropped = true
  lastError = msg
  close(t)
  if (tunnels.get(t.key) === t) tunnels.delete(t.key)
  t.reject?.(new Error(msg))
  if (t.key === entryKey) void failover()
  else if (shownKey === t.key) status('<div class="err">' + msg + '</div>')
}

// The gateway left: ask the relay who carries the site now and reconnect through it, with a
// waiting time that grows. The pages of the pods reached directly are not touched.
async function failover() {
  if (failed) return
  status('<div class="spin"></div>' + words.reconnecting)
  for (let i = 0; i < 6; i++) {
    await refreshPods()
    // The relay has not elected one yet: the pod this window was on is worth another try.
    const g = entryGateway()
    if (g) {
      const old = entryKey
      entryKey = g.key
      entryPod = g.podId
      rememberSite(g.site)
      status('<div class="spin"></div>' + words.reconnecting + ' <b>' + (g.name || g.podId) + '</b>')
      try {
        entry = await connect(g.key, g.podId, entryMode)
        if (shownKey === old) {
          // This window showed the pod that left: the same path, on the new gateway. A page
          // reached on its own tunnel is left alone: the app asks its channels again, and a page
          // of a pod of the site rides the new gateway under its own /pod/<key>/.
          shownKey = g.key
          openPath = appPath() ?? openPath
          openApp()
        } else {
          declare()
        }
        return
      } catch {}
    }
    await new Promise((r) => setTimeout(r, Math.min(8000, 500 * 2 ** i)))
  }
  fail(lastError ?? words.closed)
}

// The gateway to reconnect through (routes.js). A pod the relay does not report would refuse the
// signaling link: only the pods list is worth dialling, except when it says nothing at all (the
// link of the account is not up yet).
function entryGateway() {
  return nextGateway(pods, podsPrev, entrySite, entryKey) ??
    (pods.length || !entryPod ? null : { key: entryKey, podId: entryPod, site: entrySite })
}

// The pods as the relay sees them now: the link pushes them, and a gateway that just left has
// already been replaced in that push.
async function refreshPods() {
  if (!link) return
  try {
    const m = await Promise.race([link.recv('pods'), new Promise((r) => setTimeout(() => r(null), 2000))])
    if (m?.pods) {
      pods = m.pods
      syncSite()
    }
  } catch {}
}

// The app runs in an iframe of the same origin: its fetches are answered by the Service Worker,
// and its rpc channel is a DataChannel of the tunnel of its pod. One origin for every pod: the
// installed app never leaves it.
function openApp() {
  status(words.loading)
  const f = document.getElementById('frame')
  f.style.display = 'block'
  f.src = (shownKey ? '/pod/' + shownKey : '') + (openPath ?? '/app')
  document.getElementById('status').style.display = 'none'
  remember(entryPod)
  declare()
}

// The path the app of this window is on, without the /pod/<key>/ prefix: on a failover the page
// keeps it, it does not fall back to the home page.
function appPath() {
  try {
    const u = new URL(document.getElementById('frame').contentWindow.location.href)
    const prefix = shownKey ? '/pod/' + shownKey : ''
    if (prefix && u.pathname.startsWith(prefix)) return u.pathname.slice(prefix.length) + u.search || '/'
    return u.pathname + u.search
  } catch {
    return null
  }
}

// What the Service Worker needs to know to answer the fetches of this window: which pod its frame
// shows, and which pods it can reach.
function declare() {
  try {
    navigator.serviceWorker?.controller?.postMessage({
      kind: 'own', pod: entryPod, key: shownKey, keys: [...tunnels.keys(), shownKey],
    })
  } catch {}
}

// The app (in its iframe) and the Service Worker ask the bootstrap for channels: the
// bootstrap owns the PeerConnections. The app posts on the parent window; the messages of
// the Service Worker arrive on navigator.serviceWorker, not on window.
async function onMessage(d) {
  // A notification clicked: the Service Worker moves the app of this window to its path.
  if (d?.kind === 'open') {
    const p = safePath(d.url)
    if (p) {
      openPath = p
      openApp()
    }
    return
  }
  if (!d) return
  if (d.kind === 'port') {
    const r = await route(keyOf(d.base))
    if (!r) {
      d.port.close()
      return
    }
    bridge(r.t, d.label, d.port, r.prefix, d.side).catch(() => d.port.close())
  } else if (d.kind === 'http') {
    const r = await route(keyOf(d.base))
    if (!r) {
      d.port.postMessage(null)
      return
    }
    try {
      if (r.prefix) await routedRPC(r.t, r.prefix)
    } catch (err) {
      d.port.postMessage(null)
      return
    }
    httpFetch(r.t, d, r.prefix)
  }
}
window.addEventListener('message', (e) => onMessage(e.data))
navigator.serviceWorker.addEventListener('message', (e) => onMessage(e.data))

// One channel of a pod, wired to the MessagePort of the app. A routed channel (@<key>/label)
// runs its own handshake on the tunnel of the gateway, bound to the fingerprints of that tunnel:
// the pod at the other end checks the browser's proof against them, and the gateway vouches for
// the browser with the certificate it checked.
async function bridge(t, label, port, prefix, side) {
  const own = !prefix && label === 'rpc' && !side
  if (label === 'rpc' && !side) appPort = port
  // A pod reached through the gateway is a *window* on that pod: its rpc channel is opened
  // first, and every other channel of the page rides it. On the pod's own tunnel, the extra
  // channels are DataChannels of their own.
  const routed = prefix ? await routedRPC(t, prefix) : null
  const dc = own ? t.rpc : routed ? routed.dc : t.pc.createDataChannel(label, { ordered: true })
  const pending = []
  held(t, true)
  const write = (text) => {
    const bytes = new TextEncoder().encode(typeof text === 'string' ? text : JSON.stringify(text))
    if (dc.readyState === 'open') frame(dc, bytes)
    else pending.push(bytes)
    used(t)
    if (routed) routed.used = Date.now()
  }
  port.onmessage = (e) => write(e.data)
  port.onclose = () => (own ? close(t) : dc.close())
  const rd = own ? t.rpcReader : routed ? routed.rd : reader(dc)
  // A DataChannel is not open when the app asks for it: keep its messages until it is, else
  // the first request of the window is dropped.
  const flush = () => {
    for (const b of pending.splice(0)) frame(dc, b)
  }
  if (dc.readyState === 'open') flush()
  else dc.addEventListener('open', flush)
  for (;;) {
    const m = await rd.next()
    if (!m) break
    used(t)
    if (routed) routed.used = Date.now()
    port.postMessage(decode(m))
  }
  held(t, false)
  port.close()
}

// The rpc channel of a pod reached through the gateway: it is the window the pod serves the
// other channels on, so it is opened before them, and kept while the page lives. Its handshake
// is the browser's own, bound to the fingerprints of the tunnel it rides on.
async function routedRPC(t, prefix) {
  const key = prefix.slice(1, -1)
  t.routed = t.routed ?? new Map()
  const have = t.routed.get(key)
  if (have) return (have.used = Date.now()), have
  const dc = t.pc.createDataChannel(prefix + 'rpc', { ordered: true })
  const r = await handshake(t, dc, 'cert', key, t.fpB, t.fpP)
  if (!r.ok) throw new Error(r.error)
  const e = { dc, rd: r.rd, used: Date.now() }
  t.routed.set(key, e)
  dc.onclose = () => {
    t.routed.delete(key)
    if (shownKey === key) status('<div class="err">' + words.closed + '</div>')
  }
  return e
}

// One fetch of the page, answered by the pod on an http:<n> channel: the head as JSON,
// then the body as raw chunks, closed by an empty one.
function httpFetch(t, d, prefix) {
  const dc = t.pc.createDataChannel((prefix ?? '') + d.id, { ordered: true })
  held(t, true)
  const sendReq = () => {
    if (dc.readyState !== 'open') return setTimeout(sendReq, 200)
    frame(dc, new TextEncoder().encode(JSON.stringify({
      path: d.url, preview: d.preview, base: d.base, method: d.method, headers: d.headers, body: d.body,
    })))
    used(t)
  }
  dc.onopen = sendReq
  let head = null
  const rd = reader(dc)
  void (async () => {
    for (;;) {
      const m = await rd.next()
      // The pod closes a body with an empty message: the Service Worker reads it as the end.
      if (!m || !m.length) break
      used(t)
      if (!head) {
        head = JSON.parse(decode(m))
        d.port.postMessage({ head })
        continue
      }
      d.port.postMessage(m)
    }
    d.port.postMessage(null)
    dc.close()
    held(t, false)
  })()
}

function send(dc, obj) { frame(dc, new TextEncoder().encode(JSON.stringify(obj))) }

// A rejected flow (a passkey dismissed, the wordlist unreachable) must not leave the page
// stuck on the spinner: the message names what failed.
main().catch((e) => fail(e.message))
