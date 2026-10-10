// Identity of a device: the phrase, the keys derived from it, the certificates and the
// revocation lists. Pure functions, no DOM and no storage: the same code runs in the
// bootstrap page, in the app and in the vector check (node scripts/identity-vectors.mjs), so
// the pod and the browser agree on every byte a signature covers.
//
// The conventions shared with pod/internal/identity: base64url without padding, HKDF-SHA256
// with an explicit 32-byte zero salt (WebCrypto uses zeros of the hash size), canonical JSON
// with a fixed field order and no spaces.

export const InfoAccount = 'web-ide account v1'
export const InfoSync = 'web-ide sync v1'
export const InfoDevice = 'web-ide device v1'
export const KindBrowser = 'browser'
export const KindPod = 'pod'
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

// An Ed25519 key from its 32-byte seed. WebCrypto refuses a raw seed (no usage allowed for
// a private key), so the PKCS#8 DER is built by hand: SEQUENCE{version, AlgorithmIdentifier
// 1.3.101.112, OCTET STRING(32) seed}. The key is non-extractable when asked for the
// session, so the device key never leaves the crypto layer.
const PKCS8_PREFIX = [0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20]

export async function keyFromSeed(seed, extractable) {
  const der = new Uint8Array([...PKCS8_PREFIX, ...seed])
  return crypto.subtle.importKey('pkcs8', der, { name: 'Ed25519' }, extractable, ['sign'])
}

// The public half, as the base64url of its raw bytes: the device id, or the account id.
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

// The canonical bytes a signature covers: fixed field order, no spaces. JSON.stringify quotes
// the strings the way the pod's jstr does (only the quote, the backslash and the control
// characters), so both sides build the same bytes.
export function canonicalCert(c) {
  return `{"v":${c.v},"account":${JSON.stringify(c.account)},"key":${JSON.stringify(c.key)},` +
    `"name":${JSON.stringify(c.name)},"kind":${JSON.stringify(c.kind)},"created":${c.created},` +
    `"scope":${JSON.stringify(c.scope)}}`
}

export function canonicalRevocations(r) {
  const revoked = [...r.revoked].sort()
  return `{"account":${JSON.stringify(r.account)},"seq":${r.seq},"epoch":${r.epoch},` +
    `"revoked":[${revoked.map((k) => JSON.stringify(k)).join(',')}],"created":${r.created}}`
}

// The 4-word reading of a key id (44 bits of its SHA-256, 11 bits per word): what the user
// compares on both sides of a pairing, the guard against a relay that swaps a key.
export async function fingerprint(id, wordlist) {
  const b = b64d(id)
  const h = new Uint8Array(await crypto.subtle.digest('SHA-256', b))
  let v = 0n
  for (const x of h.subarray(0, 6)) v = (v << 8n) | BigInt(x)
  v >>= 4n
  const words = []
  for (let i = 3; i >= 0; i--) words.push(wordlist[Number((v >> BigInt(11 * i)) & 2047n)])
  return words.join(' ')
}

// BIP39 checksum: the words of the list, the right count, and the first bits of SHA-256 of
// the entropy.
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
