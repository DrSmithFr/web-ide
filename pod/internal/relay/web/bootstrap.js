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

const params = new URLSearchParams(location.hash.slice(1) || location.search.slice(1))
const pod = params.get('pod')
const token = params.get('t') || params.get('token') || ''
const join = params.get('join')
let session = rand()

const fr = (navigator.languages?.[0] ?? navigator.language ?? 'en').startsWith('fr')
const words = {
  connecting: fr ? 'Connexion…' : 'Connecting…',
  nopod: fr ? 'Aucun pod indiqué. Ouvrez <relay>/#pod=<id>&t=<token>.' : 'No pod given. Open <relay>/#pod=<id>&t=<token>.',
  offline: fr ? 'Ce pod n’est pas en ligne sur le relay.' : 'This pod is not online at the relay.',
  loading: fr ? 'Chargement de l’IDE…' : 'Loading the IDE…',
  update: fr ? 'Mettez ce pod à jour : les versions ne correspondent pas.' : 'Update this pod: the versions do not match.',
  failed: fr ? 'La connexion n’a pas pu être établie.' : 'The connection could not be established.',
  closed: fr ? 'Ce pod a fermé la connexion.' : 'This pod closed the connection.',
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
// The handshake codes of the pod (tunnel.Code*), read as text by the page.
const codes = {
  unpaired: fr ? 'Le pod n’est pas apparié avec un compte.' : 'The pod is not paired with an account.',
  cert: fr ? 'Ce pod refuse le mode token : un certificat est nécessaire.' : 'This pod refuses the token mode: a certificate is needed.',
  foreign: fr ? 'Certificat d’un autre compte.' : 'Certificate of another account.',
  revoked: fr ? 'Appareil révoqué.' : 'Device revoked.',
  proof: fr ? 'La preuve ne correspond pas.' : 'The proof does not match.',
}
const status = (html) => { const el = document.getElementById('msg'); if (el) el.innerHTML = html }
let failed = false
let lastError = null
const fail = (m) => { failed = true; lastError = m; status('<div class="err">' + m + '</div>') }

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

let pc, rpc
let handshakeDone = false
let byeTimer = null
const buffers = new Map()
let appPort = null

function read(dc, e) {
  const r = unframe(e.data, buffers.get(dc))
  buffers.set(dc, r.buf)
  return r.msg
}

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
// is only the courier, it cannot sign one.
async function applyRevocations(l) {
  l.send({ type: 'register', account, name: deviceName(), cert: rec?.cert })
  const m = await l.recv()
  if (m.type === 'error') return fail(m.message)
  if (m.list) {
    const err = await id.verifyRevocations(m.list, account)
    if (err) return fail(err)
  }
  revoked = m.list ?? null
  if (revoked) await id.save(db, rec.key, { ...rec, revocations: revoked })
  return ''
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
  const l = await id.link()
  l.send({ type: 'register', account: '', name: deviceName() })
  const ready = await l.recv()
  if (ready.type === 'error') return fail(ready.message)
  l.send({ type: 'pairAttach', code, key: deviceId, name: deviceName(), kind: id.KindBrowser })
  const p = await l.recv()
  if (p.type === 'error') return fail(p.message)
  status(`${words.device} <b>${rec.fp}</b><br><span class="small">${words.waiting}</span>`)
  const ok = await l.recv()
  if (ok.type === 'error') return fail(ok.message)
  const c = ok.pairing?.cert
  if (!c) return fail('the approval did not come')
  const err = await id.verifyCert(c, c.account, null)
  if (err) return fail(err)
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
      await connect('cert')
    } catch (err) {
      fail(err.message)
    }
  }
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
    if (!pod) return fail(words.nopod)
    status('<div class="spin"></div>' + words.connecting + ' <b>' + pod + '</b>')
    const d = await id.unlock(db)
    if (d) {
      rec = d.rec
      rec.fp = await id.fingerprint(rec.key, wl)
      key = d.key
      account = rec.account ?? ''
      show()
    } else if (!token) {
      // Nothing paired on this origin and no token: the phrase form is the only way in.
      return showPhraseForm()
    }
  }

  const l = await id.link((m) => { if (m.type === 'revocations' && m.list) revoked = m.list })
  if (account) {
    const err = await applyRevocations(l)
    if (err) return
  }
  await connect(rec?.cert ? 'cert' : 'token')
}

