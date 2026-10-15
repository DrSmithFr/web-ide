// Settings sync (milestone 4): two pods of one account. run.sh starts the relay and pod A; the
// suite makes the account with the CLI, enrolls an admin browser with the phrase, pairs pod B by
// an invite (so the wrap path runs: pod B never knows the phrase), then opens one window per pod.
// A theme changed on A reaches B; only a true conflict asks; the layout stays per device; the
// vault syncs only when switched on; the relay base holds no clear text.
const fs = require('fs')
const path = require('path')
const readline = require('readline')
const http = require('http')
const { spawn, execFileSync } = require('child_process')
const { run, assert } = require('../common.cjs')

const ROOT = path.join(__dirname, '..', '..')
const BIN = process.env.E2E_BIN
const DATA = process.env.E2E_DATA
const TMP = process.env.E2E_TMP
const WS = process.env.E2E_WS
const RELAY_PORT = process.env.E2E_RELAY_PORT
const ORIGIN = `http://localhost:${RELAY_PORT}`
const POD_A = process.env.E2E_POD_ID
// Pod B lives on its own port of the block of the suite (pod +70), never on a fixed one.
const PORT_B = Number(process.env.E2E_PORT ?? 4519) + 70
const DATA_B = path.join(TMP, 'dataB')
const POD_B = 'pod-b'
const KEY = 'sk-secret-123'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const VA = {
  protocol: 'ctap2', ctap2Version: 'ctap2_1', transport: 'internal',
  hasResidentKey: true, hasUserVerification: true, isUserVerified: true,
  hasPrf: true, automaticPresenceSimulation: true,
}

async function authenticator(page) {
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: VA })
}

/** Opens the bootstrap page and waits for the app iframe or the error the page shows. The hash
 * alone does not reload the page, so the navigation is followed by a reload. */
async function open(page, url) {
  for (let i = 0; i < 6; i++) {
    await page.goto(url, { waitUntil: 'domcontentloaded' })
    await page.reload({ waitUntil: 'domcontentloaded' })
    try {
      await page.waitForSelector('#frame, .err', { timeout: 20000 })
    } catch {}
    const msg = (await page.textContent('#msg')).trim()
    if (!msg.includes('not online')) return msg
    await sleep(1000)
  }
  return (await page.textContent('#msg')).trim()
}

const appOf = (page) => page.mainFrame().childFrames()[0]

// The app opens on the home, or straight on the project it had last time (a reload keeps it).
async function openProject(app) {
  await app.waitForSelector('.home .home-bar, .menubar', { timeout: 30000 })
  // A project of this pod: another pod holds a project of the same name, in the same list.
  const local = '.home main > .project-list a.project-open:not([data-testid=pod-project]):has-text("demo")'
  if (await app.locator(local).count()) await app.click(local)
  else if (await app.locator('.ws-dir:has-text("demo")').count()) await app.click('.ws-dir:has-text("demo")')
  await app.waitForSelector('.menubar', { timeout: 30000 })
}

async function openSettings(app) {
  await app.click('.menubar button[title^="Settings"]')
  await app.waitForSelector('.settings', { timeout: 20000 })
}

const section = (app, name) => app.click(`.settings-nav button:has-text("${name}")`)

/** A pod whose line is read from the process: a line resolves to null once it has exited. */
function reader(p) {
  const rl = readline.createInterface({ input: p.stdout })
  const log = [], queue = [], waiters = []
  const closed = new Promise((res) => p.once('close', res))
  rl.on('line', (l) => {
    log.push(l)
    const w = waiters.shift()
    if (w) w(l)
    else queue.push(l)
  })
  return {
    log,
    closed,
    line: () => (queue.length ? Promise.resolve(queue.shift()) : Promise.race([new Promise((res) => waiters.push(res)), closed.then(() => null)])),
  }
}

