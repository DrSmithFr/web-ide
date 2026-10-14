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

// What a load costs, phase by phase, and how much of the page it carried through the tunnel.
// A reload cannot keep the tunnel (the PeerConnection dies with the document), so these are the
// steps a reload pays again: window.bootTimings and window.httpChannels are read by the e2e suite
// (e2e/suites/relay.cjs), like window.tunnel and window.identity.
const bootAt = performance.now()
const timings = {}
const http = { channels: 0, bytes: 0 }
function publish() {
  window.bootTimings = timings
  window.httpChannels = http.channels
  window.httpBytes = http.bytes
}
// The first time a phase is reached: a later channel of another pod must not rename it.
function mark(name) {
  if (timings[name] === undefined) timings[name] = Math.round(performance.now() - bootAt)
  publish()
}

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
  welcome: fr ? 'Vos machines, depuis n’importe quel navigateur' : 'Your machines, from any browser',
  lead: fr ? 'Ouvrez l’IDE de vos pods d’ici, chiffré de bout en bout. Rien à installer sur cet appareil.' : 'Open the IDE of your pods from here, end-to-end encrypted. Nothing to install on this device.',
  nodevice: fr ? 'Aucun appareil n’est encore apparié dans ce navigateur.' : 'No device is paired in this browser yet.',
  restore: fr ? 'Restaurer avec votre phrase de récupération' : 'Restore with your recovery phrase',
  wordsOf: fr ? '{n} mots' : '{n} words',
  phraseOk: fr ? 'Phrase valide' : 'Valid phrase',
  pairThis: fr ? 'Apparier ce navigateur' : 'Pair this browser',
  pairing: fr ? 'Appariement…' : 'Pairing…',
  invite: fr ? 'J’ai un lien d’invitation' : 'I have an invite link',
  inviteHow: fr ? 'Sur un appareil déjà apparié, ouvrez Réglages › Accès distant › Appareils › Inviter un appareil, puis ouvrez le lien ou scannez le QR code avec cet appareil.' : 'On a paired device, open Settings › Remote access › Devices › Invite a device, then open the link or scan the QR code with this device.',
  newHere: fr ? 'Nouveau ici ?' : 'New here?',
  newHow: fr ? 'Installez le pod sur votre machine :' : 'Install the pod on your machine:',
  newCreate: fr ? 'puis créez votre compte, qui affiche votre phrase de récupération une seule fois :' : 'then create your account, which shows your recovery phrase once:',
  newBack: fr ? 'Revenez ici et restaurez avec cette phrase.' : 'Come back here and restore with that phrase.',
  docs: fr ? 'Documentation' : 'Documentation',
  locked: fr ? 'Appareil verrouillé' : 'Device locked',
  unlockWith: fr ? 'Déverrouiller avec votre clé d’accès' : 'Unlock with your passkey',
  unlockKeep: fr ? 'Il reste déverrouillé une heure après sa dernière utilisation.' : 'It stays unlocked for an hour after its last use.',
  usePhrase: fr ? 'Utiliser la phrase de récupération' : 'Use the recovery phrase instead',
  lock: fr ? 'Verrouiller' : 'Lock',
  retry: fr ? 'Réessayer' : 'Retry',
  newDevice: fr ? 'Nouvel appareil' : 'New device',
  approveOn: fr ? 'Approuvez-le sur un appareil administrateur : Réglages › Accès distant › Appareils.' : 'Approve it on an admin device: Settings › Remote access › Devices.',
  stepRelay: fr ? 'Relay' : 'Relay',
  stepPod: fr ? 'Pod' : 'Pod',
  stepSecure: fr ? 'Liaison chiffrée' : 'Secure handshake',
  account: fr ? 'Votre compte' : 'Your account',
  podsOff: fr ? 'Aucun pod en ligne' : 'No pod online',
  podsOffHow: fr ? 'Démarrez un pod de ce compte, ou vérifiez qu’il est relié à ce relay.' : 'Start a pod of this account, or check that it is linked to this relay.',
  oops: fr ? 'Connexion impossible' : 'Cannot connect',
  quota: fr ? 'Quota TURN mensuel atteint : plus de tunnel relayé, les chemins directs restent ouverts.' : 'Monthly TURN quota reached: no relayed tunnel, the direct paths stay open.',
  sponsor: fr ? 'le lever (Supporter)' : 'lift it (Supporter)',
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
  'the checksum of the phrase does not match': fr ? 'La somme de contrôle de la phrase ne correspond pas : un mot est faux.' : 'The checksum of the phrase does not match: a word is wrong.',
  'a phrase is 12, 15, 18, 21 or 24 words': fr ? 'Une phrase fait 12, 15, 18, 21 ou 24 mots.' : 'A phrase is 12, 15, 18, 21 or 24 words.',
  'the passkey did not give its PRF (WebAuthn PRF is required)': fr ? 'La clé d’accès n’a pas donné sa PRF (WebAuthn PRF est nécessaire).' : 'The passkey did not give its PRF (WebAuthn PRF is required).',
}
const lang = fr ? 'fr' : 'en'
const text = (key) => texts[key] ?? key
// The checks of a phrase, one of them naming the word that is not in the list.
const phraseError = (err) => {
  const m = /^"(.*)" is not a BIP39 word$/.exec(err)
  if (m) return fr ? `« ${m[1]} » n’est pas un mot BIP39` : err
  return text(err)
}
const fmt = (s, v) => s.replace(/\{(\w+)\}/g, (_, k) => String(v[k] ?? ''))

