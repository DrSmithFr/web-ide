// Checks the shared test vectors (pod/internal/identity/vectors.json) with the browser code
// (pod/internal/identity/web/identity.js): the same phrase must give the same seed, the same
// ids, the same canonical bytes and the same signatures in Go and in the browser. Run by
// make test; the e2e identity suite runs the same module in Chromium.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import * as identity from '../pod/internal/identity/web/identity.js'

const root = fileURLToPath(new URL('../', import.meta.url))
const v = JSON.parse(readFileSync(root + 'pod/internal/identity/vectors.json', 'utf8'))
const wordlist = readFileSync(root + 'pod/internal/identity/wordlist.txt', 'utf8').split('\n').filter(Boolean)

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

  // A signature made by the browser must verify with the account id alone (the pod checks a
  // peer the same way).
  if (!(await identity.verify(v.accountId, v.messageSig, v.message))) {
    console.error('identity vectors: the signature of the pod must verify in the browser')
    failed++
  } else console.log('identity vectors: verify OK')

  if (failed) process.exit(1)
}

main().catch((e) => { console.error('identity vectors: ' + e.message); process.exit(1) })
