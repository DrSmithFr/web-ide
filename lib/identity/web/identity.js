// Identity of a device in the browser: the phrase, the keys derived from it, the
// certificates and the revocation lists.
//
// The pure part (formats, derivation, canonical bytes) is shared with lib/identity
// and checked against the test vectors by scripts/identity-vectors.mjs, so a signature made
// in the browser verifies in the pod and the other way round. The browser part (IndexedDB,
// passkey, pairing) is used by the bootstrap page and by the app, which import this same
// file — served by the relay and by the pod under /_relay/identity.js.
//
// Recent browsers only: WebCrypto Ed25519 and WebAuthn PRF are required, there is no
// fallback for older ones — support() names what is missing.
//
// The device key never leaves the crypto layer: its seed is stored wrapped by an AES-GCM key
// derived from the passkey (WebAuthn PRF) and unwrapped as a non-extractable key for the
// session.

export const InfoAccount = 'web-ide account v1'
export const InfoSync = 'web-ide sync v1'
export const InfoDevice = 'web-ide device v1'
export const InfoPasskey = 'web-ide passkey v1'
export const InfoX25519 = 'web-ide x25519 v1'
export const InfoWrap = 'web-ide sync wrap v1'
export const KindBrowser = 'browser'
export const KindPod = 'pod'
export const KindSettings = 'settings'
export const KindVault = 'vault'
export const ScopeAll = 'all'

const enc = new TextEncoder()

// base64url without padding, the form of the ids and the signatures.
export function b64u(b) {
  let s = ''
  for (const x of b) s += String.fromCharCode(x)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

export function b64d(s) {
  const t = s.replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(t + '='.repeat((4 - (t.length % 4)) % 4))
  const b = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) b[i] = bin.charCodeAt(i)
  return b
}

// An Ed25519 key from its 32-byte seed. WebCrypto refuses a raw seed (no usage is allowed
// for a private key) and a JWK without x, so the PKCS#8 DER is built by hand:
// SEQUENCE{version, AlgorithmIdentifier 1.3.101.112, OCTET STRING(32) seed}.
const PKCS8_PREFIX = [0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20]

export async function keyFromSeed(seed, extractable) {
  const der = new Uint8Array([...PKCS8_PREFIX, ...seed])
  return crypto.subtle.importKey('pkcs8', der, { name: 'Ed25519' }, extractable, ['sign'])
}

// The public half of a key, as the base64url of its raw bytes: the device id, or the account
// id. Only available while the key is extractable, which happens at pairing.
export async function publicFromSeed(seed) {
  const k = await keyFromSeed(seed, true)
  return (await crypto.subtle.exportKey('jwk', k)).x
}

export async function publicKey(id) {
  return crypto.subtle.importKey('raw', b64d(id), { name: 'Ed25519' }, false, ['verify'])
}

export async function sign(key, msg) {
  return b64u(new Uint8Array(await crypto.subtle.sign('Ed25519', key, enc.encode(msg))))
}

export async function verify(id, sig, msg) {
  try {
    return await crypto.subtle.verify('Ed25519', await publicKey(id), b64d(sig), enc.encode(msg))
  } catch {
    return false
  }
}

// The BIP39 seed of a phrase: PBKDF2-SHA256, salt "mnemonic", 2048 rounds, 64 bytes.
export async function bip39Seed(phrase) {
  const pb = await crypto.subtle.importKey('raw', enc.encode(phrase), 'PBKDF2', false, ['deriveBits'])
  return new Uint8Array(await crypto.subtle.deriveBits(
    { name: 'PBKDF2', salt: enc.encode('mnemonic'), iterations: 2048, hash: 'SHA-256' }, pb, 512))
}

// HKDF-SHA256 with the explicit 32-byte zero salt of the pod.
export async function hkdf(seed, info) {
  const hk = await crypto.subtle.importKey('raw', seed, 'HKDF', false, ['deriveBits'])
  return new Uint8Array(await crypto.subtle.deriveBits(
    { name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(32), info: enc.encode(info) }, hk, 256))
}

// --- the sync key ---

