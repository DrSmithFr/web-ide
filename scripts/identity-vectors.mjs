// Checks the shared test vectors (lib/identity/vectors.json) with the browser code
// (lib/identity/web/identity.js): the same phrase must give the same seed, the same
// ids, the same canonical bytes and the same signatures in Go and in the browser. Run by
// make test; the e2e identity suite runs the same module in Chromium.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import * as identity from '../lib/identity/web/identity.js'

const root = fileURLToPath(new URL('../', import.meta.url))
const v = JSON.parse(readFileSync(root + 'lib/identity/vectors.json', 'utf8'))
const wordlist = readFileSync(root + 'lib/identity/wordlist.txt', 'utf8').split('\n').filter(Boolean)

const hex = (b) => Buffer.from(b).toString('hex')
let failed = 0
const check = (name, got, want) => {
  if (got !== want) { console.error(`identity vectors: ${name}\n got  ${got}\n want ${want}`); failed++ }
  else console.log(`identity vectors: ${name} OK`)
}

async function main() {
  if (wordlist.length !== 2048) { console.error('identity vectors: the wordlist must hold 2048 words'); failed++ }
  if (await identity.validatePhrase(v.phrase, wordlist)) { console.error('identity vectors: the phrase must validate'); failed++ }

  const seed = await identity.bip39Seed(v.phrase)
  check('seed', hex(seed), v.seed)
  const accountSeed = await identity.hkdf(seed, identity.InfoAccount)
  const syncSeed = await identity.hkdf(seed, identity.InfoSync)
  const deviceSeed = await identity.hkdf(seed, identity.InfoDevice)
  check('accountSeed', hex(accountSeed), v.accountSeed)
  check('syncSeed', hex(syncSeed), v.syncSeed)
  check('deviceSeed', hex(deviceSeed), v.deviceSeed)

  // The public half of a key made from a seed: WebCrypto gives it through the JWK form.
  const accountKey = await identity.keyFromSeed(accountSeed, true)
  const deviceKey = await identity.keyFromSeed(deviceSeed, true)
  check('accountId', (await crypto.subtle.exportKey('jwk', accountKey)).x, v.accountId)
  check('deviceId', (await crypto.subtle.exportKey('jwk', deviceKey)).x, v.deviceId)

  check('messageSig', await identity.sign(accountKey, v.message), v.messageSig)
  check('deviceMessageSig', await identity.sign(deviceKey, v.message), v.deviceMessageSig)

  const created = Number(v.certCanonical.match(/"created":(\d+)/)[1])
  const cert = { v: 1, account: v.accountId, key: v.deviceId, name: 'test pod', kind: identity.KindPod, created, scope: identity.ScopeAll }
  check('certCanonical', identity.canonicalCert(cert), v.certCanonical)
  check('certSig', await identity.sign(accountKey, identity.canonicalCert(cert)), v.certSig)

  const rcreated = Number(v.revocationCanonical.match(/"created":(\d+)/)[1])
  const rev = { account: v.accountId, seq: 2, epoch: 1, revoked: [v.deviceId], created: rcreated }
  check('revocationCanonical', identity.canonicalRevocations(rev), v.revocationCanonical)
  check('revSig', await identity.sign(accountKey, identity.canonicalRevocations(rev)), v.revocationSig)

  check('accountFingerprint', await identity.fingerprint(v.accountId, wordlist), v.accountFingerprint)
  check('deviceFingerprint', await identity.fingerprint(v.deviceId, wordlist), v.deviceFingerprint)

  // Sync (milestone 4): the key of an epoch, its sub-keys, the X25519 pair of a device and the
  // wrap of the epoch key for it. WebCrypto cannot make an X25519 key from a raw seed, so the
  // pod's key is imported here as the JWK the browser would have generated itself: the same seed
  // must give the same shared secret as Go's crypto/ecdh.
  const xseed = await identity.hkdf(deviceSeed, identity.InfoX25519)
  check('podXSeed', hex(xseed), v.podXSeed)
  const podJwk = { kty: 'OKP', crv: 'X25519', d: identity.b64u(xseed), x: v.podXPub }
  const shared = await identity.x25519Shared(podJwk, v.adminXPub)
  check('shared', hex(shared), v.shared)
  const wk = await identity.wrapKey(shared)
  check('wrapKey', hex(wk), v.wrapKey)
  check('wrapAAD', identity.wrapAAD(v.accountId, v.deviceId, 1), v.wrapAAD)

  const ekey1 = await identity.epochKey(syncSeed, 1)
  check('epochKey1', hex(ekey1), v.epochKey1)
  check('wrapData', await identity.sealWithNonce(wk, v.wrapNonce, v.wrapAAD, ekey1), v.wrapData)
  if (hex(await identity.openWith(wk, v.wrapAAD, { iv: v.wrapNonce, data: v.wrapData })) !== v.epochKey1) {
    console.error('identity vectors: the wrap must open to the epoch key')
    failed++
  }
  check('settingsKey', hex(await identity.kindKey(ekey1, identity.KindSettings)), v.settingsKey)
  check('vaultKey', hex(await identity.kindKey(ekey1, identity.KindVault)), v.vaultKey)
  check('blobAAD', identity.blobAAD(v.accountId, identity.KindSettings, 1, 1), v.blobAAD)
  check('blobData', await identity.sealWithNonce(await identity.kindKey(ekey1, identity.KindSettings), v.blobNonce, v.blobAAD, new TextEncoder().encode('{"theme":"nuit"}')), v.blobData)
  // A blob cannot be moved to another account, kind, epoch or seq.
  try {
    await identity.openWith(await identity.kindKey(ekey1, identity.KindSettings), identity.blobAAD(v.accountId, identity.KindVault, 1, 1), { iv: v.blobNonce, data: v.blobData })
    console.error('identity vectors: a blob must not open with the associated data of another kind')
    failed++
  } catch { console.log('identity vectors: blob aad OK') }

  const blob = { account: v.accountId, kind: identity.KindSettings, epoch: 1, seq: 1, device: v.deviceId, op: 'put', nonce: v.blobNonce, data: v.blobData }
  check('blobCanonical', identity.canonicalBlob(blob), v.blobCanonical)
  check('blobSig', await identity.sign(deviceKey, identity.canonicalBlob(blob)), v.blobSig)
  const wrap = { account: v.accountId, device: v.deviceId, epoch: 1, key: v.adminXPub, nonce: v.wrapNonce, data: v.wrapData }
  check('wrapCanonical', identity.canonicalWrap(wrap), v.wrapCanonical)
  check('wrapSig', await identity.sign(accountKey, identity.canonicalWrap(wrap)), v.wrapSig)
  const srk = { account: v.accountId, device: v.deviceId, key: v.podXPub }
  check('syncKeyCanonical', identity.canonicalSyncKey(srk), v.syncKeyCanonical)
  check('syncKeySig', await identity.sign(deviceKey, identity.canonicalSyncKey(srk)), v.syncKeySig)

  // Update this pod (#60): the browser signs the pod it names, the tag to install and the moment
  // it asked; the pod checks the same bytes.
  const upd = { v: 1, pod: v.deviceId, version: 'v1.11.0', created: Number(v.updateCanonical.match(/"created":(\d+)/)[1]) }
  check('updateCanonical', identity.canonicalUpdate(upd), v.updateCanonical)
  check('updateSig', await identity.sign(deviceKey, identity.canonicalUpdate(upd)), v.updateSig)
  // Power (relay.md, *Power*): the same shape, with an action in place of the version.
  const pow = { v: 1, pod: v.deviceId, action: 'shutdown', created: Number(v.powerCanonical.match(/"created":(\d+)/)[1]) }
  check('powerCanonical', identity.canonicalPower(pow), v.powerCanonical)
  check('powerSig', await identity.sign(deviceKey, identity.canonicalPower(pow)), v.powerSig)

  // A signature made by the browser must verify with the account id alone (the pod checks a
  // peer the same way).
  if (!(await identity.verify(v.accountId, v.messageSig, v.message))) {
    console.error('identity vectors: the signature of the pod must verify in the browser')
    failed++
  } else console.log('identity vectors: verify OK')

  if (failed) process.exit(1)
}

main().catch((e) => { console.error('identity vectors: ' + e.message); process.exit(1) })