// --- the views of the page ---
//
// Every state of the page is a view drawn in the card (#msg). They are built from DOM nodes,
// never from HTML strings: the names of pods and the messages of a pod or of the relay are not
// ours to trust.
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag)
  for (const [k, v] of Object.entries(attrs ?? {})) {
    if (v == null || v === false) continue
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v)
    else if (k === 'class') el.className = v
    else el.setAttribute(k, v === true ? '' : v)
  }
  for (const c of kids.flat()) if (c != null && c !== false) el.append(c)
  return el
}
const svg = (d) => {
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  s.setAttribute('viewBox', '0 0 24 24')
  s.setAttribute('aria-hidden', 'true')
  const p = document.createElementNS('http://www.w3.org/2000/svg', 'path')
  p.setAttribute('d', d)
  s.append(p)
  return s
}
const ICON_LOCK = 'M6 11h12v9H6zM8.5 11V8a3.5 3.5 0 0 1 7 0v3'

let view = ''
function draw(name, ...nodes) {
  view = name
  const el = document.getElementById('msg')
  if (!el) return
  el.replaceChildren(...nodes.flat())
  drawFoot()
}
// The words of a fingerprint, each in its own box: easier to compare from one screen to another.
const fpWords = (fp, big) => h('div', { class: big ? 'fp big' : 'fp' }, (fp ?? '').split(' ').flatMap((w, i) => (i ? [' ', h('span', {}, w)] : [h('span', {}, w)])))

// The footer: the relay this page belongs to, and Lock while the device is unlocked.
function drawFoot() {
  const f = document.getElementById('foot')
  if (!f) return
  const unlocked = !!(key && rec?.cert)
  f.hidden = false
  f.replaceChildren(
    h('span', {}, location.host),
    unlocked ? h('button', { class: 'link', type: 'button', onclick: () => void lockNow() }, words.lock) : '',
  )
}