// The key of an epoch comes from the sync seed: HKDF with "web-ide sync epoch <e>", so a
// device that knows the phrase can always rebuild it and a rotation needs no key to lose.
// The sub-keys per kind come from the epoch key.
export function epochKey(syncSeed, epoch) { return hkdf(syncSeed, `web-ide sync epoch ${epoch}`) }
export function kindKey(key, kind) { return hkdf(key, kind) }
export function wrapKey(shared) { return hkdf(shared, InfoWrap) }

// X25519. WebCrypto cannot import a private key from a raw seed, so a device keeps its key as a
// JWK (sealed by the passkey PRF, like the device key); the pod derives its key from its device
// seed with crypto/ecdh. Both sides must agree on the shared secret, which the test vectors check
// by importing the pod's JWK here.
export async function x25519Generate() {
  const k = await crypto.subtle.generateKey({ name: 'X25519', namedCurve: 'X25519' }, true, ['deriveBits'])
  return { jwk: await crypto.subtle.exportKey('jwk', k.privateKey), key: b64u(new Uint8Array(await crypto.subtle.exportKey('raw', k.publicKey))) }
}

// The X25519 key of a device from its 32-byte seed. WebCrypto refuses a raw seed here too, so the
// PKCS#8 DER is built by hand with the X25519 algorithm id (1.3.101.110): the same seed gives the
// same key as the pod's crypto/ecdh, which the test vectors check.
const PKCS8_X25519_PREFIX = [0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x6e, 0x04, 0x22, 0x04, 0x20]

export async function x25519FromSeed(seed) {
  const der = new Uint8Array([...PKCS8_X25519_PREFIX, ...seed])
  const k = await crypto.subtle.importKey('pkcs8', der, { name: 'X25519', namedCurve: 'X25519' }, true, ['deriveBits'])
  const jwk = await crypto.subtle.exportKey('jwk', k)
  return { jwk, key: jwk.x }
}

export async function x25519Shared(jwk, peerKey) {
  const priv = await crypto.subtle.importKey('jwk', jwk, { name: 'X25519', namedCurve: 'X25519' }, false, ['deriveBits'])
  const peer = await crypto.subtle.importKey('jwk', { kty: 'OKP', crv: 'X25519', x: peerKey }, { name: 'X25519', namedCurve: 'X25519' }, false, [])
  return new Uint8Array(await crypto.subtle.deriveBits({ name: 'X25519', namedCurve: 'X25519', public: peer }, priv, 256))
}

// AES-GCM with the associated data the write used: account|kind|epoch|seq for a blob,
// account|device|epoch for a wrap. A blob cannot be moved to another account, kind, epoch or seq.
export function blobAAD(account, kind, epoch, seq) { return `${account}|${kind}|${epoch}|${seq}` }
export function wrapAAD(account, device, epoch) { return `${account}|${device}|${epoch}` }

export async function sealWith(key, aad, bytes) {
  const iv = crypto.getRandomValues(new Uint8Array(12))
  return { iv: b64u(iv), data: await sealWithNonce(key, b64u(iv), aad, bytes) }
}

// The ciphertext with a given nonce: the test vectors use a fixed one so both sides can check
// the same bytes.
export async function sealWithNonce(key, iv, aad, bytes) {
  const k = await crypto.subtle.importKey('raw', key, { name: 'AES-GCM' }, false, ['encrypt'])
  return b64u(new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: b64d(iv), additionalData: enc.encode(aad) }, k, bytes)))
}

export async function openWith(key, aad, blob) {
  const k = await crypto.subtle.importKey('raw', key, { name: 'AES-GCM' }, false, ['decrypt'])
  return new Uint8Array(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: b64d(blob.iv), additionalData: enc.encode(aad) }, k, b64d(blob.data)))
}

// The bytes each side signs for the sync (fixed field order, no spaces, like the certificates).
export function canonicalSyncKey(r) {
  return `{"account":${JSON.stringify(r.account)},"device":${JSON.stringify(r.device)},"key":${JSON.stringify(r.key)}}`
}

