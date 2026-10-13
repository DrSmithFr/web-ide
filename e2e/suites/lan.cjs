// LAN (#50): the pods of one account reach each other on the local network with no relay.
// run.sh starts pod "alpha" with its LAN port on loopback and no mDNS (the addresses are
// typed in Settings); the suite pairs it with the CLI, starts four more pods on loopback —
// bravo and echo joined with the phrase, charlie unpaired, delta of another account — and
// checks the home page, a revocation made offline that spreads from pod to pod, foxtrot
// joining over the LAN through the Devices panel, and a project of bravo opened through the
// gateway. No relay process runs.
const fs = require('fs')
const path = require('path')
const { spawn, execFileSync } = require('child_process')
const { run, assert, open, text } = require('../common.cjs')

const BIN = process.env.E2E_BIN
const DATA = process.env.E2E_DATA
const TMP = process.env.E2E_TMP
const LAN_PORT = Number(process.env.E2E_LAN_PORT)
const STATIC = path.join(__dirname, '../../pod/webdist/dist')
const PHRASE = 'lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper'
const OTHER = 'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const pair = (data, args, input = '') => execFileSync(BIN, ['-data', data, 'pair', ...args], { input, encoding: 'utf8' })

const pods = []

/** Starts a pod on loopback with a project <name>-app holding hello.txt. */
async function startPod(name, n, mode) {
  const dir = path.join(TMP, 'lan-' + name)
  const data = path.join(dir, 'data')
  const ws = path.join(dir, 'ws')
  const app = path.join(ws, name + '-app')
  fs.mkdirSync(data, { recursive: true })
  fs.mkdirSync(app, { recursive: true })
  fs.writeFileSync(path.join(app, 'hello.txt'), `hello from ${name}\n`)
  const now = new Date().toISOString()
  fs.writeFileSync(path.join(data, 'projects.json'), JSON.stringify([{ id: name + '-app', title: name + '-app', description: '', type: 'local', path: app, createdAt: now, openedAt: now }]))
  if (mode === 'join') execFileSync(BIN, ['-data', data, '-name', name, 'pair', 'join', '--phrase'], { input: PHRASE + '\n' })
  if (mode === 'foreign') execFileSync(BIN, ['-data', data, '-name', name, 'pair', 'restore'], { input: OTHER + '\n' })
  const port = LAN_PORT + 10 + 2 * n
  const log = fs.openSync(path.join(dir, 'pod.log'), 'a')
  const proc = spawn(BIN, ['-addr', `127.0.0.1:${port}`, '-data', data, '-workspace', ws, '-static', STATIC, '-lan', `127.0.0.1:${port + 1}`, '-mdns=false', '-name', name, '-keeper', 'off'], {
    stdio: ['ignore', log, log],
  })
  pods.push(proc)
  for (let i = 0; i < 100; i++) {
    if (await fetch(`http://127.0.0.1:${port}/`).then(() => true, () => false)) break
    await sleep(100)
  }
  return { name, data, ws, lan: `127.0.0.1:${port + 1}` }
}

const VA = {
  protocol: 'ctap2', ctap2Version: 'ctap2_1', transport: 'internal',
  hasResidentKey: true, hasUserVerification: true, isUserVerified: true,
  hasPrf: true, automaticPresenceSimulation: true,
}

/** pair join with no relay: waits on its LAN port until the code is printed. */
function joinLAN(name, port) {
  const dir = path.join(TMP, 'lan-' + name)
  const data = path.join(dir, 'data')
  fs.mkdirSync(data, { recursive: true })
  const proc = spawn(BIN, ['-data', data, '-lan', `127.0.0.1:${port}`, '-mdns=false', '-name', name, 'pair', 'join'], { stdio: ['ignore', 'pipe', 'pipe'] })
  pods.push(proc)
  let out = ''
  const exited = new Promise((r) => proc.on('exit', (code) => r(code)))
  const printed = new Promise((resolve, reject) => {
    proc.stdout.on('data', (b) => {
      out += b
      const code = /Pairing code: (\S+)/.exec(out)
      const fp = /Fingerprint of this device: (.+)/.exec(out)
      if (code && fp) resolve({ code: code[1], fp: fp[1].trim() })
    })
    setTimeout(() => reject(new Error('pair join printed nothing: ' + out)), 10000)
  })
  return { data, lan: `127.0.0.1:${port}`, printed, exited, out: () => out }
}

const deviceKey = (data) => / *device *: (\S+)/.exec(pair(data, ['status']))[1]