// The brand: antennae.space shows its name, a relay of one's own its host.
function drawBrand() {
  const host = location.hostname
  const own = /^[\d.]+$|^\[|^localhost$/.test(host) ? 'Web IDE' : host.split('.')[0]
  const b = document.getElementById('brand')
  if (b) b.textContent = own
  const hs = document.getElementById('host')
  const remote = fr ? 'accès distant' : 'remote access'
  if (hs) hs.textContent = own === 'Web IDE' ? remote : 'Web IDE · ' + remote
}

// The connection, in three steps: the relay, the pod, the secure handshake.
let stepAt = 0
let stepPod = ''
function connecting(step, name) {
  if (failed || view === 'error' || view === 'offline') return
  stepAt = step ?? stepAt
  if (name) stepPod = name
  const labels = [words.stepRelay, words.stepPod + (stepPod ? ' ' : ''), words.stepSecure]
  draw('connecting',
    h('h1', {}, words.connecting),
    h('ol', { class: 'steps' }, labels.map((l, i) =>
      h('li', { class: i < stepAt ? 'done' : i === stepAt ? 'now' : '' },
        h('span', { class: 'dot' }), h('span', {}, l, i === 1 && stepPod ? h('b', {}, stepPod) : '')))),
  )
}
const busy = (line) => draw('busy', h('div', { class: 'spin' }), h('p', { class: 'center muted' }, line))
// A page of the window that lost its channel: said in the card, without ending the window.
const notice = (m) => draw('error', h('div', { class: 'err' }, m))

// The monthly TURN quota of the hosted relay is reached: the relay gave no TURN credential, so
// the tunnel can only take a direct path. The page says it and points at what lifts the quota,
// it does not stop the attempt — most tunnels do not need TURN.
function quotaNote(q) {
  const el = document.getElementById('msg')
  if (!el || el.querySelector('.quota')) return
  const size = (n) => {
    const u = ['o', 'Ko', 'Mo', 'Go']
    let v = n, i = 0
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
    return (i ? v.toFixed(1) : String(v)) + ' ' + (i ? u[i] : 'o')
  }
  el.append(h('p', { class: 'quota muted' },
    words.quota, ' ', size(q.used), ' / ', size(q.limit), ' ',
    h('a', { href: 'https://github.com/sponsors/DrSmithFr', target: '_blank', rel: 'noreferrer' }, words.sponsor)))
}

let failed = false
let lastError = null
// An error: what happened, and what to do about it. A revoked device ends its session and is
// offered the phrase; anything else can be retried.
function fail(m) {
  failed = true
  lastError = m
  const revokedNow = m === text('revoked') || m === text('device revoked')
  if (revokedNow) void id.lock()
  draw('error',
    h('h1', {}, words.oops),
    h('div', { class: 'err' }, m),
    h('div', { class: 'actions' },
      h('button', { class: 'btn primary', type: 'button', onclick: () => location.reload() }, words.retry),
      revokedNow ? h('button', { class: 'btn', type: 'button', onclick: () => { failed = false; showPhraseForm() } }, words.usePhrase) : ''),
  )
}

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
let swReady = null // the Service Worker of this origin, started at once and awaited by openApp
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
  await firstPods(l)
  if (!pods.length) {
    l.send({ type: 'devices' })
    const d = await l.recv('devices')
    const names = (d.devices ?? []).filter((x) => x.cert?.kind === id.KindPod).map((x) => x.cert.name)
    if (!names.length) return fail(words.nopod)
    failed = true
    lastError = words.nopodonline
    draw('offline',
      h('h1', {}, words.podsOff),
      h('div', { class: 'err' }, words.nopodonline, names.join(', ')),
      h('p', { class: 'muted small' }, words.podsOffHow),
      h('div', { class: 'actions' }, h('button', { class: 'btn primary', type: 'button', onclick: () => location.reload() }, words.retry)))
    return lastError
  }
  const chosen = enterGateway()
  rememberSite(chosen.site)
  pod = chosen.podId
  remember(pod)
  // The Service Worker answers the fetches of the app by finding the bootstrap page by its
  // "pod=": the pod picked here goes in the hash (a hash does not reload the page).
  location.hash = 'pod=' + pod
  connecting(1, chosen.name || chosen.podId)
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
  const pairing = (waiting) => draw('pairing',
    h('h1', {}, words.newDevice),
    h('p', { class: 'muted' }, words.compare),
    fpWords(rec.fp, true),
    waiting ? h('div', { class: 'notice' }, h('div', { class: 'spin' })) : '',
    h('p', { class: 'muted small center' }, waiting ? words.waiting : words.approveOn),
    waiting ? h('p', { class: 'muted small center' }, words.approveOn) : '')
  pairing(false)
  const l = await id.link(null, lang)
  l.send({ type: 'register', account: '', name: deviceName(), lang })
  const ready = await l.recv()
  if (ready.type === 'error') return fail(text(ready.message))
  l.send({ type: 'pairAttach', code, key: deviceId, name: deviceName(), kind: id.KindBrowser })
  const p = await l.recv()
  if (p.type === 'error') return fail(text(p.message))
  pairing(true)
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
  await id.keepUnlocked(rec, key)
  busy(words.paired)
  return ''
}

