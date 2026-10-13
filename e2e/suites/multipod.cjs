// Multi-pod (milestone 3): three pods of one account, alpha and bravo on one site (they are
// linked by the LAN), charlie on another one. run.sh starts the relay and pod alpha with -relay,
// -lan and -preferred; the suite pairs alpha with the CLI, starts bravo (no relay link of its
// own) and charlie (-lan off), then opens the relay origin with no #pod: the bootstrap picks the
// gateway of the site and the home page lists the three pods with their projects. The relay only
// caches ciphertext, so a dump of its base holds no project name.
//
// Relay 3b (routing): every project opens with the front end of the pod that holds it, on the one
// origin of the relay — /pod/<key>/ for a pod registered there, and the same path carried by the
// gateway for a LAN-only one; /_lan/pod/<key>/ on 127.0.0.1, pod to pod over WebRTC for a pod of
// another site. When the gateway leaves, the browser reconnects through the new one, an open
// project keeps working, and notifications enabled on one pod reach every pod of the account.
const fs = require('fs')
const path = require('path')
const http = require('http')
const { spawn, execFileSync } = require('child_process')
const { run, assert, text, pushService } = require('../common.cjs')

const BIN = process.env.E2E_BIN
const DATA = process.env.E2E_DATA
const TMP = process.env.E2E_TMP
const WS = process.env.E2E_WS
const PORT_A = Number(process.env.E2E_PORT)
const RELAY_PORT = Number(process.env.E2E_RELAY_PORT)
const LAN_PORT = Number(process.env.E2E_LAN_PORT)
const ORIGIN = `http://localhost:${RELAY_PORT}`
const TOKEN = process.env.E2E_TOKEN
const STATIC = path.join(__dirname, '../../pod/webdist/dist')
const PHRASE = 'lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper'

