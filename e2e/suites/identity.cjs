// Identity (milestone 2): the pod makes an account from a 24-word phrase, browsers pair with
// the phrase or by an invite, and every connection is a mutual proof between two device
// certificates bound to the DTLS fingerprints. run.sh starts the relay and the pod with
// -relay; the suite pairs the pod with the CLI, then plays two browsers with a virtual
// authenticator that answers the WebAuthn PRF extension.
//
// The app runs in the iframe of the bootstrap page, on the relay origin: the suite opens it
// through localhost, since WebAuthn refuses an IP address as a relying-party id.
const fs = require('fs')
const path = require('path')
const readline = require('readline')
const http = require('http')
const { spawn, execFileSync } = require('child_process')
const { run, assert } = require('../common.cjs')

const ORIGIN = `http://localhost:${process.env.E2E_RELAY_PORT}`
const POD_ID = process.env.E2E_POD_ID
const TOKEN = process.env.E2E_TOKEN
const BIN = process.env.E2E_BIN
const DATA = process.env.E2E_DATA

const VA = {
  protocol: 'ctap2', ctap2Version: 'ctap2_1', transport: 'internal',
  hasResidentKey: true, hasUserVerification: true, isUserVerified: true,
  hasPrf: true, automaticPresenceSimulation: true,
}

/** A virtual authenticator that answers the PRF extension for one context. */
async function authenticator(page) {
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: VA })
}

/** Runs "pair create" to the end: reads the phrase (eight numbered rows of three words), the
 * positions asked back, answers them with answer(positions, phrase), and returns the phrase
 * and what came after. */
async function pairCreate(answer) {
  const p = spawn(BIN, ['-data', DATA, 'pair', 'create'], { stdio: ['pipe', 'pipe', 'pipe'] })
  const rl = readline.createInterface({ input: p.stdout })
  const err = []
  p.stderr.on('data', (d) => err.push(String(d)))
  const closed = new Promise((res) => p.once('close', res))
  // The pod writes the header, the phrase and the question in one flush: the lines that
  // arrive before anyone waits for them are queued. A line resolves to null once the command
  // has exited (the check path ends there).
  const got = []
  const waiters = []
  rl.on('line', (l) => {
    const w = waiters.shift()
    if (w) w(l)
    else got.push(l)
  })
  const line = () => {
    if (got.length) return Promise.resolve(got.shift())
    return new Promise((res) => { waiters.push(res); closed.then(() => res(null)) })
  }

  await line() // the header
  // Eight rows of three numbered words: the numbers are dropped, the words joined.
  let rows = ''
  for (let i = 0; i < 8; i++) rows += (await line()) ?? ''
  const phrase = rows.split(/\s+/).filter((w) => w && !w.endsWith(')')).join(' ')
  const pos = (((await line()) ?? '').match(/\d+/g) ?? []).map(Number)
  for (const w of answer(pos, phrase)) p.stdin.write(w + '\n')
  const rest = []
  for (let i = 0; i < 3; i++) {
    const l = await line()
    if (l === null) break
    rest.push(l)
  }
  await closed
  await new Promise((r) => setTimeout(r, 200)) // the last stderr line
  return { phrase, pos, out: rest.join('\n'), err: err.join('').trim() }
}

const status = () => execFileSync(BIN, ['-data', DATA, 'pair', 'status'], { encoding: 'utf8' })

/** Waits until the pod is registered with the relay. The signaling path answers 404 for a pod
 * that is not online and 400 for a plain request to an online one: read from Node, so the page
 * does not log a failed WebSocket. */
async function waitOnline() {
  const codes = []
  for (let i = 0; i < 40; i++) {
    const code = await new Promise((res) => {
      const req = http.get(`http://127.0.0.1:${process.env.E2E_RELAY_PORT}/_relay/signal?pod=${POD_ID}&session=probe`, (r) => {
        r.resume()
        res(r.statusCode)
      })
      req.on('error', () => res(0))
      req.setTimeout(3000, () => { req.destroy(); res(0) })
    })
    if (code !== 404 && code !== 0) return true
    codes.push(code)
    await new Promise((r) => setTimeout(r, 500))
  }
  console.log('  dbg  signaling codes: ' + codes.join(' '))
  return false
}

/** Opens the bootstrap page and waits for the app iframe or the error the page shows. The
 * hash alone does not reload the page (the tunnel of a connected device would be kept), so
 * the navigation is followed by a reload: the handshake runs again. The pod may still be
 * registering with the relay: the page is reloaded until it answers. */