// The welcome view of an origin with no device: what this page is, the recovery phrase to pair
// this browser, and the two other ways in (an invite link, a first pod).
function showPhraseForm() {
  const input = h('textarea', {
    id: 'phrase', rows: '4', autocomplete: 'off', autocapitalize: 'none', spellcheck: 'false',
    placeholder: words.phrase, 'aria-describedby': 'phrase-check',
  })
  const count = h('span', {}, fmt(words.wordsOf, { n: 0 }))
  const verdict = h('span', {})
  const go = h('button', { class: 'btn primary block', type: 'submit' }, words.pairThis)
  const form = h('form', { id: 'pair' },
    h('label', { for: 'phrase' }, words.restore),
    input,
    h('div', { class: 'check', id: 'phrase-check' }, count, verdict),
    go)
  // The words are counted, and the BIP39 checksum checked, as they are typed.
  let wl = null
  const check = async () => {
    const phrase = input.value.trim().toLowerCase().split(/\s+/).filter(Boolean).join(' ')
    const n = phrase ? phrase.split(' ').length : 0
    count.textContent = fmt(words.wordsOf, { n })
    verdict.className = ''
    verdict.textContent = ''
    if (![12, 15, 18, 21, 24].includes(n)) return
    wl = wl ?? (await id.wordlist())
    const err = await id.validatePhrase(phrase, wl)
    verdict.className = err ? 'bad' : 'good'
    verdict.textContent = err ? phraseError(err) : words.phraseOk
  }
  input.addEventListener('input', () => void check())
  // Enter pairs, Shift+Enter is a new line.
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      form.requestSubmit()
    }
  })
  // The form must not submit: that would reload the page and lose the tunnel.
  form.addEventListener('submit', async (e) => {
    e.preventDefault()
    if (!input.value.trim()) return
    go.disabled = true
    go.textContent = words.pairing
    try {
      const d = await id.enroll(db, input.value.trim(), await id.wordlist(), deviceName(), id.KindBrowser)
      rec = d.rec
      rec.fp = await id.fingerprint(rec.key, await id.wordlist())
      key = d.key
      account = rec.account
      show()
      await id.keepUnlocked(rec, key)
      connecting(0)
      await enter('cert')
    } catch (err) {
      fail(text(err.message))
    }
  })
  const install = 'curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh'
  draw('welcome',
    h('h1', {}, words.welcome),
    h('p', { class: 'lead' }, words.lead),
    h('p', { class: 'muted small' }, words.nodevice),
    form,
    h('div', { class: 'ways' },
      h('details', {}, h('summary', {}, words.invite), h('div', { class: 'body' }, words.inviteHow)),
      h('details', {}, h('summary', {}, words.newHere), h('div', { class: 'body' },
        words.newHow, h('code', { class: 'code' }, install),
        words.newCreate, h('code', { class: 'code' }, 'web-ide-pod pair create'),
        words.newBack, ' ',
        h('a', { href: 'https://github.com/DrSmithFr/web-ide', target: '_blank', rel: 'noopener' }, words.docs)))),
  )
}