// Bravo and charlie take the ports of the block of the suite, from E2E_LAN_PORT + 70 on: a port
// is kept only if nothing answers on it (a suite interrupted before its cleanup leaves its pods
// behind). The block stays below the ephemeral range of the machine, so an outbound socket of a
// pod cannot take a port the suite is about to listen on.
async function portFree(port) {
  const srv = http.createServer()
  const ok = await new Promise((res) => {
    srv.once('error', () => res(false))
    srv.listen(port, '127.0.0.1', () => res(true))
  })
  if (ok) await new Promise((r) => srv.close(r))
  return ok
}
async function pickPort(from) {
  for (let p = from; p < from + 20; p++) if (await portFree(p)) return p
  throw new Error('no free port from ' + from)
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const listen = (srv, port = 0) => new Promise((resolve) => srv.listen(port, '127.0.0.1', () => resolve(srv.address().port)))
async function waitFor(f, ms = 20000) {
  for (const end = Date.now() + ms; Date.now() < end; await sleep(200)) {
    const v = f()
    if (v) return v
  }
  return null
}
const pair = (data, args, input = '') => execFileSync(BIN, ['-data', data, 'pair', ...args], { input, encoding: 'utf8' })
const deviceKey = (data) => / *device *: (\S+)/.exec(pair(data, ['status']))[1]

// The fake push service (run.sh lets its endpoint through with -push-test-endpoint) and the fake
// model server: a browser that enabled notifications on alpha is notified by an agent turn run on
// charlie, another pod of the account.
const push = pushService()
const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
const llm = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  for await (const _ of req);
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  sse(res, { content: 'Turn done.' })
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`)
  res.end('data: [DONE]\n\n')
})

const procs = []
async function stop(proc) {
  if (!proc || proc.exitCode !== null) return
  proc.kill('SIGTERM')
  for (let i = 0; i < 100 && proc.exitCode === null; i++) await sleep(100)
  if (proc.exitCode === null) proc.kill('SIGKILL')
}

/** A pod of its own: its data folder, its workspace with one project <name>-app, paired with the
 * phrase. relay: it registers with the relay; lan: the address of its LAN port (off: none). */
async function startPod(name, data, port, { lan = null, relay = false, protocol = 1, podVersion = '', updateCmd = '' } = {}) {
  const dir = path.dirname(data)
  const ws = path.join(dir, 'ws')
  const app = path.join(ws, name + '-app')
  fs.mkdirSync(data, { recursive: true })
  fs.mkdirSync(app, { recursive: true })
  fs.writeFileSync(path.join(app, 'hello.txt'), `hello from ${name}\n`)
  const now = new Date().toISOString()
  fs.writeFileSync(path.join(data, 'projects.json'), JSON.stringify([{ id: name + '-app', title: name + '-app', description: '', type: 'local', path: app, createdAt: now, openedAt: now }]))
  if (!fs.existsSync(path.join(data, 'device.key'))) pair(data, ['join', '--phrase'], PHRASE + '\n')
  // A pod id of its own: the default is the host name, and the three pods of the suite share it.
  fs.writeFileSync(path.join(data, 'relay.json'), JSON.stringify({ podId: 'pod-' + name, secret: 'sec' + name + '-0000000000000000000000' }))
  const args = ['-addr', `127.0.0.1:${port}`, '-data', data, '-workspace', ws, '-static', STATIC, '-name', name, '-keeper', 'off']
  args.push(lan ? ['-lan', lan, '-mdns=false'] : ['-lan', 'off'])
  if (relay) args.push('-relay', `http://127.0.0.1:${RELAY_PORT}`)
  // A pod started with another multi-pod protocol, and with an install command of its own standing
  // for scripts/install.sh (the suite never installs anything as a service).
  if (protocol !== 1) args.push('-multipod-protocol', String(protocol))
  // A pod that stands for a release: a build of the source tree reports a development version, and
  // no pod installs one of those.
  if (podVersion) args.push('-pod-version', podVersion)
  const log = fs.openSync(path.join(dir, 'pod.log'), 'a')
  const proc = spawn(BIN, args.flat(), { stdio: ['ignore', log, log], env: { ...process.env, ...(updateCmd ? { WEBIDE_UPDATE_CMD: updateCmd } : {}) } })
  procs.push(proc)
  for (let i = 0; i < 150; i++) {
    if (await fetch(`http://127.0.0.1:${port}/`).then(() => true, () => false)) break
    await sleep(100)
  }
  return proc
}

/** Waits until a pod is registered with the relay: the signaling path answers 404 for a pod that
 * is not online, and 400 for a plain request to an online one. */
async function waitOnline(podId) {
  for (let i = 0; i < 60; i++) {
    const code = await new Promise((res) => {
      const req = http.get(`http://127.0.0.1:${RELAY_PORT}/_relay/signal?pod=${podId}&session=probe`, (r) => {
        r.resume()
        res(r.statusCode)
      })
      req.on('error', () => res(0))
      req.setTimeout(3000, () => {
        req.destroy()
        res(0)
      })
    })
    if (code !== 404 && code !== 0) return true
    await sleep(500)
  }
  return false
}

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

/** Opens the bootstrap page and waits for the app iframe or the message the page shows. The hash
 * alone does not reload the page, so the navigation is followed by a reload. */
async function open(page, url) {
  for (let i = 0; i < 8; i++) {
    await page.goto(url, { waitUntil: 'domcontentloaded' })
    await page.reload({ waitUntil: 'domcontentloaded' })
    try {
      await page.waitForSelector('#frame, .err, #pair', { timeout: 20000 })
    } catch {}
    const msg = (await page.textContent('#msg')).trim()
    if (!msg.includes('not online')) return msg
    await sleep(1000)
  }
  return (await page.textContent('#msg')).trim()
}

const appOf = (page) => page.mainFrame().childFrames()[0]

/** Opens a project of another pod from the home page and checks the page really runs on that
 * pod: a file edited here lands on that pod, and a terminal runs in its folder. keep: the
 * terminal is left open (the caller types in it afterwards). */