export function canonicalBlob(r) {
  return `{"account":${JSON.stringify(r.account)},"kind":${JSON.stringify(r.kind)},` +
    `"epoch":${r.epoch},"seq":${r.seq},"device":${JSON.stringify(r.device)},"op":${JSON.stringify(r.op)},` +
    `"nonce":${JSON.stringify(r.nonce)},"data":${JSON.stringify(r.data)}}`
}

export function canonicalWrap(w) {
  return `{"account":${JSON.stringify(w.account)},"device":${JSON.stringify(w.device)},` +
    `"epoch":${w.epoch},"key":${JSON.stringify(w.key)},"nonce":${JSON.stringify(w.nonce)},"data":${JSON.stringify(w.data)}}`
}

// The canonical bytes a signature covers: fixed field order, no spaces. JSON.stringify quotes
// the strings the way the pod's jstr does, so both sides build the same bytes.
export function canonicalCert(c) {
  return `{"v":${c.v},"account":${JSON.stringify(c.account)},"key":${JSON.stringify(c.key)},` +
    `"name":${JSON.stringify(c.name)},"kind":${JSON.stringify(c.kind)},"created":${c.created},` +
    `"scope":${JSON.stringify(c.scope)}}`
}

// The bytes an update request covers: the pod it names, the release tag to install, the moment
// the user asked. The pod checks the signature against the key of the certificate that travels
// with the request.
export function canonicalUpdate(r) {
  return `{"v":${r.v},"pod":${JSON.stringify(r.pod)},"version":${JSON.stringify(r.version)},"created":${r.created}}`
}

// The bytes a power request covers (relay.md, *Power*): the pod it is for, the action, the moment
// the user asked.
export function canonicalPower(r) {
  return `{"v":${r.v},"pod":${JSON.stringify(r.pod)},"action":${JSON.stringify(r.action)},"created":${r.created}}`
}

export function canonicalRevocations(r) {
  const revoked = [...r.revoked].sort()
  return `{"account":${JSON.stringify(r.account)},"seq":${r.seq},"epoch":${r.epoch},` +
    `"revoked":[${revoked.map((k) => JSON.stringify(k)).join(',')}],"created":${r.created}}`
}

// The bytes each side signs: role|fpBrowser|fpPod|nonceBrowser|noncePod. The order is always
// browser then pod, so both sides bind the same string.
export function binding(role, fpBrowser, fpPod, nonceBrowser, noncePod) {
  return role + '|' + fpBrowser + '|' + fpPod + '|' + nonceBrowser + '|' + noncePod
}

// The 4-word reading of a key id (44 bits of its SHA-256, 11 bits per word): what the user
// compares on both sides of a pairing, the guard against a relay that swaps a key.
export async function fingerprint(id, wordlist) {
  const h = new Uint8Array(await crypto.subtle.digest('SHA-256', b64d(id)))
  let v = 0n
  for (const x of h.subarray(0, 6)) v = (v << 8n) | BigInt(x)
  v >>= 4n
  const words = []
  for (let i = 3; i >= 0; i--) words.push(wordlist[Number((v >> BigInt(11 * i)) & 2047n)])
  return words.join(' ')
}

// BIP39 checksum: the words of the list, the right count, and the first bits of SHA-256 of
// the entropy. Returns "" when the phrase is good.
export async function validatePhrase(phrase, wordlist) {
  const words = phrase.split(/\s+/).filter(Boolean)
  if (![12, 15, 18, 21, 24].includes(words.length)) return 'a phrase is 12, 15, 18, 21 or 24 words'
  const index = new Map(wordlist.map((w, i) => [w, i]))
  let bits = ''
  for (const w of words) {
    const i = index.get(w)
    if (i === undefined) return `"${w}" is not a BIP39 word`
    bits += i.toString(2).padStart(11, '0')
  }
  const cbits = Math.floor(words.length * 11 / 32) // the integer division of the pod
  const entropy = new Uint8Array((bits.length - cbits) / 8)
  for (let i = 0; i < entropy.length; i++) entropy[i] = parseInt(bits.slice(i * 8, i * 8 + 8), 2)
  const h = new Uint8Array(await crypto.subtle.digest('SHA-256', entropy))
  if (h[0].toString(2).padStart(8, '0').slice(0, cbits) !== bits.slice(bits.length - cbits)) {
    return 'the checksum of the phrase does not match'
  }
  return ''
}