// The unlock view: a device is paired here but its passkey was not given (dismissed, or a browser
// that wants a click before it shows the prompt). Resolves with the unlocked device, or null when
// the user goes for the phrase.
function showUnlock(recs) {
  return new Promise((resolve) => {
    const r0 = recs[0]
    const fp = h('div', {})
    void id.wordlist().then((wl) => id.fingerprint(r0.key, wl)).then((f) => fp.replaceChildren(fpWords(f)), () => {})
    const go = h('button', { class: 'btn primary block', type: 'button' }, words.unlockWith)
    go.addEventListener('click', async () => {
      go.disabled = true
      const d = await id.unlock(db).catch(() => null)
      go.disabled = false
      if (d) resolve(d)
    })
    draw('unlock',
      h('h1', {}, words.locked),
      h('div', { class: 'device' },
        h('div', { class: 'icon' }, svg(ICON_LOCK)),
        h('div', {}, h('b', {}, r0.cert?.name || deviceName()), h('div', { class: 'muted small' }, words.account))),
      fp,
      go,
      h('p', { class: 'muted small center' }, words.unlockKeep),
      h('p', { class: 'center small' }, h('button', { class: 'link', type: 'button', onclick: () => resolve(null) }, words.usePhrase)))
    go.focus()
  })
}

// Lock: the session of this device ends now, the next page asks the passkey.
async function lockNow() {
  await id.lock()
  location.reload()
}

// The link of the account: the proof of possession, the revocation list, the pods the relay
// reports, and — when the link named no pod — the gateway this browser enters through.
async function enter(mode) {
  entryMode = mode
  const l = await id.link(onPush, lang)
  link = l
  mark('link')
  if (account) {
    const err = await applyRevocations(l)
    if (err) return err
    mark('revocations')
    if (!pod) {
      const err = await enterSite(l)
      if (err) return err
    } else {
      await firstPods(l)
    }
  }
  mark('pods')
  if (!pod) return fail(words.nopod)
  const p = pods.find((p) => p.podId === pod)
  entryKey = p?.key ?? ''
  entryPod = pod
  entrySite = p?.site ?? ''
  shownKey = entryKey
  connecting(1, p?.name || pod)
  entry = await connect(entryKey, pod, mode)
  openApp()
  return ''
}

// The first pods list of the link. It can arrive while the page still writes the revocation list
// to IndexedDB, before anyone waits for it: onPush has kept it then, and waiting for another one
// would hang the page.
let podsSeen = false
async function firstPods(l) {
  if (!podsSeen) await l.recv('pods')
}