async function openProject(page, app, name, ws, label, own = false, { keep = false } = {}) {
  // A pod's own projects are the first block of its home page; another pod's are its own block.
  await app.click(own ? `a.project-open:has-text("${name}-app")` : `[data-testid=pod-block][data-name=${name}] [data-testid=pod-project]:has-text("${name}-app")`)
  await app.waitForSelector('.menubar', { timeout: 60000 })
  const row = `.explorer .tree-row[data-path="${path.join(ws, name + '-app', 'hello.txt')}"]`
  await app.waitForSelector(row, { timeout: 30000 })
  await app.dblclick(row)
  await app.waitForSelector('.pane.active .ed-content', { timeout: 30000 })
  assert((await text(app)).startsWith(`hello from ${name}`), `the file comes from ${label}: ` + (await text(app)))
  await app.click('.pane.active .ed-content')
  await page.keyboard.press('Control+End')
  await page.keyboard.type(`edited through ${label}`)
  await page.keyboard.press('Control+s')
  const want = `edited through ${label}`
  let onDisk = ''
  for (let i = 0; i < 80 && !onDisk.includes(want); i++) {
    await sleep(100)
    onDisk = fs.readFileSync(path.join(ws, name + '-app', 'hello.txt'), 'utf8')
  }
  assert(onDisk.includes(want), `the change is saved on ${label}: ` + JSON.stringify(onDisk))
  await openTerm(page, app)
  await page.keyboard.type('pwd\n')
  await app.waitForFunction((d) => document.querySelector('.xterm-rows')?.textContent.includes(d), path.join(ws, name + '-app'), { timeout: 20000 })
  if (!keep) await page.keyboard.type('exit\n')
}

/** A terminal of the active project, ready to type in (by its shortcut or by the button of the
 * console panel: the panel can come up collapsed on a page opened from another pod). */
async function openTerm(page, app) {
  await page.keyboard.press('Control+Shift+Backquote')
  try {
    await app.waitForSelector('.xterm', { state: 'attached', timeout: 8000 })
  } catch {
    await app.click('.console-tabs .icon-btn.small[title^="New terminal"]')
    await app.waitForSelector('.xterm', { state: 'attached', timeout: 20000 })
  }
  await app.waitForTimeout(800)
  // The terminal of a new window is focused already; clicking it only matters when another pane
  // took the focus (and a terminal of a panel that just came up is not clickable yet).
  try {
    await app.click('.xterm', { timeout: 4000 })
  } catch {}
}

/** Types a command in the terminal of the page and waits for its answer. */
async function termRun(page, app, cmd, want) {
  await page.keyboard.type(cmd + '\n')
  await app.waitForFunction((d) => document.querySelector('.xterm-rows')?.textContent.includes(d), want, { timeout: 20000 })
}

/** The pod a channel of this window goes to, as the bootstrap page keeps it. */
const tunnelKeys = (page) => page.evaluate(() => [...(window.tunnels?.keys() ?? [])])

/** The gateway of the site, as the settings panel of a pod shows it. */
async function siteGateway(page) {
  await openPods(page)
  return await page.$eval('[data-testid=pods-settings] .props', (el) => {
    for (const dt of el.querySelectorAll('dt')) {
      if (dt.textContent.includes('Gateway of the site')) return dt.nextElementSibling.textContent.trim()
    }
    return ''
  })
}

/** Opens Settings > Remote access > Pods of the account on the home page of a pod. */
async function openPods(page) {
  await page.click('.home-head button[title="Settings"]')
  await page.waitForSelector('.settings')
  await page.click('.settings-nav button:has-text("Remote access")')
  await page.waitForSelector('[data-testid=pods-settings]', { timeout: 20000 })
}