// A certificate is good when it is signed by the pinned account, covers the scope "all" and
// its device is not revoked. The browser checks the peer itself, as the pod does: the relay
// is only the courier.
export async function verifyCert(c, pinned, revoked) {
  if (!c) return 'no certificate'
  if (c.v !== 1) return `certificate version ${c.v}`
  if (c.account !== pinned) return 'certificate of another account'
  if (c.scope !== ScopeAll) return `scope ${c.scope}`
  if (!(await verify(c.account, c.sig, canonicalCert(c)))) return 'the certificate signature does not match'
  if (revoked?.revoked?.includes(c.key)) return 'device revoked' // wire text kept for older pages and pods: a revoked member
  return ''
}

// The signed revocation list an admin posts: the device keys it revoked and the epoch of the
// sync key rotated at the same time. Only a device holding the account key can sign one, so
// the relay can store it but never make one.
export async function signRevocations(accountKey, accountId, revoked, seq, epoch) {
  const r = { account: accountId, seq, epoch, revoked: [...revoked].sort(), created: Math.floor(Date.now() / 1000) }
  r.sig = await sign(accountKey, canonicalRevocations(r))
  return r
}

export async function verifyRevocations(r, pinned) {
  if (!r) return 'no revocation list'
  if (r.account !== pinned) return 'revocation list of another account'
  if (!(await verify(r.account, r.sig, canonicalRevocations(r)))) return 'the revocation list signature does not match'
  return ''
}

// What the browser needs, named when something is missing (no fallback for older browsers).
export async function support() {
  const missing = []
  if (!crypto?.subtle) missing.push('WebCrypto')
  else {
    try {
      await crypto.subtle.generateKey({ name: 'Ed25519' }, false, ['sign', 'verify'])
    } catch {
      missing.push('WebCrypto Ed25519')
    }
  }
  if (!navigator?.credentials || !window.PublicKeyCredential) missing.push('WebAuthn')
  return missing
}

// --- the record of a device (IndexedDB, per origin) ---

const DB = 'webide-identity'

export function openStore() {
  return new Promise((resolve, reject) => {
    const r = indexedDB.open(DB, 1)
    r.onupgradeneeded = () => r.result.createObjectStore('device')
    r.onsuccess = () => resolve(r.result)
    r.onerror = () => reject(r.error)
  })
}

export async function load(db, key) {
  return new Promise((resolve, reject) => {
    const q = db.transaction('device').objectStore('device').get(key)
    q.onsuccess = () => resolve(q.result)
    q.onerror = () => reject(q.error)
  })
}

export async function save(db, key, rec) {
  return new Promise((resolve, reject) => {
    const t = db.transaction('device', 'readwrite')
    t.objectStore('device').put(rec, key)
    t.oncomplete = () => resolve(rec)
    t.onerror = () => reject(t.error)
  })
}

// Every record of this origin. The salt must be known before the passkey is asked (it is the
// input of the PRF), so the day-to-day unlock reads the records first.
export async function list(db) {
  return new Promise((resolve, reject) => {
    const q = db.transaction('device').objectStore('device').getAll()
    q.onsuccess = () => resolve(q.result)
    q.onerror = () => reject(q.error)
  })
}

// The record of a device, stored under its device id (its public key, public information):
// the handle of the passkey, the 16 bytes given to the PRF, the id of the credential, the
// wrapped seeds and the certificate the account signed. The salt is stored with it because
// the same bytes must be given on create and on every get: a different salt would derive a
// different key and the seed could never be unwrapped. Only a device that knows the phrase
// (an admin) holds its account seed.
export function newRecord(id) {
  return { key: id, handle: id, salt: b64u(crypto.getRandomValues(new Uint8Array(16))) }
}

async function aesKey(prf, usages) {
  const bits = await hkdf(prf, InfoPasskey)
  return crypto.subtle.importKey('raw', bits, { name: 'AES-GCM' }, false, usages)
}

