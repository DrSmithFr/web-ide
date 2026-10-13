// Multi-pod (milestone 3): three pods of one account, alpha and bravo on one site (they are
// linked by the LAN), charlie on another one. run.sh starts the relay and pod alpha with -relay,
// -lan and -preferred; the suite pairs alpha with the CLI, starts bravo (no relay link of its
// own) and charlie (-lan off), then opens the relay origin with no #pod: the bootstrap picks the
// gateway of the site and the home page lists the three pods with their projects. The relay only
// caches ciphertext, so a dump of its base holds no project name.
const fs = require('fs')
const path = require('path')
const http = require('http')
const { spawn, execFileSync } = require('child_process')
const { run, assert } = require('../common.cjs')

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
const pair = (data, args, input = '') => execFileSync(BIN, ['-data', data, 'pair', ...args], { input, encoding: 'utf8' })
const deviceKey = (data) => / *device *: (\S+)/.exec(pair(data, ['status']))[1]

const procs = []
async function stop(proc) {
  if (!proc || proc.exitCode !== null) return
  proc.kill('SIGTERM')
  for (let i = 0; i < 100 && proc.exitCode === null; i++) await sleep(100)
  if (proc.exitCode === null) proc.kill('SIGKILL')
}

/** A pod of its own: its data folder, its workspace with one project <name>-app, paired with the
 * phrase. relay: it registers with the relay; lan: the address of its LAN port (off: none). */
async function startPod(name, data, port, { lan = null, relay = false } = {}) {
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
  const log = fs.openSync(path.join(dir, 'pod.log'), 'a')
  const proc = spawn(BIN, args.flat(), { stdio: ['ignore', log, log] })
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
      assert(href && href.includes('/_lan/pod/'), 'the page of a pod of the site runs through the gateway: ' + href)

      // 5. The relay only ever saw ciphertext: its base holds no project name.
      const dump = fs.readFileSync(process.env.E2E_RELAY_DATA + '/relay.db', 'latin1')
      for (const name of ['alpha-app', 'bravo-app', 'charlie-app'])
        assert(!dump.includes(name), `the relay base holds no project name (${name})`)

      // 6. Charlie stops: its block turns grey, its cached list stays. Started again, it is online.
      await stop(charlie)
      await app.waitForSelector('[data-testid=pod-block][data-name=charlie].offline', { timeout: 30000 })
      assert((await app.textContent('[data-testid=pod-block][data-name=charlie]')).includes('charlie-app'), 'an offline pod keeps its list')
      charlie = await startPod('charlie', dataC, portC, { relay: true })
      await app.waitForSelector('[data-testid=pod-block][data-name=charlie]:not(.offline)', { timeout: 30000 })

      // 7. On 127.0.0.1 of alpha, the same blocks: the LAN pods are listed with the relay down or
      // not, and their page is served under /_lan/pod/.
      const local = await ctx.newPage()
      await local.goto(`http://127.0.0.1:${PORT_A}/?token=${TOKEN}`)
      await local.waitForSelector('.home .home-bar', { timeout: 40000 })
      await local.waitForSelector('[data-testid=pod-block][data-name=bravo] [data-testid=pod-project]:has-text("bravo-app")', { timeout: 30000 })
      await local.waitForSelector('[data-testid=pod-block][data-name=charlie]', { timeout: 30000 })
      await local.close()

      // 8. Election: alpha leaves, bravo (which now has its own link) takes the role.
      const pid = Number(fs.readFileSync(path.join(TMP, 'pod.pid'), 'utf8').trim())
      process.kill(pid, 'SIGTERM')
      for (let i = 0; i < 100 && fs.existsSync(`/proc/${pid}`); i++) await sleep(100)
      await stop(bravo)
      bravo = await startPod('bravo', dataB, portB, { lan: `127.0.0.1:${lanPeerPort}`, relay: true })
      // The grace of the site, then the new gateway: bravo says it is the entry point.
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

      if (errors.length) assert(false, 'page errors:\n    ' + errors.join('\n    '))
    } finally {
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