async function open(page, url) {
  for (let i = 0; i < 6; i++) {
    await page.goto(url, { waitUntil: 'domcontentloaded' })
    await page.reload({ waitUntil: 'domcontentloaded' })
    try {
      await page.waitForSelector('#frame, .err', { timeout: 20000 })
    } catch {}
    const msg = (await page.textContent('#msg')).trim()
    if (!msg.includes('not online')) return msg
    await new Promise((r) => setTimeout(r, 1000))
  }
  return (await page.textContent('#msg')).trim()
}

run(
  async ({ page, ctx, errors }) => {
    // 1. The phrase is checked before anything is stored: a wrong confirmation word stores
    // nothing, and the phrase is never written to disk.
    const wrong = 'not-the-word'
    const bad = await pairCreate(() => [wrong, wrong, wrong])
    assert(bad.err.includes('does not match') && !fs.existsSync(path.join(DATA, 'identity.json')),
      'a wrong confirmation word stores nothing: ' + bad.err)

    // 2. The real create: the phrase is shown once, three words are asked back.
    const words = (pos, phrase) => pos.map((p) => phrase.split(' ')[p - 1])
    const created = await pairCreate(words)
    assert(created.out.includes('device.key (0600)'), 'create stores only the keys: ' + created.out.replace(/\n/g, ' | '))
    assert((fs.statSync(path.join(DATA, 'device.key')).mode & 0o777) === 0o600, 'device.key is 0600')
    const stored = fs.readFileSync(path.join(DATA, 'identity.json'), 'utf8')
    assert(!stored.includes(created.phrase.split(' ')[0]), 'identity.json holds no phrase')
    const phrase = created.phrase
    assert(phrase.split(' ').length === 24, 'the phrase is 24 words')

    const podStatus = status()
    const podLine = podStatus.match(/device\s+: (\S+) \((.*)\)/)
    const accountLine = podStatus.match(/account\s+: (\S+) \((.*)\)/)
    assert(podLine && accountLine, 'pair status shows the device and the account: ' + podStatus.replace(/\n/g, ' | '))

    // The pod reads its identity at startup: restart it so the pairing is live, and wait until
    // it is registered with the relay.
    execFileSync('bash', [process.env.E2E_RESTART_POD])
    assert(await waitOnline(), 'the restarted pod is online at the relay')
    await authenticator(page)

    // 3. Browser 1 pairs with the phrase and becomes an admin (virtual authenticator, PRF).
    // An origin with no device and no token must show the phrase form: the wordlist is part
    // of that path (a missing wordlist leaves the page on the spinner).
    const msg = await open(page, `${ORIGIN}/#pod=${POD_ID}`)
    assert(msg.includes('No device on this origin'), 'the phrase form shows up on an unpaired origin: ' + msg)
    await page.waitForSelector('#pair input', { timeout: 20000 })
    await page.fill('#pair input', phrase)
    await page.keyboard.press('Enter')
    await page.waitForSelector('#frame', { timeout: 30000 })
    const b1 = await page.evaluate(async () => {
      const m = await import('/_relay/identity.js')
      const wl = await m.wordlist()
      return {
        device: window.identity.device, account: window.identity.account,
        fp: window.identity.fingerprint, acctFp: await m.fingerprint(window.identity.account, wl),
      }
    })
    assert(b1.account === accountLine[1], 'the browser derives the same account as the pod: ' + b1.account)
    assert(b1.acctFp === accountLine[2], 'the fingerprint of the account is the same in the pod and in the browser: ' + b1.acctFp)
    assert(b1.device !== podLine[1], 'the browser is its own device, not the pod: ' + b1.device)

    // Settings > Remote access > Devices, on the relay origin. The app is the iframe of the
    // bootstrap page; a project must be open for the menubar.
    const app = page.mainFrame().childFrames()[0]
    await app.waitForSelector('.home .home-bar', { timeout: 30000 })
    if (await app.locator('.project-open:has-text("demo")').count()) await app.click('.project-open:has-text("demo")')
    else await app.click('.ws-dir:has-text("demo")')
    await app.waitForSelector('.menubar', { timeout: 30000 })
    await app.click('.menubar button[title^="Settings"]')
    await app.waitForSelector('.settings')
    await app.click('.settings-nav button:has-text("Remote access")')
    await app.waitForSelector('[data-testid=devices] [data-testid=device-list]', { timeout: 20000 })
    const devices = await app.textContent('[data-testid=devices] [data-testid=device-list]')
    assert(devices.includes(podLine[2]) && devices.includes(b1.fp), 'the panel lists the pod and this browser by their fingerprint: ' + devices.trim().slice(0, 200))
    assert(devices.includes('pod') && devices.includes('browser'), 'the devices carry their kind')
    assert((await app.locator('[data-testid=device-list] tr').count()) === 3, 'two devices and the header row')

    // 4. An invite for a phone: the code, the QR, and the fingerprint the admin compares.
    await app.click('[data-testid=devices] button:has-text("Invite a device")')
    await app.waitForSelector('.qr svg', { timeout: 15000 })
    const code = (await app.textContent('.qr .mono')).trim()
    assert(/^[A-Z2-7]{8}$/.test(code), 'the invite code is 8 base32 characters: ' + code)
    assert((await app.textContent('.qr')).includes(b1.fp), 'the panel shows the fingerprint of the inviting device')
    // The link carries the pod the admin is on: a phone opens it in its camera app.
    const linkText = (await app.textContent('.qr p.mono')).trim()
    assert(linkText.includes(`#pod=${POD_ID}&join=${code}`), 'the invite link carries the pod and the code: ' + linkText)

    // 5. Browser 2 joins by the invite link. Its key must be the one the admin reads. The
    // extra contexts keep the English locale of the suites, since the page words follow the
    // browser language.
    const ctx2 = await ctx.browser().newContext({ locale: process.env.E2E_LOCALE ?? 'en-US' })
    const page2 = await ctx2.newPage()
    await authenticator(page2)
    await page2.goto(linkText, { waitUntil: 'domcontentloaded' })
    await page2.waitForFunction(() => window.identity?.device, null, { timeout: 20000 })
    const b2 = await page2.evaluate(() => window.identity)
    assert((await page2.textContent('#msg')).includes(b2.fingerprint), 'the joining device shows its fingerprint')

    await app.waitForSelector(`.settings-body .rules tr:has-text("${code}")`, { timeout: 15000 })
    const row = await app.textContent(`.settings-body .rules tr:has-text("${code}")`)
    assert(row.includes(b2.fingerprint), 'the admin reads the key the device posted, by its fingerprint: ' + row.trim())
    await app.click(`.settings-body .rules tr:has-text("${code}") button:has-text("Approve")`)

    await page2.waitForSelector('#frame', { timeout: 30000 })
    assert(!(await page2.textContent('#msg')).includes('err'), 'the joined device connects: ' + b2.fingerprint)

    // 6. A browser with no certificate is refused: a paired pod refuses the token mode.
    const ctx3 = await ctx.browser().newContext({ locale: process.env.E2E_LOCALE ?? 'en-US' })
    const page3 = await ctx3.newPage()
    await authenticator(page3)
    await page3.goto(`${ORIGIN}/#pod=${POD_ID}&t=${TOKEN}`, { waitUntil: 'domcontentloaded' })
    await page3.waitForSelector('.err', { timeout: 30000 })
    assert((await page3.textContent('.err')).includes('refuses the token mode'), 'an unpaired browser is refused: ' + (await page3.textContent('.err')))

    // 7. The admin revokes browser 2: the relay stores the signed list and pushes it to the
    // pod, which checks it itself. The revoked device is refused at the next connection.
    await app.click(`[data-testid=device-list] tr:has-text("${b2.fingerprint}") button:has-text("Revoke")`)
    await app.waitForSelector('.toast:has-text("epoch")', { timeout: 15000 })
    for (let i = 0; i < 20 && !status().includes('revocations'); i++) await new Promise((r) => setTimeout(r, 500))
    const after = status()
    assert(after.includes('revocations : seq 1, epoch 1, 1 device(s)'), 'the pod applied the revocation: ' + after.replace(/\n/g, ' | '))

    // Back to the plain link (the invite code is single use): the revoked device is refused.
    const refused = await open(page2, `${ORIGIN}/#pod=${POD_ID}`)
    assert(refused.includes('Device revoked'), 'the revoked device is refused: ' + refused)
    assert(await page.waitForSelector('#frame'), 'the admin device is still connected')

    await ctx2.close()
    await ctx3.close()
    if (errors.length) assert(false, 'page errors:\n    ' + errors.join('\n    '))
  },
  { url: `${ORIGIN}/#pod=${POD_ID}` },
)