export async function seal(prf, bytes) {
  const key = await aesKey(prf, ['encrypt'])
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, bytes))
  return { iv: b64u(iv), data: b64u(ct) }
}

export async function open(prf, blob) {
  const key = await aesKey(prf, ['decrypt'])
  return new Uint8Array(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: b64d(blob.iv) }, key, b64d(blob.data)))
}

// --- passkey (WebAuthn PRF) ---

// One passkey per origin per device, the user handle being the account id (or the device id
// before an admin has approved it). The PRF output is the same on create and on get, so it
// derives the key that wraps the seeds; the record is found by the id of the credential.
// The PRF output is a byte array in Chromium and a base64url string in other engines: both
// readings give the same bytes.
function asBytes(v) {
  if (typeof v === 'string') return b64d(v)
  if (v instanceof Uint8Array) return v
  return new Uint8Array(v)
}

export async function passkey(rec, create) {
  const challenge = crypto.getRandomValues(new Uint8Array(16))
  const prf = { prf: { eval: { first: b64d(rec.salt) } } }
  const cred = create
    ? await navigator.credentials.create({ publicKey: {
        challenge,
        rp: { name: 'Web IDE', id: location.hostname },
        user: { id: enc.encode(rec.handle), name: rec.handle, displayName: rec.handle },
        // Only the PRF is used, never the signature of the passkey: any algorithm will do, and
        // Google Password Manager, iCloud Keychain and Windows Hello refuse Ed25519 alone.
        pubKeyCredParams: [-8, -7, -257].map((alg) => ({ type: 'public-key', alg })), // Ed25519, ES256, RS256
        authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
        extensions: prf,
      } })
    : await navigator.credentials.get({ publicKey: {
        challenge,
        rpId: location.hostname,
        userVerification: 'required',
        // Several devices of the same account share one origin: without the credential id the
        // browser would show a picker instead of unlocking this record.
        ...(rec.id ? { allowCredentials: [{ type: 'public-key', id: b64d(rec.id) }] } : {}),
        extensions: prf,
      } })
  // The extension outputs come from getClientExtensionResults(), not from cred.extensions.
  const out = cred.getClientExtensionResults?.()?.prf?.results?.first
  if (!out) throw new Error('the passkey did not give its PRF (WebAuthn PRF is required)')
  return { id: b64u(new Uint8Array(cred.rawId)), prf: asBytes(out) }
}

// The bytes a device signs to prove it holds the private half of the key its certificate names:
// the nonce the relay sent, tied to the origin the link was opened at. A copied certificate
// answers nothing.
export function relayBinding(origin, nonce) {
  return 'relay|' + origin + '|' + nonce
}

// prove answers the challenge of a link that registered with `prove: true`: the nonce it sent,
// signed with this device's key.
export async function prove(l, key, origin) {
  const c = await l.recv()
  if (!c || c.type !== 'challenge') return c // an error: the relay refused the link
  await l.send({ type: 'proof', nonce: c.nonce, sig: await sign(key, relayBinding(origin, c.nonce)) })
  return null
}

