// Bootstrap page of the relay. It opens the WebRTC tunnel to the pod (chosen by #pod=),
// proves the token INSIDE the tunnel (the channel-binding handshake: the token is bound to
// both DTLS fingerprints, so the relay never sees it and cannot swap the SDP), then hands
// the app to the pod: the Service Worker answers the page's fetches through the tunnel, and
// the JSON protocol goes through a MessagePort.
const params = new URLSearchParams(location.hash.slice(1) || location.search.slice(1))
const pod = params.get('pod')
const token = params.get('t') || params.get('token') || ''
const session = rand()

const fr = (navigator.languages?.[0] ?? navigator.language ?? 'en').startsWith('fr')
const words = {
  connecting: fr ? 'Connexion…' : 'Connecting…',
  nopod: fr ? 'Aucun pod indiqué. Ouvrez <relay>/?pod=<id>&t=<token>.' : 'No pod given. Open <relay>/?pod=<id>&t=<token>.',
  offline: fr ? 'Ce pod n’est pas en ligne sur le relay.' : 'This pod is not online at the relay.',
  connected: fr ? 'Connecté.' : 'Connected.',
  loading: fr ? 'Chargement de l’IDE…' : 'Loading the IDE…',
  update: fr ? 'Mettez ce pod à jour : les versions ne correspondent pas.' : 'Update this pod: the versions do not match.',
  failed: fr ? 'La connexion n’a pas pu être établie.' : 'The connection could not be established.',
  closed: fr ? 'Ce pod a fermé la connexion.' : 'This pod closed the connection.',
  sw: fr ? 'Service Worker : ' : 'Service Worker: ',
}
const status = (html) => { const el = document.getElementById('msg'); if (el) el.innerHTML = html }
const fail = (m) => status('<div class="err">' + m + '</div>')

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
  const sig = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(role + '|' + fpB + '|' + fpP + '|' + nonceB + '|' + nonceP))
  return [...new Uint8Array(sig)].map((x) => x.toString(16).padStart(2, '0')).join('')
}
const fingerprint = (desc) => {
  const m = desc?.sdp?.match(/a=fingerprint:sha-256 ([0-9a-fA-F:]+)/)
  return m ? m[1].replace(/:/g, '').toUpperCase() : ''
}

let pc, rpc
let handshakeDone = false
const buffers = new Map()
let appPort = null

function read(dc, e) {
  const r = unframe(e.data, buffers.get(dc))
  buffers.set(dc, r.buf)
  return r.msg
}

async function main() {
  if (!pod) return fail(words.nopod)
  status('<div class="spin"></div>' + words.connecting + ' <b>' + pod + '</b>')
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

  const sig = new WebSocket((location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/_relay/signal?pod=' + encodeURIComponent(pod) + '&session=' + session)
  sig.onmessage = (e) => {
    const m = JSON.parse(e.data)
    if (m.type === 'ice') start(m)
    else if (m.type === 'answer') pc.setRemoteDescription({ type: 'answer', sdp: m.sdp })
    else if (m.type === 'candidate') { try { pc.addIceCandidate(JSON.parse(m.candidate)) } catch {} }
    else if (m.type === 'bye') fail(words.closed)
  }
  sig.onerror = () => fail(words.offline)

  // Trickle ICE: the browser's candidates go to the relay, tagged with the session.
  const sendCandidate = (c) => {
    if (!c || !sig || sig.readyState !== WebSocket.OPEN) return
    sig.send(JSON.stringify({ type: 'candidate', session, candidate: c }))
  }

  async function start(ice) {
    pc = new RTCPeerConnection({ iceServers: [{ urls: ice.urls, username: ice.username, credential: ice.credential }] })
    window.tunnel = { pc, session, pod } // read by the e2e suite (getStats)
    rpc = pc.createDataChannel('rpc', { ordered: true })
    rpc.onmessage = async (e) => {
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
      if ((await mac('pod', fpB, fpP, nonceB, hp.nonce)) !== hp.proof) return fail(words.failed)
      send(rpc, { id: 0, method: '$auth', params: { proof: await mac('browser', fpB, fpP, nonceB, hp.nonce) } })
    }
    const nonceB = rand()
    rpc.onopen = () => send(rpc, { id: 0, method: '$hello', params: { protocol: 'web-ide-rpc', version: '1', nonce: nonceB } })
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

main()