async function connect(mode) {
  const sig = new WebSocket((location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/_relay/signal?pod=' + encodeURIComponent(pod) + '&session=' + session)
  sig.onmessage = (e) => {
    const m = JSON.parse(e.data)
    if (m.type === 'ice') start(m, mode, sig)
    else if (m.type === 'answer') pc.setRemoteDescription({ type: 'answer', sdp: m.sdp })
    else if (m.type === 'candidate') { try { pc.addIceCandidate(JSON.parse(m.candidate)) } catch {} }
    else if (m.type === 'bye') {
      // The refusal travels on the DataChannel (the handshake error), the bye on the signaling
      // link: the bye can arrive first. Give the proof a moment before saying the pod closed.
      if (handshakeDone || failed) return
      clearTimeout(byeTimer)
      byeTimer = setTimeout(() => { if (!handshakeDone && !failed) fail(lastError ?? words.closed) }, 600)
    }
  }
  sig.onerror = () => fail(words.offline)

  // Trickle ICE: the browser's candidates go to the relay, tagged with the session.
  const sendCandidate = (c) => {
    if (!c || !sig || sig.readyState !== WebSocket.OPEN) return
    sig.send(JSON.stringify({ type: 'candidate', session, candidate: c }))
  }

  async function start(ice, mode, sig) {
    pc = new RTCPeerConnection({ iceServers: [{ urls: ice.urls, username: ice.username, credential: ice.credential }] })
    window.tunnel = { pc, session, pod } // read by the e2e suite (getStats)
    rpc = pc.createDataChannel('rpc', { ordered: true })
    const nonceB = rand()
    rpc.onmessage = (e) => handleHello(e).catch((err) => fail(err.message))
    async function handleHello(e) {
      const m = read(rpc, e)
      if (!m) return
      if (handshakeDone) {
        if (appPort) appPort.postMessage(new TextDecoder().decode(m))
        return
      }
      const hello = JSON.parse(new TextDecoder().decode(m))
      const hp = hello.params || {}
      if (hp.error === 'version') return fail(words.update)
      if (hello.method === '$ready') {
        handshakeDone = true
        openApp()
        return
      }
      if (hello.method !== '$hello') return
      const fpB = fingerprint(pc.localDescription)
      const fpP = fingerprint(pc.remoteDescription)
      if (hp.error === 'unpaired' && token) {
        // The pod has no account: fall back to the token mode, on a fresh session.
        pc.close()
        session = rand()
        return connect('token')
      }
      if (hp.error) return fail(codes[hp.error] ?? hp.error)
      if (mode === 'token') {
        if ((await mac('pod', fpB, fpP, nonceB, hp.nonce)) !== hp.proof) return fail(words.failed)
        send(rpc, { id: 0, method: '$auth', params: { proof: await mac('browser', fpB, fpP, nonceB, hp.nonce) } })
        return
      }
      const err = await id.verifyCert(hp.cert, account, revoked)
      if (err) return fail(err)
      // The proof is signed by the device key of the peer, the one its certificate carries.
      if (!(await id.verify(hp.cert.key, hp.sig, id.binding('pod', fpB, fpP, nonceB, hp.nonce)))) return fail(words.failed)
      send(rpc, { id: 0, method: '$auth', params: { sig: await id.sign(key, id.binding('browser', fpB, fpP, nonceB, hp.nonce)) } })
    }
    rpc.onopen = () => {
      const hello = mode === 'token'
        ? { id: 0, method: '$hello', params: { protocol: 'web-ide-rpc', version: '1', nonce: nonceB } }
        : { id: 0, method: '$hello', params: { protocol: 'web-ide-rpc', version: '2', nonce: nonceB, cert: rec.cert } }
      send(rpc, hello)
    }
    pc.oniceconnectionstatechange = () => { if (pc.iceConnectionState === 'failed') fail(words.failed) }
    pc.onicecandidate = (e) => sendCandidate(e.candidate)
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    sig.send(JSON.stringify({ type: 'offer', session, sdp: offer.sdp }))
  }

  // The app runs in an iframe of the same origin: its fetches are answered by the Service
  // Worker, and its rpc channel is the one the handshake ran on.
  function openApp() {
    handshakeDone = true
    status(words.loading)
    const f = document.getElementById('frame')
    f.style.display = 'block'
    f.src = '/app'
    document.getElementById('status').style.display = 'none'
  }
}

function send(dc, obj) { frame(dc, new TextEncoder().encode(JSON.stringify(obj))) }

// The app (in its iframe) and the Service Worker ask the bootstrap for channels: the
// bootstrap owns the PeerConnection. The app posts on the parent window; the messages of
// the Service Worker arrive on navigator.serviceWorker, not on window.
function onMessage(d) {
  if (!d || !pc) return
  if (d.kind === 'port') bridge(d.label, d.port)
  else if (d.kind === 'http') httpFetch(d)
}
window.addEventListener('message', (e) => onMessage(e.data))
navigator.serviceWorker.addEventListener('message', (e) => onMessage(e.data))

function bridge(label, port) {
  const dc = label === 'rpc' ? rpc : pc.createDataChannel(label, { ordered: true })
  if (label === 'rpc') appPort = port
  // A DataChannel is not open when the app asks for it: keep its messages until it is, else
  // the first request of the window is dropped.
  const pending = []
  const write = (text) => {
    const bytes = new TextEncoder().encode(typeof text === 'string' ? text : JSON.stringify(text))
    if (dc.readyState === 'open') frame(dc, bytes)
    else pending.push(bytes)
  }
  port.onmessage = (e) => write(e.data)
  dc.onopen = () => { for (const b of pending.splice(0)) frame(dc, b) }
  dc.onmessage = (e) => {
    const m = read(dc, e)
    if (m) port.postMessage(new TextDecoder().decode(m))
  }
  dc.onclose = () => port.close()
  port.onclose = () => dc.close()
}

// One fetch of the page, answered by the pod on an http:<n> channel: the head as JSON,
// then the body as raw chunks, closed by an empty one.
function httpFetch(d) {
  const dc = pc.createDataChannel(d.id, { ordered: true })
  const sendReq = () => {
    if (dc.readyState !== 'open') return setTimeout(sendReq, 200)
    frame(dc, new TextEncoder().encode(JSON.stringify({
      path: d.url, preview: d.preview, method: d.method, headers: d.headers, body: d.body,
    })))
  }
  dc.onopen = sendReq
  dc.onmessage = (e) => {
    const m = read(dc, e)
    if (!m) return
    if (!d.head) {
      d.head = JSON.parse(new TextDecoder().decode(m))
      d.port.postMessage({ head: d.head })
      return
    }
    if (!m.length) {
      d.port.postMessage(null)
      dc.close()
      return
    }
    d.port.postMessage(m)
  }
  dc.onclose = () => d.port.postMessage(null)
}

// A rejected flow (a passkey dismissed, the wordlist unreachable) must not leave the page
// stuck on the spinner: the message names what failed.
main().catch((e) => fail(e.message))