// The pairing and admin link: the same path on the relay origin (the bootstrap page) and on
// the pod origin (the app, which proxies it). Every message reaches the handler, and a message
// someone is waiting for resolves that wait too: a panel that asks for the device list and
// then for a code must not have its answer taken by the list.
//
// The language is given with the register message, so the relay answers its errors in it. The
// texts the module itself returns (a certificate that does not verify) are English keys: each
// page translates them with its own catalog.
// early {pod, session}: a tunnel to that pod opens with the link (#75) — its ICE servers come with
// the link, kept until the tunnel takes the session (adopt).
export function link(onPush, lang, early) {
  const q = early ? '?pod=' + encodeURIComponent(early.pod) + '&session=' + encodeURIComponent(early.session) : ''
  const ws = new WebSocket((location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/_relay/identity' + q)
  const waiters = []
  // The tunnels the page signals on this link (relay.md, #75): a message of a session goes to its
  // tunnel, never to a waiter of the link.
  const sessions = new Map()
  const held = []
  if (early) sessions.set(early.session, (m) => held.push(m))
  const handle = (session) => ({
    send: (m) => ws.send(JSON.stringify({ ...m, session })),
    open: () => ws.readyState === WebSocket.OPEN,
    close: () => {
      if (!sessions.delete(session)) return
      try {
        ws.send(JSON.stringify({ type: 'bye', session }))
      } catch {}
    },
  })
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data)
    if (m.session) {
      sessions.get(m.session)?.(m)
      return
    }
    if (onPush) onPush(m)
    const i = waiters.findIndex((w) => !w.type || w.type === m.type)
    if (i >= 0) waiters.splice(i, 1)[0].resolve(m)
  }
  return new Promise((resolve, reject) => {
    ws.onopen = () => resolve({
      send: (m) => ws.send(JSON.stringify(m)),
      // A wait with a type is resolved only by a message of that type: a panel that asked for
      // the device list must not have its answer taken by the list.
      recv: (type) => new Promise((r) => waiters.push({ type: type ?? '', resolve: r })),
      close: () => ws.close(),
      lang: lang ?? '',
      // The signaling of one tunnel to a pod on this link: its messages come to onMsg, and the
      // relay answers "signal" with the ICE servers of the session.
      signal: (session, pod, onMsg) => {
        sessions.set(session, onMsg)
        ws.send(JSON.stringify({ type: 'signal', pod, session }))
        return handle(session)
      },
      // The session opened with the link (early): its tunnel takes it, with what came already.
      adopt: (session, onMsg) => {
        sessions.set(session, onMsg)
        for (const m of held.splice(0)) onMsg(m)
        return handle(session)
      },
      isOpen: () => ws.readyState === WebSocket.OPEN,
    })
    ws.onerror = () => reject(new Error('the relay is not reachable'))
  })
}

// The BIP39 wordlist, the same 2048 words the pod embeds.
export async function wordlist() {
  const r = await fetch('/_relay/bip39.txt')
  if (!r.ok) throw new Error('the wordlist is not reachable')
  return (await r.text()).split('\n').filter(Boolean)
}

// The device of this origin, unlocked by its passkey: the record, its signing key for the
// session and the PRF that opened it. Null when this origin holds no device, or when the
// passkey was dismissed.
export async function unlock(db) {
  for (const r of await list(db)) {
    try {
      const p = await passkey(r, false)
      const seed = await open(p.prf, r.device)
      return { rec: r, key: await keyFromSeed(seed, false), prf: p.prf, seed }
    } catch {
      // Another device of this profile, or the passkey was dismissed.
    }
  }
  return null
}

// --- the unlocked session ---

// A device unlocked by its passkey stays unlocked an hour after its last use, so a reload or
// the installed app opening again does not ask the passkey. Only the signing key of the device
// is kept, as a non-extractable CryptoKey: never the seed, the PRF, the account key or the sync
// seed, so an admin action still asks the passkey. Its own database: the one of the devices
// keeps its version, which an older copy of this module (a pod of the LAN) must still open.
const SessionDB = 'webide-session'
export const SessionTTL = 60 * 60 * 1000

function sessions() {
  return new Promise((resolve, reject) => {
    const r = indexedDB.open(SessionDB, 1)
    r.onupgradeneeded = () => r.result.createObjectStore('session')
    r.onsuccess = () => resolve(r.result)
    r.onerror = () => reject(r.error)
  })
}

async function sessionOp(mode, fn) {
  const s = await sessions()
  try {
    return await new Promise((resolve, reject) => {
      const t = s.transaction('session', mode)
      const q = fn(t.objectStore('session'))
      t.oncomplete = () => resolve(q?.result)
      t.onerror = () => reject(t.error)
    })
  } finally {
    s.close()
  }
}

// keepUnlocked starts the session of a paired device (its key must be non-extractable).
export async function keepUnlocked(rec, key) {
  if (!rec?.cert || !key || key.extractable) return
  await sessionOp('readwrite', (st) => st.put({ device: rec.key, key, until: Date.now() + SessionTTL }, rec.key))
}