async function waitOnline(podId) {
  for (let i = 0; i < 40; i++) {
    const code = await new Promise((res) => {
      const req = http.get(`${ORIGIN}/_relay/signal?pod=${podId}&session=probe`, (r) => { r.resume(); res(r.statusCode) })
      req.on('error', () => res(0))
      req.setTimeout(3000, () => { req.destroy(); res(0) })
    })
    if (code !== 404 && code !== 0) return true
    await sleep(500)
  }
  return false
}

let bProc = null
function startPodB() {
  bProc = spawn(BIN, ['-addr', `127.0.0.1:${PORT_B}`, '-data', DATA_B, '-workspace', WS,
    '-static', path.join(ROOT, 'pod', 'webdist', 'dist'), '-relay', `http://127.0.0.1:${RELAY_PORT}`, '-force-turn', '-lan', 'off'],
    { stdio: ['ignore', 'pipe', 'pipe'] })
  bProc.stdout.on('data', (d) => process.stdout.write('  B| ' + d))
  bProc.stderr.on('data', (d) => process.stderr.write('  B| ' + d))
  return bProc
}
async function stopPodB() {
  if (!bProc) return
  bProc.kill('SIGTERM')
  await Promise.race([new Promise((res) => bProc.once('exit', res)), sleep(5000)])
  bProc = null
}
process.on('SIGTERM', () => { try { bProc && bProc.kill() } catch {} })

const history = (dir) => JSON.parse(fs.readFileSync(path.join(dir, 'settings.json'), 'utf8'))
const current = (dir) => {
  const h = history(dir)
  return (h.entries.find((e) => e.id === h.current) ?? h.entries[h.entries.length - 1]).settings
}
/** Waits until the current settings of a pod answer a test: a change applied from the other pod
 * goes through Settings.Save, so the file on disk is what the test reads. */
async function waitCurrent(dir, test, label) {
  for (let i = 0; i < 40; i++) {
    if (test(current(dir))) return
    await sleep(500)
  }
  assert(false, label + ': ' + JSON.stringify(current(dir)))
}

// Appends an entry to the settings of a stopped pod, leaving its sync base untouched: the pod
// comes back with a local change the relay has never seen.
function appendSettings(dir, patch, label) {
  const h = history(dir)
  const e = { id: h.nextId, ts: new Date().toISOString(), label, settings: { ...current(dir), ...patch } }
  h.entries.push(e)
  h.nextId++
  h.current = e.id
  fs.writeFileSync(path.join(dir, 'settings.json'), JSON.stringify(h, null, 2))
}

// One line of the relay base, the values joined (a row printed as a tuple would never compare to
// a single value).
const py = (sql) => execFileSync('python3', ['-c',
  "import sqlite3,sys,os;c=sqlite3.connect(os.environ['E2E_RELAY_DATA']+'/relay.db');print(' '.join(' '.join(str(x) for x in r) for r in c.execute(sys.argv[1]).fetchall()))", sql],
  { encoding: 'utf8' }).trim()