run(
  async ({ page, ctx, errors }) => {
    let bravo = null
    let charlie = null
    let dataB = ''
    let dataC = ''
    try {
      await push.listen()
      const llmPort = await listen(llm)
      // The subscription of the suite in place of the push service of the browser (headless
      // Chromium cannot reach a real one), before the bootstrap page is opened again.
      await ctx.grantPermissions(['notifications'], { origin: ORIGIN })
      await ctx.addInitScript(({ endpoint, p256dh, auth }) => {
        if (!self.PushManager) return
        const sub = { endpoint, options: {}, toJSON: () => ({ endpoint, keys: { p256dh, auth } }), getKey: () => null, unsubscribe: async () => true }
        let current = null
        PushManager.prototype.subscribe = async () => (current = sub)
        PushManager.prototype.getSubscription = async () => current
      }, { endpoint: push.endpoint, p256dh: push.keys.p256dh, auth: push.keys.auth })

      // 1. Alpha is paired while it runs, and its LAN peers are typed in its config: restart it
      // so it starts paired, with its sync hub and its preferred gateway flag.
      const lanPeerPort = await pickPort(LAN_PORT + 71)
      // join --phrase, not restore: the pod keeps its own device key. Restore would give it the
      // key the phrase derives, which is the one the browser pairing below holds too.
      pair(DATA, ['join', '--phrase'], PHRASE + '\n')
      const now = new Date().toISOString()
      fs.mkdirSync(path.join(WS, 'alpha-app'), { recursive: true })
      fs.writeFileSync(path.join(WS, 'alpha-app', 'hello.txt'), 'hello from alpha\n')
      fs.writeFileSync(path.join(DATA, 'projects.json'), JSON.stringify([{ id: 'alpha-app', title: 'alpha-app', description: '', type: 'local', path: path.join(WS, 'alpha-app'), createdAt: now, openedAt: now }]))
      fs.writeFileSync(path.join(DATA, 'config.json'), JSON.stringify({ lan: { peers: [`127.0.0.1:${lanPeerPort}`] } }))
      // The restart waits for the old pod to let go of its port: relaunching too fast leaves a
      // pod that crashed on "address already in use", and the suite would talk to the old one.
      const alphaPid = Number(fs.readFileSync(path.join(TMP, 'pod.pid'), 'utf8').trim())
      process.kill(alphaPid, 'SIGTERM')
      for (let i = 0; i < 100 && fs.existsSync(`/proc/${alphaPid}`); i++) await sleep(100)
      for (let i = 0; i < 50 && !(await portFree(PORT_A)); i++) await sleep(200)
      execFileSync('bash', [process.env.E2E_RESTART_POD])
      for (let i = 0; i < 50; i++) {
        if (await fetch(`http://127.0.0.1:${PORT_A}/`).then(() => true, () => false)) break
        await sleep(200)
      }
      assert(await waitOnline(process.env.E2E_POD_ID), 'alpha is registered with the relay')

      // 2. Bravo has no link to the relay: only a peer of its site reaches it. Charlie is on
      // another site: it has its relay link and no LAN peer.
      dataB = path.join(TMP, 'mp-bravo', 'data')
      dataC = path.join(TMP, 'mp-charlie', 'data')
      const portB = await pickPort(LAN_PORT + 70)
      const portC = await pickPort(LAN_PORT + 72)
      bravo = await startPod('bravo', dataB, portB, { lan: `127.0.0.1:${lanPeerPort}` })
      charlie = await startPod('charlie', dataC, portC, { relay: true })
      assert(await waitOnline(process.env.E2E_POD_ID), 'alpha is still online')
      const keyB = deviceKey(dataB)

      // 3. A browser pairs with the phrase on the relay origin, with no pod in the link: it
      // enters the account through the gateway of the site, which is alpha (preferred).
      await authenticator(page)
      const msg = await open(page, `${ORIGIN}/`)
      assert(!msg.includes('No pod given'), 'the bootstrap finds a gateway on its own: ' + msg)
      await page.waitForSelector('#pair input', { timeout: 20000 })
      await page.fill('#pair input', PHRASE)
      await page.keyboard.press('Enter')
      await page.waitForSelector('#frame', { timeout: 40000 })
      let app = appOf(page)
      await app.waitForSelector('.home .home-bar', { timeout: 40000 })
      const bar = (await app.textContent('.home-bar')).trim()
      assert(bar.includes(WS), 'the page runs on alpha, the preferred gateway')
      assert((await app.textContent('.home')).includes('alpha-app'), 'the projects of alpha are its own block')

      // The Devices panel is where an admin wraps the key of the epoch for the pods that
      // published their X25519 key: without it no list can be read.
      await app.click('.home-head button[title="Settings"]')
      await app.waitForSelector('.settings')
      await app.click('.settings-nav button:has-text("Remote access")')
      await app.waitForSelector('[data-testid=devices] [data-testid=device-list]', { timeout: 20000 })
      const py = (sql) => execFileSync('python3', ['-c',
        "import sqlite3,sys,os;c=sqlite3.connect(os.environ['E2E_RELAY_DATA']+'/relay.db');print(' | '.join(' '.join(str(x) for x in r) for r in c.execute(sys.argv[1]).fetchall()))", sql], { encoding: 'utf8' })
      const rows = await app.$$eval('[data-testid=devices] [data-testid=device-list] tr', (els) => els.map((e) => e.textContent))
      assert(rows.filter((r) => r.includes('pod')).length === 2 && rows.some((r) => r.includes('browser')), 'the account lists its two pods and this browser: ' + rows.join(' / ').slice(0, 300))

      // 4. One Projects block per pod. Bravo has no link to the relay: it is listed through the
      // LAN link of alpha. Charlie is on another site: its list comes from the cache of the relay,
      // which only ever holds ciphertext — so the block appears once the pod published it.
      await app.waitForSelector('[data-testid=pod-block][data-name=bravo] [data-testid=pod-project]:has-text("bravo-app")', { timeout: 90000 })
      const written = () => py("select count(*) from blobs where kind like 'projects:%'").trim() === '2'
      for (let i = 0; i < 60 && !written(); i++) await sleep(1000)
      assert(written(), 'each pod published its list under its own kind')
      await page.reload({ waitUntil: 'domcontentloaded' })
      await page.waitForSelector('#frame', { timeout: 40000 })
      app = appOf(page)
      await app.waitForSelector('.home .home-bar', { timeout: 40000 })
      await app.waitForSelector('[data-testid=pod-block][data-name=charlie] [data-testid=pod-project]:has-text("charlie-app")', { timeout: 60000 })
      const blocks = await app.$$eval('[data-testid=pod-block]', (els) => els.map((e) => e.dataset.name))
      assert(blocks.includes('bravo') && blocks.includes('charlie'), 'the home page lists the other pods: ' + blocks.join(','))
      const href = await app.getAttribute('[data-testid=pod-block][data-name=bravo] [data-testid=pod-project]', 'href')
      assert(href && href.startsWith('/pod/' + keyB), 'on the relay origin, the page of a pod of the site runs under /pod/: ' + href)

      // 5. Notifications are enabled on this device, on the page of alpha: every pod of the
      // account gets the subscription, not only the one the window shows (a project of charlie
      // will notify this browser, section 8).
      await app.click('.home-head button[title="Settings"]')
      await app.waitForSelector('.settings')
      await app.click('.settings-nav button:has-text("Notifications")')
      await app.waitForSelector('[data-testid=push-enable]', { timeout: 20000 })
      await app.click('[data-testid=push-enable]')
      await app.waitForSelector('[data-testid=push-kind-finished]', { timeout: 20000 })
      await app.click('.modal-settings .modal-head button')

      // 6. Bravo has no link of its own to the relay: its page is carried by the tunnel of its
      // gateway, and the pod at the other end rechecks the browser behind it.
      await openProject(page, app, 'bravo', path.join(TMP, 'mp-bravo', 'ws'), 'alpha')
      const purl = new URL(app.url())
      assert(purl.origin === ORIGIN && purl.pathname === `/pod/${keyB}/project/bravo-app`, 'the project of bravo stays on the relay origin: ' + app.url())
      await page.reload({ waitUntil: 'domcontentloaded' })
      await page.waitForSelector('#frame', { timeout: 40000 })
      app = appOf(page)
      await app.waitForSelector('.home .home-bar', { timeout: 40000 })

      // 7. The relay only ever saw ciphertext: its base holds no project name.
      const dump = fs.readFileSync(process.env.E2E_RELAY_DATA + '/relay.db', 'latin1')
      for (const name of ['alpha-app', 'bravo-app', 'charlie-app'])
        assert(!dump.includes(name), `the relay base holds no project name (${name})`)

      // 8. Charlie stops: its block turns grey, its cached list stays. Started again, it is online.
      await stop(charlie)
      await app.waitForSelector('[data-testid=pod-block][data-name=charlie].offline', { timeout: 30000 })
      assert((await app.textContent('[data-testid=pod-block][data-name=charlie]')).includes('charlie-app'), 'an offline pod keeps its list')
      charlie = await startPod('charlie', dataC, portC, { relay: true })
      await app.waitForSelector('[data-testid=pod-block][data-name=charlie]:not(.offline)', { timeout: 30000 })

      // 9. On 127.0.0.1 of alpha, the same blocks: the LAN pods are listed with the relay down or
      // not, and their page is served under /_lan/pod/.
      const local = await ctx.newPage()
      await local.goto(`http://127.0.0.1:${PORT_A}/?token=${TOKEN}`)
      await local.waitForSelector('.home .home-bar', { timeout: 40000 })
      await local.waitForSelector('[data-testid=pod-block][data-name=bravo] [data-testid=pod-project]:has-text("bravo-app")', { timeout: 30000 })
      await local.waitForSelector('[data-testid=pod-block][data-name=charlie]', { timeout: 30000 })
      const keyC = deviceKey(dataC)
      // Bravo is a peer of the LAN of alpha: its page runs under /_lan/pod/ of alpha. Charlie is
      // of another site: alpha dials it pod to pod over WebRTC, under the same path.
      await openProject(local, local, 'bravo', path.join(TMP, 'mp-bravo', 'ws'), 'alpha')
      assert(new URL(local.url()).pathname === `/_lan/pod/${keyB}/project/bravo-app`, 'the page of bravo runs under /_lan/pod/: ' + local.url())
      await local.goto(`http://127.0.0.1:${PORT_A}/?token=${TOKEN}`)
      await local.waitForSelector('.home .home-bar', { timeout: 40000 })
      await openProject(local, local, 'charlie', path.join(TMP, 'mp-charlie', 'ws'), 'alpha')
      assert(new URL(local.url()).pathname === `/_lan/pod/${keyC}/project/charlie-app`, 'a pod of another site is reached pod to pod: ' + local.url())
      await local.close()

      // 10. Each pod registered at the relay gets its own tunnel: a project of alpha and a project
      // of charlie, each opened under /pod/<key>/ of the relay origin, edited and run there.
      const keyA = deviceKey(DATA)
      await openProject(page, app, 'alpha', WS, 'alpha', true)
      assert(new URL(app.url()).pathname === `/pod/${keyA}/project/alpha-app`, 'the project of alpha runs under its own path: ' + app.url())
      await app.goto(`${ORIGIN}/pod/${keyC}/`, { waitUntil: 'domcontentloaded' })
      await app.waitForSelector('.home .home-bar', { timeout: 40000 })
      assert((await app.textContent('.home-bar')).includes(path.join(TMP, 'mp-charlie', 'ws')), 'the home page of charlie comes from charlie: ' + (await app.textContent('.home-bar')))
      await openProject(page, app, 'charlie', path.join(TMP, 'mp-charlie', 'ws'), 'charlie', true)
      assert(new URL(app.url()).pathname === `/pod/${keyC}/project/charlie-app`, 'the project of charlie runs under its own path: ' + app.url())
      const direct = await tunnelKeys(page)
      for (const k of [keyA, keyC]) assert(direct.includes(k), `${k} has its own tunnel: ` + direct.join(','))

      // 11. An agent turn on charlie, on a window that shows no pod: the browser is notified by
      // charlie, which got the subscription from the fan-out of section 5, not from a panel it
      // ever opened on that pod.
      await app.click('.rail-right .rail-btn[title="AI assistant"]')
      await app.waitForSelector('.ai-empty')
      await app.click('.ai-empty button:has-text("Add a model server")')
      await app.fill('.ai-servers input[name=url]', `127.0.0.1:${llmPort}`)
      await app.click('.ai-servers button:has-text("Add")')
      await app.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
      await app.click('.ai-servers .modal-head button')
      await app.waitForSelector('[data-testid=model-pill]:has-text("fake-model")', { timeout: 20000 })
      await app.evaluate(() => {
        Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
        document.dispatchEvent(new Event('visibilitychange'))
      })
      await app.fill('.ai-composer .ed-content', 'turn on charlie')
      await app.click('[data-testid=send]')
      await app.waitForSelector('.ai-msg.assistant .md:has-text("Turn done.")', { timeout: 30000 })
      const note = await waitFor(() => push.received.find((r) => r.payload?.title === 'The agent finished'))
      assert(note, 'a browser that enabled notifications on alpha is notified by an agent turn of charlie')
      assert(note.payload.body.includes('charlie-app'), 'the notification names the project of the pod that sent it: ' + JSON.stringify(note.payload))
      assert(note.payload.url.startsWith('/#pod=pod-charlie&open=%2Fproject%2F'), 'it opens the conversation on charlie: ' + note.payload.url)
      await app.evaluate(() => {
        Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
        document.dispatchEvent(new Event('visibilitychange'))
      })

      // 12. Update this pod. Charlie is restarted with an older multi-pod protocol and an install
      // command of its own: the home page of the window shows it as an older version, its projects
      // are not openable, and *Update this pod* carries a request signed by the device key of this
      // browser to charlie, which runs the installer it was given.
      const marker = path.join(TMP, 'charlie-installed')
      fs.rmSync(marker, { force: true })
      await stop(charlie)
      charlie = await startPod('charlie', dataC, portC, { relay: true, protocol: 0, updateCmd: 'touch ' + marker })
      assert(await waitOnline('pod-charlie'), 'charlie is registered with the relay')
      await app.goto(`${ORIGIN}/app`, { waitUntil: 'domcontentloaded' })
      await app.waitForSelector('.home .home-bar', { timeout: 40000 })
      const block = '[data-testid=pod-block][data-name=charlie]'
      const badge = await app.waitForSelector(`${block} [data-testid=pod-mismatch]`, { timeout: 40000 })
      assert((await badge.textContent()).includes('older version'), 'charlie runs an older multi-pod protocol: ' + (await badge.textContent()))
      const card = await waitFor(() => app.$(`${block} [data-testid=pod-project]`), 40000)
      assert(card, 'the projects of charlie are listed: ' + (await app.textContent(block)))
      assert((await card.getAttribute('title')).includes('multi-pod protocol'), 'its projects are not openable: ' + (await card.getAttribute('title')))
      assert(!(await app.$(`${block} a.project-open`)), 'no link opens a pod of another protocol')
      await app.click(`${block} [data-testid=pod-update] button`)
      assert(await waitFor(() => (fs.existsSync(marker) ? true : null), 30000), 'charlie ran the installer with the request signed by the device key of the browser')

      // 13. Failover. Bravo takes its own link to the relay, a project of bravo is opened on the
      // page, a terminal runs in it. Then alpha, the gateway this window entered through, is
      // killed: the page of bravo keeps working, and the home page of the window is the new
      // gateway's.
      await stop(bravo)
      bravo = await startPod('bravo', dataB, portB, { lan: `127.0.0.1:${lanPeerPort}`, relay: true })
      assert(await waitOnline('pod-bravo'), 'bravo is registered with the relay')
      const bravoWs = path.join(TMP, 'mp-bravo', 'ws')
      await app.goto(`${ORIGIN}/app`, { waitUntil: 'domcontentloaded' })
      await app.waitForSelector('.home .home-bar', { timeout: 40000 })
      await openProject(page, app, 'bravo', bravoWs, 'bravo', false, { keep: true })
      await termRun(page, app, 'echo before-the-gateway-left', 'before-the-gateway-left')
      const pid = Number(fs.readFileSync(path.join(TMP, 'pod.pid'), 'utf8').trim())
      process.kill(pid, 'SIGTERM')
      for (let i = 0; i < 100 && fs.existsSync(`/proc/${pid}`); i++) await sleep(100)

      // The page of bravo is untouched: the same URL, the same terminal, and a change still
      // saved on bravo.
      const furl = new URL(app.url())
      assert(furl.origin === ORIGIN && furl.pathname === `/pod/${keyB}/project/bravo-app`, 'the project of bravo keeps its URL: ' + app.url())
      await app.click('.pane.active .ed-content')
      await page.keyboard.press('Control+End')
      await page.keyboard.type(' edited after the failover')
      await page.keyboard.press('Control+s')
      let after = ''
      for (let i = 0; i < 80 && !after.includes('edited after the failover'); i++) {
        await sleep(100)
        after = fs.readFileSync(path.join(bravoWs, 'bravo-app', 'hello.txt'), 'utf8')
      }
      assert(after.includes('edited after the failover'), 'the project still edits after the gateway left: ' + JSON.stringify(after))
      try {
        await app.click('.xterm', { timeout: 4000 })
      } catch {}
      await termRun(page, app, 'echo after-the-gateway-left', 'after-the-gateway-left')
      const xrows = await app.textContent('.xterm-rows')
      assert(xrows.includes('before-the-gateway-left') && xrows.includes('after-the-gateway-left'), 'the terminal of the page survived the gateway: ' + xrows.slice(-200))

      // The relay elected a new gateway for the site, and this window followed it: the home page
      // of the origin is bravo's, alpha is greyed.
      let gate = ''
      for (let i = 0; i < 40 && gate !== keyB; i++) {
        await sleep(1000)
        const bp = await ctx.newPage()
        await bp.goto(`http://127.0.0.1:${portB}/?token=${fs.readFileSync(path.join(dataB, 'token'), 'utf8').trim()}`)
        await bp.waitForSelector('.home .home-bar', { timeout: 40000 })
        gate = await siteGateway(bp)
        await bp.close()
      }
      assert(gate === keyB, `bravo is the gateway of the site now: ${gate}`)
      await app.goto(`${ORIGIN}/app`, { waitUntil: 'domcontentloaded' })
      await app.waitForSelector('.home .home-bar', { timeout: 40000 })
      assert((await app.textContent('.home-bar')).includes(bravoWs), 'the window enters through the new gateway: ' + (await app.textContent('.home-bar')))
      assert(!(await app.$('[data-testid=pod-block][data-name=alpha]:not(.offline)')), 'the pod that left is not shown as online')
      const left = await tunnelKeys(page)
      assert(!left.includes(keyA) && left.includes(keyB), 'the tunnel of the gateway that left is gone: ' + left.join(','))

      if (errors.length) assert(false, 'page errors:\n    ' + errors.join('\n    '))
    } finally {
      llm.closeAllConnections()
      llm.close()
      push.server.close()
      for (const p of procs) await stop(p)
      try {
        fs.copyFileSync(process.env.E2E_RELAY_LOG, '/tmp/mp-relay.log')
        fs.copyFileSync(path.join(TMP, 'pod.log'), '/tmp/mp-alpha.log')
        fs.copyFileSync(path.join(DATA, 'config.json'), '/tmp/mp-config.json')
        fs.copyFileSync(path.join(dataC, 'projects.json'), '/tmp/mp-charlie-projects.json')
        fs.copyFileSync(path.join(TMP, 'mp-bravo', 'pod.log'), '/tmp/mp-bravo.log')
        fs.copyFileSync(path.join(TMP, 'mp-charlie', 'pod.log'), '/tmp/mp-charlie.log')
      } catch {}
    }
  },
  { url: `${ORIGIN}/` },
)