// What the identity link pushes: the revocation list, and the pods of the account as the relay
// sees them (a gateway that left or came back changes that list).
function onPush(m) {
  if (m.type === 'revocations' && m.list) revoked = m.list
  if (m.type === 'pods') {
    podsSeen = true
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

// The Service Worker is what answers the fetches of the app, so the page cannot open before it
// is active. Its activation is the slow part of a first visit (Chromium starts one per browser
// profile: ~2 s there, a millisecond on the next loads) — so it is started here and waited for
// only when the app is about to open: it overlaps the device and the tunnel instead of adding to
// them (see window.bootTimings, e2e/suites/relay.cjs).
async function startSW() {
  try {
    const reg = await navigator.serviceWorker.register('/sw.js', { scope: '/' })
    mark('swreg')
    await reg.ready
    if (!reg.active) await reg.update()
    await reg.ready
    mark('sw')
    return true
  } catch (e) {
    fail(words.sw + e.message)
    return false
  }
}

async function main() {
  drawBrand()
  const missing = await id.support()
  if (missing.length) return fail(words.needs + missing.join(', '))
  mark('support')
  swReady = startSW()

  db = await id.openStore()
  const wl = await id.wordlist()
  mark('words')

  // An invite link carries the pod its author was on: the joining device pairs first, then
  // opens that pod. Without a pod it stops at the pairing.
  if (join) {
    const err = await joinFlow(join, wl)
    if (err) return
    if (!pod) return fail(words.nopod)
  } else {
    connecting(0, pod ?? '')
    // A session still running opens without the passkey; else the passkey, asked at once, and
    // when it is not given, the unlock view and its button.
    let d = await id.resume(db)
    if (!d) {
      const recs = await id.list(db)
      if (recs.length) {
        d = await id.unlock(db)
        if (!d && !token) d = await showUnlock(recs)
        if (d) await id.keepUnlocked(d.rec, d.key)
        if (!d && !token) {
          showPhraseForm()
          return
        }
        connecting(0)
      }
    }
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
  mark('device')
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
  sig.onopen = () => mark('signal')
  sig.onmessage = (e) => {
    const m = JSON.parse(e.data)
    if (m.type === 'ice') {
      mark('ice')
      if (m.quota?.over) quotaNote(m.quota)
      start(t, m).catch((err) => drop(t, err.message))
    } else if (m.type === 'answer') {
      mark('answer')
      t.pc?.setRemoteDescription({ type: 'answer', sdp: m.sdp }).catch(() => {})
    } else if (m.type === 'candidate') { try { t.pc?.addIceCandidate(JSON.parse(m.candidate)) } catch {} }
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
  // Over the monthly quota the relay sent no credential: keep the STUN servers and drop the TURN
  // ones, which a client cannot use without one (relay.md, Hosted service).
  const urls = ice.quota?.over ? (ice.urls ?? []).filter((u) => u.startsWith('stun:')) : ice.urls
  const pc = new RTCPeerConnection({ iceServers: [{ urls, username: ice.username, credential: ice.credential }] })
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
  mark('dc')
  if (t.key === entryKey && dc === t.rpc) connecting(2)
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
  if (ready.method === '$ready') mark('ready')
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
  else if (shownKey === t.key) notice(msg)
}

// The gateway left: ask the relay who carries the site now and reconnect through it, with a
// waiting time that grows. The pages of the pods reached directly are not touched.
async function failover() {
  if (failed) return
  busy(words.reconnecting.trim())
  for (let i = 0; i < 6; i++) {
    await refreshPods()
    // The relay has not elected one yet: the pod this window was on is worth another try.
    const g = entryGateway()
    if (g) {
      const old = entryKey
      entryKey = g.key
      entryPod = g.podId
      rememberSite(g.site)
      busy(words.reconnecting + (g.name || g.podId))
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
async function openApp() {
  busy(words.loading)
  // The fetches of the app go through the Service Worker: opening the frame before it is active
  // would answer them from the relay, which does not know them.
  if (!(await swReady)) return
  mark('app')
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
// shows, which pods it can reach, and which version that pod runs — the Service Worker keeps the
// assets of a pod under that version and drops them when the pod is updated (#71).
function declare() {
  try {
    const at = pods.find((x) => x.key === shownKey) ?? pods.find((x) => x.key === entryKey)
    navigator.serviceWorker?.controller?.postMessage({
      kind: 'own', pod: entryPod, key: shownKey, keys: [...tunnels.keys(), shownKey], version: at?.version ?? '',
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
    if (shownKey === key) notice(words.closed)
  }
  return e
}

// One fetch of the page, answered by the pod on an http:<n> channel: the head as JSON,
// then the body as raw chunks, closed by an empty one.
function httpFetch(t, d, prefix) {
  const dc = t.pc.createDataChannel((prefix ?? '') + d.id, { ordered: true })
  held(t, true)
  http.channels++
  publish()
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
      http.bytes += m.length
      d.port.postMessage(m)
    }
    d.port.postMessage(null)
    dc.close()
    held(t, false)
    publish()
  })()
}

function send(dc, obj) { frame(dc, new TextEncoder().encode(JSON.stringify(obj))) }

// The session of the device lasts an hour after its last use: a page left open and looked at
// keeps it going.
setInterval(() => {
  if (key && document.visibilityState === 'visible') void id.touch()
}, 5 * 60 * 1000)
document.addEventListener('visibilitychange', () => {
  if (key && document.visibilityState === 'visible') void id.touch()
})

// A rejected flow (a passkey dismissed, the wordlist unreachable) must not leave the page
// stuck on the spinner: the message names what failed.
main().catch((e) => fail(e.message))
