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
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', { options: VA })
  return { cdp, authenticatorId }
}

/** How many times the passkeys of an authenticator were used: each assertion moves the count. */
async function assertions(a) {
  const { credentials } = await a.cdp.send('WebAuthn.getCredentials', { authenticatorId: a.authenticatorId })
  return credentials.reduce((n, c) => n + c.signCount, 0)
}

/** The unlocked sessions this origin keeps (identity.js, keepUnlocked). */
const sessionsOf = (page) => page.evaluate(() => new Promise((resolve) => {
  const r = indexedDB.open('webide-session', 1)
  r.onupgradeneeded = () => r.result.createObjectStore('session')
  r.onsuccess = () => {
    const q = r.result.transaction('session').objectStore('session').getAll()
    q.onsuccess = () => resolve(q.result.map((s) => ({ device: s.device, until: s.until, extractable: s.key.extractable })))
  }
}))

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
    const auth1 = await authenticator(page)

    // 3. Browser 1 pairs with the phrase and becomes an admin (virtual authenticator, PRF).
    // An origin with no device and no token must show the phrase form: the wordlist is part
    // of that path (a missing wordlist leaves the page on the spinner).
    const msg = await open(page, `${ORIGIN}/#pod=${POD_ID}`)
    assert(msg.includes('No device is paired in this browser'), 'the phrase form shows up on an unpaired origin: ' + msg)
    await page.waitForSelector('#phrase', { timeout: 20000 })
    await page.fill('#phrase', phrase)
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

    // Settings > Remote access > Members, on the relay origin. The app is the iframe of the
    // bootstrap page; a project must be open for the menubar.
    const app = page.mainFrame().childFrames()[0]
    await app.waitForSelector('.home .home-bar', { timeout: 30000 })
    if (await app.locator('.project-open:has-text("demo")').count()) await app.click('.project-open:has-text("demo")')
    else await app.click('.ws-dir:has-text("demo")')
    await app.waitForSelector('.menubar', { timeout: 30000 })
    await app.click('.menubar button[title^="Settings"]')
    await app.waitForSelector('.settings')
    await app.click('.settings-nav button:has-text("Remote access")')
    await app.waitForSelector('[data-testid=members] [data-testid=member-list]', { timeout: 20000 })
    const members = await app.textContent('[data-testid=members] [data-testid=member-list]')
    assert(members.includes(podLine[2]) && members.includes(b1.fp), 'the panel lists the pod and this browser by their fingerprint: ' + members.trim().slice(0, 200))
    assert(members.includes('pod') && members.includes('browser'), 'the members carry their kind')
    assert((await app.locator('[data-testid=member-list] tr').count()) === 3, 'two members and the header row')

    // The GitHub link: only a device that proved its key may start it, so the panel's link
    // proves possession. The window goes on to GitHub with the OAuth App of the relay.
    await page.context().route((u) => u.hostname === 'github.com', (r) => r.fulfill({ status: 200, contentType: 'text/html', body: '<p>GitHub</p>' }))
    const [authorize] = await Promise.all([
      page.context().waitForEvent('request', { predicate: (r) => r.url().startsWith('https://github.com/login/oauth/authorize'), timeout: 15000 }),
      app.click('[data-testid=members] button:has-text("Link a GitHub account")'),
    ])
    const ghURL = new URL(authorize.url())
    assert(ghURL.searchParams.get('client_id') === 'e2e-client' && ghURL.searchParams.get('state'), 'Link a GitHub account opens GitHub with the OAuth App and a state: ' + authorize.url())
    assert(!(await app.locator('.toast:has-text("only a device")').count()), 'no "only a device of the account" error')
    for (const p of page.context().pages()) if (p.url().startsWith('https://github.com/')) await p.close()

    // The level of the account (#87), as Settings > Remote access shows it: the badge next to the
    // TURN quota and next to the GitHub login, read again from a fresh view of the page.
    const levelView = async () => {
      await app.click('.settings-nav button:has-text("Keyboard shortcuts")').catch(() => {})
      await app.click('.settings-nav button:has-text("Remote access")')
      await app.waitForSelector('[data-testid=members] [data-testid=github-link]', { timeout: 20000 })
      await app.waitForTimeout(300)
      const text = async (sel) => (await app.locator(sel).count()) ? (await app.textContent(sel)).trim() : ''
      return { turn: await text('[data-testid=turn-level]'), members: await text('[data-testid=github-link] [data-testid=level-badge]'),
        unlimited: (await app.locator('[data-testid=turn-unlimited]').count()) > 0 }
    }
    const waitLevel = async (ok) => {
      let v
      for (let i = 0; i < 30; i++) {
        v = await levelView()
        if (ok(v)) return v
      }
      return v
    }
    const relayCLI = (...args) => execFileSync(BIN, ['-data', process.env.E2E_RELAY_DATA, 'relay', ...args], { encoding: 'utf8' })

    // The VIP list by account id, written by the CLI next to the live relay: no GitHub link is
    // needed, and the pod is told without reconnecting (run.sh: WEBIDE_LEVEL_POLL=1s).
    assert(/is a VIP, until/.test(relayCLI('vip', 'add', b1.account, '-days', '12', '-note', 'e2e friend')), 'relay vip add takes an account id')
    let lv = await waitLevel((v) => v.turn === 'VIP · 12 d' && v.members === 'VIP · 12 d')
    assert(lv.turn === 'VIP · 12 d' && lv.unlimited, 'a VIP added with the CLI shows VIP · 12 d and ∞ next to the TURN quota: ' + JSON.stringify(lv))
    assert(lv.members === 'VIP · 12 d', 'and next to the GitHub login: ' + JSON.stringify(lv))
    const listed = relayCLI('supporters')
    assert(listed.includes(b1.account) && listed.includes('VIP') && listed.includes('e2e friend'), 'relay supporters lists the VIP: ' + listed)
    relayCLI('vip', 'remove', b1.account)
    lv = await waitLevel((v) => !v.turn && !v.members && !v.unlimited)
    assert(!lv.turn && !lv.members && !lv.unlimited, 'removed, the account is back to free: ' + JSON.stringify(lv))

    // A sponsorship of the linked login moves the account to Supporter: the relay tells the pod
    // at once (no reconnection), and Settings reads used / ∞ with a thank-you. The link itself
    // is written in the base, GitHub being out of reach; the event is a real signed one.
    execFileSync('python3', ['-I', '-c', `import sqlite3,sys,time
db=sqlite3.connect(sys.argv[1]); db.execute("INSERT INTO github (account, github_id, login, created) VALUES (?,?,?,?)", (sys.argv[2], "4242", "e2e", int(time.time()))); db.commit()`,
      path.join(process.env.E2E_RELAY_DATA, 'relay.db'), b1.account])
    const event = JSON.stringify({ action: 'created', sponsorship: { created_at: new Date().toISOString(),
      sponsor: { id: 4242, login: 'e2e' }, tier: { monthly_price_in_cents: 500, is_one_time: false } } })
    const sig = 'sha256=' + require('crypto').createHmac('sha256', 'e2e-hook').update(event).digest('hex')
    const hook = await fetch(process.env.E2E_RELAY_URL + '/_relay/github/webhook', { method: 'POST', body: event,
      headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': 'sponsorship', 'X-Hub-Signature-256': sig } })
    assert(hook.status === 204, 'the relay takes the signed sponsorship event: ' + hook.status)
    let unlimited = ''
    for (let i = 0; i < 20 && !unlimited; i++) {
      await app.click('.settings-nav button:has-text("Keyboard shortcuts")').catch(() => {})
      await app.click('.settings-nav button:has-text("Remote access")')
      await app.waitForTimeout(300)
      if (await app.locator('[data-testid=turn-unlimited]').count()) unlimited = (await app.textContent('.settings-body .props')).replace(/\s+/g, ' ')
    }
    assert(unlimited.includes('/ ∞') && unlimited.includes('hugs'), 'Settings tells the new supporter at once: used / ∞ and a thank-you: ' + unlimited.slice(0, 300))
    lv = await levelView()
    assert(lv.turn === 'Supporter' && lv.members === 'Supporter', 'the badge of a $5 sponsor: ' + JSON.stringify(lv))
    // A sponsor on the VIP list too has both words, and the end of the VIP level.
    relayCLI('vip', 'add', b1.account, '-days', '12')
    lv = await waitLevel((v) => v.turn === 'Supporter · VIP · 12 d')
    assert(lv.turn === 'Supporter · VIP · 12 d' && lv.members === 'Supporter · VIP · 12 d', 'a sponsor who is a VIP too: ' + JSON.stringify(lv))

    // The Team tier (#88): $50 a month gives 10 seats; the sponsor is the admin of its team and
    // takes no seat. Members are added by account id (here made up: a member needs no pod).
    const tier = JSON.stringify({ action: 'tier_changed', sponsorship: { created_at: new Date().toISOString(),
      sponsor: { id: 4242, login: 'e2e' }, tier: { monthly_price_in_cents: 5000, is_one_time: false } } })
    const tierSig = 'sha256=' + require('crypto').createHmac('sha256', 'e2e-hook').update(tier).digest('hex')
    const tierHook = await fetch(process.env.E2E_RELAY_URL + '/_relay/github/webhook', { method: 'POST', body: tier,
      headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': 'sponsorship', 'X-Hub-Signature-256': tierSig } })
    assert(tierHook.status === 204, 'the relay takes the $50 tier: ' + tierHook.status)
    const seatsText = async () => {
      await levelView()
      await app.waitForSelector('[data-testid=team-seats]', { timeout: 10000 }).catch(() => {})
      return (await app.locator('[data-testid=team-seats]').count()) ? (await app.textContent('[data-testid=team-seats]')).trim() : ''
    }
    let seatsNow = await seatsText()
    assert(seatsNow === '0/10 seats' && (await app.textContent('[data-testid=team] h4')).includes('Team e2e'), 'a $50 sponsor manages its team: ' + seatsNow)
    const newAccount = () => require('crypto').randomBytes(32).toString('base64url')
    const first = newAccount()
    await app.fill('[data-testid=team-add]', first)
    await app.press('[data-testid=team-add]', 'Enter')
    await app.waitForFunction(() => document.querySelector('[data-testid=team-seats]')?.textContent?.trim() === '1/10 seats', null, { timeout: 10000 })
    assert((await app.locator('[data-testid=team-member]').count()) === 1, 'the member is listed')
    assert(relayCLI('supporters').includes('team e2e'), 'relay supporters lists the member of the team')
    for (let i = 0; i < 9; i++) relayCLI('team', 'add', 'e2e', newAccount())
    seatsNow = await seatsText()
    assert(seatsNow === '10/10 seats', 'nine more by the CLI: ' + seatsNow)
    await app.fill('[data-testid=team-add]', newAccount())
    await app.press('[data-testid=team-add]', 'Enter')
    await app.waitForSelector('.toast:has-text("no seat left")', { timeout: 10000 })
    assert((await app.locator('[data-testid=team-member]').count()) === 10, 'an eleventh member is refused')
    await app.click('[data-testid=team-member]:first-child button:has-text("Remove")')
    await app.waitForFunction(() => document.querySelector('[data-testid=team-seats]')?.textContent?.trim() === '9/10 seats', null, { timeout: 10000 })
    assert(!relayCLI('team', 'list').includes(first), 'the member removed from the panel is off the team')
    lv = await levelView()
    assert(lv.turn === 'VIP' && lv.members === 'VIP', 'the $50 sponsor is a VIP itself, with no end: ' + JSON.stringify(lv))

    // 4. An invite for a phone: the code, the QR, and the fingerprint the admin compares.
    await app.click('[data-testid=members] button:has-text("Invite a device")')
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
    await app.click(`[data-testid=member-list] tr:has-text("${b2.fingerprint}") button:has-text("Revoke")`)
    await app.waitForSelector('.toast:has-text("epoch")', { timeout: 15000 })
    for (let i = 0; i < 20 && !status().includes('revocations'); i++) await new Promise((r) => setTimeout(r, 500))
    const after = status()
    assert(after.includes('revocations : seq 1, epoch 1, 1 device(s)'), 'the pod applied the revocation: ' + after.replace(/\n/g, ' | '))

    // Back to the plain link (the invite code is single use): the revoked device is refused.
    const refused = await open(page2, `${ORIGIN}/#pod=${POD_ID}`)
    assert(refused.includes('Member revoked'), 'the revoked member is refused: ' + refused)
    assert(await page.waitForSelector('#frame'), 'the admin device is still connected')

    // The revoked device ended its session: it would not open a pod even before the refusal.
    assert((await sessionsOf(page2)).length === 0, 'a revoked device keeps no session')

    // 8. The device stays unlocked an hour after its last use: a reload opens the pods without
    // the passkey, and only a non-extractable key is kept.
    const sess = await sessionsOf(page)
    assert(sess.length === 1 && sess[0].device === b1.device && !sess[0].extractable, 'one session, a non-extractable key: ' + JSON.stringify(sess))
    assert(sess[0].until > Date.now() + 50 * 60 * 1000, 'the session runs an hour')
    const before = await assertions(auth1)
    let msg8 = await open(page, `${ORIGIN}/#pod=${POD_ID}`)
    assert(await page.isVisible('#frame'), 'the reload opens the app: ' + msg8)
    assert((await assertions(auth1)) === before, 'a reload within the hour does not ask the passkey')

    // An hour gone by: the passkey is asked again.
    await page.evaluate(() => new Promise((resolve) => {
      const r = indexedDB.open('webide-session', 1)
      r.onsuccess = () => {
        const st = r.result.transaction('session', 'readwrite').objectStore('session')
        const q = st.getAll()
        q.onsuccess = () => { for (const s of q.result) st.put({ ...s, until: Date.now() - 1000 }, s.device); resolve() }
      }
    }))
    msg8 = await open(page, `${ORIGIN}/#pod=${POD_ID}`)
    assert(await page.isVisible('#frame'), 'the expired session unlocks with the passkey: ' + msg8)
    assert((await assertions(auth1)) > before, 'an expired session asks the passkey')

    // 9. A passkey not given (dismissed, or a browser that wants a click first) shows the unlock
    // view and its button; the button unlocks. The first prompt of the page is refused once.
    const used = await assertions(auth1)
    await page.context().addInitScript(() => {
      if (sessionStorage.getItem('dismissOnce') !== '1') return
      sessionStorage.removeItem('dismissOnce')
      const get = navigator.credentials.get.bind(navigator.credentials)
      let first = true
      navigator.credentials.get = (o) => {
        if (!first) return get(o)
        first = false
        return Promise.reject(new DOMException('dismissed', 'NotAllowedError'))
      }
    })
    await page.evaluate(async () => {
      sessionStorage.setItem('dismissOnce', '1')
      await (await import('/_relay/identity.js')).lock()
    })
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.waitForSelector('#msg button:has-text("Unlock with your passkey")', { timeout: 20000 })
    assert((await page.textContent('#msg')).includes(b1.fp.split(' ')[0]), 'the unlock view shows the fingerprint of the device')
    await page.click('#msg button:has-text("Unlock with your passkey")')
    await page.waitForSelector('#frame', { state: 'visible', timeout: 30000 })
    assert((await assertions(auth1)) > used, 'the button asks the passkey')

    // 10. Lock in Settings > Remote access > Members ends the session at once.
    const app10 = page.mainFrame().childFrames()[0]
    await app10.waitForSelector('.menubar, .home .home-bar', { timeout: 30000 })
    if (!(await app10.locator('.menubar').count())) {
      if (await app10.locator('.project-open:has-text("demo")').count()) await app10.click('.project-open:has-text("demo")')
      else await app10.click('.ws-dir:has-text("demo")')
      await app10.waitForSelector('.menubar', { timeout: 30000 })
    }
    await app10.click('.menubar button[title^="Settings"]')
    await app10.click('.settings-nav button:has-text("Remote access")')
    await app10.click('[data-testid=lock-device]', { timeout: 20000 })
    await app10.waitForSelector('.toast:has-text("locked")', { timeout: 15000 })
    assert((await sessionsOf(page)).length === 0, 'Lock ends the session')

    // 11. The welcome view of an origin with no device: the three ways in, and the phrase is
    // checked as it is typed.
    const ctx4 = await ctx.browser().newContext({ locale: process.env.E2E_LOCALE ?? 'en-US' })
    const page4 = await ctx4.newPage()
    await authenticator(page4)
    await page4.goto(`${ORIGIN}/`, { waitUntil: 'domcontentloaded' })
    await page4.waitForSelector('#phrase', { timeout: 20000 })
    const welcome = await page4.textContent('#msg')
    assert(welcome.includes('Your machines, from any browser') && welcome.includes('I have an invite link') && welcome.includes('New here?'), 'the welcome view: ' + welcome.slice(0, 200))
    await page4.fill('#phrase', phrase.split(' ').slice(0, 23).join(' ') + ' notaword')
    await page4.waitForSelector('#phrase-check .bad', { timeout: 5000 })
    await page4.fill('#phrase', phrase)
    await page4.waitForSelector('#phrase-check .good', { timeout: 5000 })
    assert(!(await page4.evaluate(() => document.querySelector('[href^="http"]:not([href^="https://github.com/"])'))), 'no link out of the page but the docs')

    await ctx2.close()
    await ctx3.close()
    await ctx4.close()
    if (errors.length) assert(false, 'page errors:\n    ' + errors.join('\n    '))
  },
  { url: `${ORIGIN}/#pod=${POD_ID}` },
)