run(async ({ page }) => {
  try {
    // 1. alpha is paired by the CLI while it runs: it opens its LAN port.
    pair(DATA, ['restore'], PHRASE + '\n')
    const bravo = await startPod('bravo', 0, 'join')
    const charlie = await startPod('charlie', 1, 'none')
    const delta = await startPod('delta', 2, 'foreign')
    const echo = await startPod('echo', 3, 'join')

    // 2. The addresses are typed in Settings > Remote access > Local network.
    await page.waitForSelector('.home .home-bar')
    await page.click('.home-head button[title="Settings"]')
    await page.click('.settings-nav >> text=Remote access')
    await page.waitForFunction((p) => document.querySelector('[data-testid=lan-settings]')?.textContent.includes('127.0.0.1:' + p), LAN_PORT, { timeout: 15000 })
    assert(true, 'alpha opened its LAN port once paired by the CLI')
    await page.fill('[data-testid=lan-manual]', [bravo, charlie, delta, echo].map((p) => p.lan).join('\n'))
    await page.click('[data-testid=lan-save]')
    await page.waitForFunction(() => document.querySelectorAll('[data-testid=lan-peer] .dot-connected').length === 2, null, { timeout: 20000 })
    const peers = await page.$$eval('[data-testid=lan-peer]', (els) => els.map((e) => e.textContent))
    assert(peers.some((p) => p.includes('bravo')) && peers.some((p) => p.includes('echo')), 'bravo and echo are reached: ' + peers.join(' | '))
    await page.waitForFunction(
      ([d]) => [...document.querySelectorAll('[data-testid=lan-failed]')].some((e) => e.textContent.includes(d) && e.textContent.includes('another account')),
      [delta.lan],
      { timeout: 15000 },
    )
    const failedList = await page.$$eval('[data-testid=lan-failed]', (els) => els.map((e) => e.textContent).join(' | '))
    assert(failedList.includes(charlie.lan) && failedList.includes('no answer'), 'the unpaired pod opens no LAN port: ' + failedList)
    assert(failedList.includes(delta.lan), 'the pod of another account is refused: ' + failedList)
    const saved = JSON.parse(fs.readFileSync(path.join(DATA, 'config.json'), 'utf8'))
    assert(saved.lan?.peers?.length === 4, 'the addresses are saved in config.json: ' + JSON.stringify(saved.lan))
    await page.click('.modal-head button[title^="Close"]')
    await page.waitForSelector('.modal', { state: 'detached' })

    // 3. The home page shows one block per pod of the account, with its projects.
    await page.waitForSelector('[data-testid=pod-block][data-name=bravo] [data-testid=pod-project]:has-text("bravo-app")', { timeout: 15000 })
    await page.waitForSelector('[data-testid=pod-block][data-name=echo] [data-testid=pod-project]:has-text("echo-app")')
    const names = await page.$$eval('[data-testid=pod-block]', (els) => els.map((e) => e.dataset.name))
    assert(names.sort().join() === 'bravo,echo', 'only the pods of the account have a block: ' + names)

    // 4. echo is revoked offline (phrase typed): alpha drops it, and bravo gets the list
    // from alpha, with no relay.
    const out = pair(DATA, ['revoke', deviceKey(echo.data)], PHRASE + '\n')
    assert(out.includes('revocations : seq 1'), 'pair revoke signs the list: ' + out.replace(/\n/g, ' | '))
    await page.waitForFunction(() => !document.querySelector('[data-testid=pod-block][data-name=echo]'), null, { timeout: 15000 })
    assert(true, 'the revoked pod leaves the home page')
    let bravoStatus = ''
    for (let i = 0; i < 40 && !bravoStatus.includes('revocations'); i++) {
      await sleep(250)
      bravoStatus = pair(bravo.data, ['status'])
    }
    assert(bravoStatus.includes('revocations : seq 1, epoch 1, 1 device(s)'), 'bravo got the list over the LAN: ' + bravoStatus.replace(/\n/g, ' | '))

    // 5. foxtrot joins on the LAN with no relay: an admin browser on localhost (a passkey
    // cannot be made on 127.0.0.1) sees its request in Devices and approves it.
    const fox = joinLAN('foxtrot', LAN_PORT + 30)
    const req = await fox.printed
    const admin = await page.context().newPage()
    const cdp = await page.context().newCDPSession(admin)
    await cdp.send('WebAuthn.enable')
    await cdp.send('WebAuthn.addVirtualAuthenticator', { options: VA })
    await admin.goto(`${process.env.E2E_URL.replace('127.0.0.1', 'localhost')}/?token=${process.env.E2E_TOKEN}`)
    await admin.waitForSelector('.home .home-bar')
    await admin.click('.home-head button[title="Settings"]')
    await admin.click('.settings-nav >> text=Remote access')
    await admin.waitForSelector('[data-testid=lan-manual]')
    const typed = await admin.inputValue('[data-testid=lan-manual]')
    await admin.fill('[data-testid=lan-manual]', typed + '\n' + fox.lan)
    await admin.click('[data-testid=lan-save]')
    await admin.fill('[data-testid=devices] input', PHRASE)
    await admin.click('[data-testid=devices] button:has-text("Pair this device as an admin")')
    await admin.waitForSelector(`[data-testid=devices] .rules tr:has-text("${req.code}")`, { timeout: 20000 })
    const row = await admin.textContent(`[data-testid=devices] .rules tr:has-text("${req.code}")`)
    assert(row.includes(req.fp) && row.includes('foxtrot'), 'the panel shows the request of foxtrot with its fingerprint: ' + row)
    const listed = await admin.textContent('[data-testid=devices] [data-testid=device-list]')
    assert(listed.includes('bravo') && !listed.includes('echo'), 'the panel lists the devices the pod knows, without the revoked one: ' + listed.trim().slice(0, 200))
    await admin.click(`[data-testid=devices] .rules tr:has-text("${req.code}") button:has-text("Approve")`)
    const code = await Promise.race([fox.exited, sleep(15000).then(() => 'timeout')])
    assert(code === 0 && fox.out().includes('Paired with account'), 'foxtrot is paired over the LAN: ' + fox.out().replace(/\n/g, ' | '))
    assert(pair(fox.data, ['status']).includes('certificate : foxtrot, pod'), 'foxtrot holds its certificate')
    await admin.close()

    // 6. A project of bravo opens through alpha, under /_lan/pod/<key>/ of its origin: its
    // page comes from bravo.
    const home = page.url()
    await page.click('[data-testid=pod-block][data-name=bravo] [data-testid=pod-project]')
    await page.waitForSelector('.menubar', { timeout: 20000 })
    const url = new URL(page.url())
    const prefix = url.pathname.match(/^\/_lan\/pod\/[\w-]+/)?.[0]
    assert(url.origin === new URL(home).origin && prefix && url.pathname === prefix + '/project/bravo-app', 'the project opens under the path of bravo on alpha: ' + page.url())
    await open(page, 'hello.txt')
    assert((await text(page)).startsWith('hello from bravo'), 'the file comes from bravo: ' + (await text(page)))
    await page.click('.pane.active .ed-content')
    await page.keyboard.press('Control+End')
    await page.keyboard.type('edited through alpha')
    await page.keyboard.press('Control+s')
    let onDisk = ''
    for (let i = 0; i < 40 && !onDisk.includes('edited through alpha'); i++) {
      await sleep(100)
      onDisk = fs.readFileSync(path.join(bravo.ws, 'bravo-app', 'hello.txt'), 'utf8')
    }
    assert(onDisk.includes('edited through alpha'), 'the change is saved on bravo: ' + JSON.stringify(onDisk))

    // A terminal of the project runs on bravo.
    await page.keyboard.press('Control+Shift+Backquote')
    await page.waitForSelector('.xterm', { timeout: 5000 })
    await page.waitForTimeout(800)
    await page.keyboard.type('pwd\n')
    await page.waitForFunction((d) => document.querySelector('.xterm-rows')?.textContent.includes(d), path.join(bravo.ws, 'bravo-app'), { timeout: 5000 })
    assert(true, 'a terminal runs in the folder of bravo')

    // 7. The home page of bravo, through the gateway, leads back to alpha.
    await page.goto(`${url.origin}${prefix}/`)
    await page.waitForSelector('[data-testid=lan-back]')
    assert((await page.textContent('[data-testid=lan-back]')).includes('alpha'), 'the page of bravo links back to alpha')
    assert(!(await page.$('[data-testid=pod-block]')), 'the page of bravo shows no LAN blocks of its own')
    await page.click('[data-testid=lan-back]')
    await page.waitForSelector('[data-testid=pod-block][data-name=bravo]')
    assert(new URL(page.url()).pathname === '/', 'back on the home page of alpha')
  } finally {
    for (const p of pods) p.kill()
    // The logs of the extra pods, next to the screenshot of a failure.
    for (const n of ['bravo', 'charlie', 'delta', 'echo', 'foxtrot']) {
      try {
        fs.copyFileSync(path.join(TMP, 'lan-' + n, 'pod.log'), path.join(process.env.E2E_OUT, 'lan-' + n + '.log'))
      } catch {}
    }
  }
})