run(
  async ({ page, ctx, errors }) => {
    try {
      // 1. Pod A makes the account with the phrase, and gets a server with an API key before it
      // starts: the vault is what carries it to the other pod.
      const p = spawn(BIN, ['-data', DATA, 'pair', 'create'], { stdio: ['pipe', 'pipe', 'pipe'] })
      const r = reader(p)
      await r.line() // the header
      // Eight rows of three numbered words: the numbers are dropped, the words joined.
      let rows = ''
      for (let i = 0; i < 8; i++) rows += (await r.line()) ?? ''
      const phrase = rows.split(/\s+/).filter((w) => w && !w.endsWith(')')).join(' ')
      const pos = (((await r.line()) ?? '').match(/\d+/g) ?? []).map(Number)
      for (const w of pos.map((i) => phrase.split(' ')[i - 1])) p.stdin.write(w + '\n')
      await r.closed
      assert(phrase.split(' ').length === 24, 'the phrase of pod A is 24 words: ' + phrase)
      fs.mkdirSync(DATA, { recursive: true })
      fs.writeFileSync(path.join(DATA, 'llm.json'), JSON.stringify({ servers: [{ id: 'srv1', name: 'x', kind: 'openai', url: 'http://127.0.0.1:9999', apiKey: KEY }] }))
      execFileSync('bash', [process.env.E2E_RESTART_POD])
      assert(await waitOnline(POD_A), 'pod A is online at the relay')

      // 2. The admin browser: the only device that holds the account key, so the only one that
      // can wrap the key of the epoch for the pods.
      await authenticator(page)
      const msg = await open(page, `${ORIGIN}/#pod=${POD_A}`)
      assert(msg.includes('No device is paired in this browser'), 'the phrase form shows up: ' + msg)
      await page.waitForSelector('#phrase', { timeout: 20000 })
      await page.fill('#phrase', phrase)
      await page.keyboard.press('Enter')
      await page.waitForSelector('#frame', { timeout: 30000 })
      const appA = appOf(page)
      await openProject(appA)
      await openSettings(appA)
      await section(appA, 'Remote access')
      await appA.waitForSelector('[data-testid=devices] [data-testid=device-list]', { timeout: 20000 })
      // The admin browser is the only device that holds the account key, so it is the only one that
      // can wrap the key of the epoch: pod A, which never knew the phrase, gets it wrapped here.
      await appA.waitForSelector('[data-testid=sync] .check:has-text("settings") input', { timeout: 20000 })
      let panelA = ''
      for (let i = 0; i < 40; i++) {
        panelA = (await appA.locator('[data-testid=sync]').textContent()).trim()
        if (panelA.includes('synced')) break
        await sleep(500)
      }
      assert(panelA.includes('synced'), 'the key of the epoch was wrapped for pod A: ' + panelA)

      // 3. Pod B joins by an invite: the admin approves, then wraps the key of the epoch for it.
      fs.mkdirSync(DATA_B, { recursive: true })
      fs.writeFileSync(path.join(DATA_B, 'relay.json'), JSON.stringify({ podId: POD_B, secret: 'a'.repeat(32) }))
      // The same server id, without the key: the merge is per entry, so the key can be applied.
      fs.writeFileSync(path.join(DATA_B, 'llm.json'), JSON.stringify({ servers: [{ id: 'srv1', name: 'x', kind: 'openai', url: 'http://127.0.0.1:9999' }] }))
      // The admin invites a device; pod B posts its key to that code, so the fingerprint the
      // panel reads is the one of its key.
      await appA.click('[data-testid=devices] button:has-text("Invite a device")')
      await appA.waitForSelector('.qr svg', { timeout: 20000 })
      const code = (await appA.textContent('.qr .mono')).trim()
      assert(/^[A-Z2-7]{8}$/.test(code), 'the invite code is 8 base32 characters: ' + code)
      const j = reader(spawn(BIN, ['-data', DATA_B, '-relay', `http://127.0.0.1:${RELAY_PORT}`, '-name', 'phone', 'pair', 'join', code], { stdio: ['ignore', 'pipe', 'pipe'] }))
      let fpB = ''
      for (let i = 0; i < 60 && !fpB; i++) {
        const l = await j.line()
        // A blank line ends the QR block: only a null line means the process has exited.
        if (l === null) break
        if (l.includes('Fingerprint of this device:')) fpB = l.split(': ')[1]
      }
      assert(fpB, 'pod B shows the fingerprint of its key: ' + fpB)
      await appA.waitForSelector(`.settings-body .rules tr:has-text("${code}") button:has-text("Approve")`, { timeout: 20000 })
      await appA.click(`.settings-body .rules tr:has-text("${code}") button:has-text("Approve")`)
      await j.closed
      assert(j.log.join(' ').includes('Paired with account'), 'pod B is paired by the admin browser')

      // 4. Pod B publishes its X25519 key: the panel wraps the key of the epoch for it, since a
      // pod paired without the phrase has no other way to get it.
      startPodB()
      assert(await waitOnline(POD_B), 'pod B is online at the relay')
      const row = appA.locator(`[data-testid=device-list] tr:has-text("${fpB}")`)
      await row.waitFor({ timeout: 30000 })
      assert((await row.innerText()).includes('wrapped'), 'the key of the epoch is wrapped for pod B: ' + (await row.innerText()).trim())

      // Pod B is opened on its own origin. A passkey cannot be made on an IP address, so this
      // browser has no certificate and takes the random device id it keeps in localStorage: it is a
      // device of its own, different from the admin browser above.
      const tokenB = fs.readFileSync(path.join(DATA_B, 'token'), 'utf8').trim()
      const ctxB = await ctx.browser().newContext({ locale: process.env.E2E_LOCALE ?? 'en-US' })
      const pageB = await ctxB.newPage()
      // The window of pod B is another context: its errors join the ones of the suite.
      pageB.on('pageerror', (e) => errors.push('pageerror B: ' + e.message))
      await pageB.goto(`http://127.0.0.1:${PORT_B}/?token=${tokenB}`)
      const appB = pageB
      await openProject(appB)
      await openSettings(appB)
      await section(appB, 'Remote access')
      await appB.waitForSelector('[data-testid=sync]', { timeout: 20000 })
      assert(await appA.locator('[data-testid=sync] .check:has-text("settings") input').isChecked(), 'settings sync is on by default')
      assert(!(await appB.locator('[data-testid=sync] .check:has-text("vault") input').isChecked()), 'the vault is off by default')

      // 5. A theme changed on A reaches B.
      await section(appA, 'Themes')
      await section(appB, 'Themes')
      await appA.click('.theme-card:has-text("Day")')
      await appB.waitForSelector('.theme-card.active:has-text("Day")', { timeout: 20000 })
      assert(current(DATA_B).theme === 'jour', 'the theme of A is applied on B: ' + JSON.stringify(current(DATA_B)))

      // 6. Two different fields merge without asking: a change made on B reaches A, and no
      // conflict row appears for it.
      await section(appB, 'Fonts')
      const cb = appB.locator('.check:has-text("Ligatures") input')
      assert(await cb.isChecked(), 'the ligatures are on by default')
      await cb.click()   // off: a field B changes while A changes none of the same ones
      await waitCurrent(DATA_B, (s) => s.font?.ligatures === false, 'the ligatures are off on B')
      await section(appA, 'Remote access')
      await waitCurrent(DATA, (s) => s.font?.ligatures === false, 'the ligatures switched off on B reach A')
      assert(!(await appA.locator('[data-testid=sync] .rules tr').count()), 'no conflict for two different fields')

      // 7. Only a true conflict asks. B is stopped first, so A's change is the remote one and B's
      // change is its own: at its next pull B finds the same field changed on both sides.
      await stopPodB()
      appendSettings(DATA_B, { theme: 'contraste' }, 'Theme on B while offline')
      await section(appA, 'Themes')
      await appA.click('.theme-card:has-text("Solarized dark")')
      await sleep(2000)
      startPodB()
      assert(await waitOnline(POD_B), 'pod B is back online')
      await pageB.reload()
      const appB2 = pageB
      await openProject(appB2)
      await openSettings(appB2)
      await section(appB2, 'Remote access')
      await appB2.waitForSelector('[data-testid=sync] .rules tr:has-text("theme")', { timeout: 30000 })
      const conf = (await appB2.textContent('[data-testid=sync] .rules tr:has-text("theme")')).trim()
      assert(conf.includes('contraste') && conf.includes('solarized'), 'the conflict shows both values: ' + conf)
      await appB2.click('[data-testid=sync] .rules tr:has-text("theme") button:has-text("Take the other")')
      await section(appB2, 'Themes')
      await appB2.waitForSelector('.theme-card.active:has-text("Solarized dark")', { timeout: 20000 })
      await appA.waitForSelector('.theme-card.active:has-text("Solarized dark")', { timeout: 20000 })
      assert(current(DATA).theme === 'solarized' && current(DATA_B).theme === 'solarized',
        'the choice made on B is a normal write, taken by A too: ' + current(DATA).theme + ' / ' + current(DATA_B).theme)

      // 8. The layout stays per device: a size chosen on one device never moves the other. The
      // change is recorded on blur, so the input is left before the pod saves it.
      await section(appA, 'Fonts')
      await appA.fill('[data-testid=font-size]', '15')
      await appA.locator('[data-testid=font-size]').evaluate((el) => el.blur())
      await sleep(1500)
      await section(appB2, 'Fonts')
      await appB2.fill('[data-testid=font-size]', '11')
      await appB2.locator('[data-testid=font-size]').evaluate((el) => el.blur())
      await sleep(1500)
      assert(await appA.inputValue('[data-testid=font-size]') === '15', 'A keeps its own size')
      assert(await appB2.inputValue('[data-testid=font-size]') === '11', 'B keeps its own size')
      await appA.click('.settings-body button:has-text("Use as default for new devices")')
      await sleep(2000)
      const devA = await page.evaluate(() => window.identity.device)
      const devB = await pageB.evaluate(() => localStorage.getItem('webide.device'))
      assert(devA && devB && devA !== devB, 'the two browsers are two devices: ' + devA + ' vs ' + devB)
      const dA = current(DATA).devices ?? {}, dB = current(DATA_B).devices ?? {}
      assert(dA[devA]?.font?.size === 15 && dA[devB]?.font?.size === 11, 'both devices are in the settings of A: ' + JSON.stringify(dA))
      assert(dB[devB]?.font?.size === 11 && dB[devA]?.font?.size === 15, 'and in those of B: ' + JSON.stringify(dB))
      assert(dB.fallback?.font?.size === 15, 'the fallback layout is saved for new devices: ' + JSON.stringify(dB.fallback))

      // 9. The vault: nothing is put while it is off; the key reaches B once it is on; the relay
      // base holds no clear text; switching off deletes the blob.
      assert(!py("select kind from blobs").includes('vault'), 'no vault blob while the vault is off')
      await section(appA, 'Remote access')
      await appA.click('[data-testid=sync] .check:has-text("vault") input')
      await sleep(3000)
      assert(py("select kind from blobs").includes('vault'), 'the vault blob appears once switched on')
      assert(fs.readFileSync(path.join(DATA_B, 'llm.json'), 'utf8').includes(KEY), 'the API key reached B through the vault')
      const dump = py('select data from blobs') + fs.readFileSync(path.join(process.env.E2E_RELAY_DATA, 'relay.db')).toString('latin1')
      assert(!dump.includes(KEY) && !dump.includes('solarized') && !dump.includes('contraste'), 'the relay holds no settings nor secrets in clear')
      await appA.click('[data-testid=sync] .check:has-text("vault") input')
      await sleep(3000)
      assert(!py("select kind from blobs").includes('vault'), 'switching the vault off deletes the blob')

      // 10. A revocation moves to a new epoch and the data is encrypted again.
      await section(appA, 'Remote access')
      await appA.click('[data-testid=device-list] tr:has-text("phone") button:has-text("Revoke")')
      await appA.waitForSelector('.toast', { timeout: 20000 })
      const toasts = (await appA.locator('.toast').allInnerTexts()).join(' | ')
      assert(toasts.toLowerCase().includes('renew'), 'the panel suggests renewing the API keys: ' + toasts)
      for (let i = 0; i < 40 && py("select epoch from blobs where kind='settings'") !== '1'; i++) await sleep(500)
      assert(py("select epoch from blobs where kind='settings'") === '1', 'the settings blob is encrypted again at epoch 1')

      await ctxB.close()
      if (errors.length) assert(false, 'page errors:\n    ' + errors.join('\n    '))
    } finally {
      await stopPodB()
    }
  },
  { url: `${ORIGIN}/#pod=${POD_A}` },
)