// resume returns the device of a session still running, { rec, key }, and moves its end an hour
// later; null when there is none. An expired session is deleted.
export async function resume(db) {
  let all = []
  try {
    all = (await sessionOp('readonly', (st) => st.getAll())) ?? []
  } catch {
    return null
  }
  const now = Date.now()
  for (const s of all) {
    if (s.until <= now) {
      await sessionOp('readwrite', (st) => st.delete(s.device))
      continue
    }
    const rec = await load(db, s.device)
    if (!rec?.cert) continue
    await sessionOp('readwrite', (st) => st.put({ ...s, until: now + SessionTTL }, s.device))
    return { rec, key: s.key }
  }
  return null
}

// touch moves the end of the running sessions an hour later: the page calls it while it is used.
export async function touch() {
  try {
    const now = Date.now()
    for (const s of (await sessionOp('readonly', (st) => st.getAll())) ?? []) {
      if (s.until > now) await sessionOp('readwrite', (st) => st.put({ ...s, until: now + SessionTTL }, s.device))
    }
  } catch {}
}

// lock ends every session of this origin: the next page asks the passkey again.
export async function lock() {
  try {
    await sessionOp('readwrite', (st) => st.clear())
  } catch {}
}

// The account key of an admin device, unwrapped for the session: the only key that can sign a
// certificate or a revocation list.
export async function accountKey(rec, prf) {
  if (!rec?.accountSeed) return null
  return keyFromSeed(await open(prf, rec.accountSeed), true)
}

// The sync seed of an admin device, unwrapped for the session: what lets it wrap the key of an
// epoch for a device that does not know the phrase.
export async function syncSeed(rec, prf) {
  if (!rec?.syncSeed) return null
  return open(prf, rec.syncSeed)
}

// The wrap of the key of an epoch for a device: X25519 with the key it published, HKDF, AES-GCM.
// Only a device holding the account key signs it, so the relay stores a wrap but cannot make one.
export async function wrapFor(accountKey, accountId, device, peerKey, seed, syncSeed, epoch) {
  const { jwk, key } = await x25519FromSeed(seed)
  const shared = await x25519Shared(jwk, peerKey)
  const wk = await wrapKey(shared)
  const ekey = await epochKey(syncSeed, epoch)
  const s = await sealWith(wk, wrapAAD(accountId, device, epoch), ekey)
  const w = { account: accountId, device, epoch, key, nonce: s.iv, data: s.data }
  w.sig = await sign(accountKey, canonicalWrap(w))
  return w
}

// A certificate signed by the account key for a device. The admin signs for the public key it
// read, never for a private key it was shown.
export async function signCert(accountKey, accountId, deviceId, name, kind) {
  const c = { v: 1, account: accountId, key: deviceId, name, kind, created: Math.floor(Date.now() / 1000), scope: ScopeAll }
  c.sig = await sign(accountKey, canonicalCert(c))
  return c
}

// Pairing with the phrase, on a computer: the phrase is used once here and never stored. Only
// the wrapped seeds, the account id and the certificate the account key signed are kept. A
// device that knows the phrase is an admin.
export async function enroll(db, phrase, wordlist, name, kind) {
  const err = await validatePhrase(phrase, wordlist)
  if (err) throw new Error(err)
  const seed = await bip39Seed(phrase)
  const accountSeed = await hkdf(seed, InfoAccount)
  const deviceSeed = await hkdf(seed, InfoDevice)
  const accountId = await publicFromSeed(accountSeed)
  const deviceId = await publicFromSeed(deviceSeed)
  const rec = newRecord(deviceId)
  const p = await passkey(rec, true)
  rec.id = p.id
  rec.account = accountId
  rec.device = await seal(p.prf, deviceSeed)
  rec.accountSeed = await seal(p.prf, accountSeed)
  rec.syncSeed = await seal(p.prf, await hkdf(seed, InfoSync))
  const key = await keyFromSeed(deviceSeed, false)
  const accountSigningKey = await keyFromSeed(accountSeed, true)
  rec.cert = await signCert(accountSigningKey, accountId, deviceId, name, kind)
  await save(db, deviceId, rec)
  return { rec, key, prf: p.prf, accountKey: accountSigningKey, seed: deviceSeed }
}
