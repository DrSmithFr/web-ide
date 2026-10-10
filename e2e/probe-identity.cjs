// Throwaway probe (not a suite): what the browser can do for the identity work —
// WebCrypto Ed25519, HKDF, PBKDF2, and the PRF of a virtual authenticator.
const { chromium } = require('playwright-core')

const b64u = (b) => Buffer.from(b).toString('base64url')

const args = {
  jwkPrivate: { kty: 'OKP', crv: 'Ed25519', d: b64u(Buffer.from('a2762b46d03642fbd86da31d9d9d68c1d6b6718009dd44367f155bcac8c016cf', 'hex')), x: 'S6ZXtI0OcDa4t1QXvRf_DWwpA2DJKuRqdJKGe8UveQs' },
  jwkPublic: { kty: 'OKP', crv: 'Ed25519', x: 'zTFqKvpAtSeuHqtYNDGohuqw5giUVa4Zx0uHH0wYrFY' },
  sigHex: '5d8577a9f9159643ff51ce39f089edaeeb72a043f3a828df0663b7e3f612a8bc5e023a847e31f541bfd7c960a178803fa31e1fe2328a4c0639d99c77572ea60a',
  seedHex: 'bef3db7af09861839c336e33be87c753babe9fd4a4d0fb6dc7475ac864a8d0f76aaadacbc47d9396407b5b3a85a8bebdba95b7c349098223cb6253ab93c16357',
  phrase: 'lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper',
}

const race = (p, ms, label) => Promise.race([p, new Promise((r) => setTimeout(() => r('TIMEOUT ' + label), ms))])

async function main() {
  const b = await chromium.launch({ headless: true })
  const p = await b.newPage()
  await p.goto('http://127.0.0.1:4433/', { waitUntil: 'commit' }).catch(() => {})
  const cdp = await p.context().newCDPSession(p)

  for (const v of [
    { enable: true },
    { enable: true, protocol: 'ctap2' },
    { enable: true, protocol: 'ctap2', hasPrf: true },
    { enable: true, protocol: 'ctap2', automaticApprovalTimeoutMs: 1000, useSystemStoredCredentials: false, useSystemSignedPublicKeys: false },
  ]) {
    console.log('CDP', JSON.stringify(v), '->', await race(cdp.send('WebAuthn.addVirtualAuthenticator', v), 8000, 'cdp'))
  }

  const r = await race(p.evaluate(async (a) => {
    const c = globalThis.crypto
    const hexToB = (h) => new Uint8Array(h.match(/../g).map((x) => parseInt(x, 16)))
    const hex = (b) => [...new Uint8Array(b)].map((x) => x.toString(16).padStart(2, '0')).join('')
    const out = { secure: window.isSecureContext }
    const msg = new TextEncoder().encode('{"v":1,"account":"zTFqKvpAtSeuHqtYNDGohuqw5giUVa4Zx0uHH0wYrFY","key":"S6ZXtI0OcDa4t1QXvRf_DWwpA2DJKuRqdJKGe8UveQs","name":"test pod","kind":"pod","created":1766721600,"scope":"all"}')
    const sig = hexToB(a.sigHex)
    try {
      const k = await c.subtle.importKey('jwk', a.jwkPrivate, 'Ed25519', false, ['sign'])
      out.jwkPrivateSign = hex(await c.subtle.sign('Ed25519', k, msg)) === a.sigHex
    } catch (e) { out.jwkPrivateSign = 'FAIL ' + e.message }
    try {
      const k = await c.subtle.importKey('jwk', a.jwkPublic, 'Ed25519', false, ['verify'])
      out.jwkPublicVerify = await c.subtle.verify('Ed25519', k, sig, msg)
    } catch (e) { out.jwkPublicVerify = 'FAIL ' + e.message }
    try {
      const pair = await c.subtle.generateKey('Ed25519', true, ['sign', 'verify'])
      out.generateKey = Object.keys(await c.subtle.exportKey('jwk', pair.privateKey))
    } catch (e) { out.generateKey = 'FAIL ' + e.message }
    // HKDF with an explicit 32-byte zero salt (the convention of the Go package).
    try {
      const hk = await c.subtle.importKey('raw', hexToB(a.seedHex), { name: 'HKDF' }, false, ['deriveBits'])
      out.hkdfZeroSalt = hex(await c.subtle.deriveBits({ name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(32), info: new TextEncoder().encode('web-ide sync v1'), length: 256 }, hk, 256))
      out.hkdfEmptySalt = hex(await c.subtle.deriveBits({ name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(0), info: new TextEncoder().encode('web-ide sync v1'), length: 256 }, hk, 256))
    } catch (e) { out.hkdf = 'FAIL ' + e.message }
    try {
      const pb = await c.subtle.importKey('raw', new TextEncoder().encode(a.phrase), { name: 'PBKDF2' }, false, ['deriveBits'])
      out.pbkdf2 = hex(await c.subtle.deriveBits({ name: 'PBKDF2', salt: new TextEncoder().encode('mnemonic'), iterations: 2048, hash: 'SHA-256' }, pb, 512))
    } catch (e) { out.pbkdf2 = 'FAIL ' + e.message }
    return out
  }, args), 20000, 'evaluate')
  console.log(JSON.stringify(r, null, 1))

  const prf = await race(p.evaluate(async () => {
    const out = { origin: location.origin }
    const rp = { name: 'test', id: location.hostname }
    try {
      const cred = await navigator.credentials.create({
        publicKey: { challenge: new Uint8Array(16), rp, user: { id: new Uint8Array(16), name: 'u', displayName: 'u' }, pubKeyCredParams: [{ type: 'public-key', alg: -8 }], authenticatorSelection: { userVerification: 'required' } },
      })
      out.created = !!cred && cred.id.length > 0
    } catch (e) { out.create = 'FAIL ' + e.message }
    try {
      const got = await navigator.credentials.get({
        publicKey: { challenge: new Uint8Array(16), rp, userVerification: 'required', extensions: { prf: { eval: { first: new Uint8Array(8) } } } },
      })
      out.prf = got && got.extensions && got.extensions.prf ? Object.keys(got.extensions.prf) : 'none'
    } catch (e) { out.prf = 'FAIL ' + e.message }
    return out
  }), 20000, 'prf')
  console.log(JSON.stringify(prf, null, 1))
  await b.close()
}

main().catch((e) => console.log('ERR', e.message))
