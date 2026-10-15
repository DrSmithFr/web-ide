// Relay (milestone 1): a browser on another network opens a project of a pod through the
// relay, and installs as an app (PWA). run.sh starts the relay and the pod with -relay and
// -force-turn, so the selected candidate pair is a TURN relay one. The app is loaded from the
// pod through the tunnel (Service Worker), the token is proved inside the DataChannel,
// terminals and big transfers have their own channel, previews are served through the Service
// Worker; a wrong token is refused; 127.0.0.1 still works with the same pod.
const fs = require('fs')
const http = require('http')
const net = require('net')
const { run, assert, WS, OUT } = require('../common.cjs')

const RELAY = process.env.E2E_RELAY_URL
const POD_ID = process.env.E2E_POD_ID
const TOKEN = process.env.E2E_TOKEN
const link = (t) => `${RELAY}/#pod=${POD_ID}&t=${t}`

const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const call = (id, name, args) => ({ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } })

let appPort = 0
const command = () => `node -e "require('http').createServer((q, s) => s.end('preview app ' + q.headers.host + ' ' + (q.headers.cookie || '-'))).listen(${appPort}, '127.0.0.1')"`

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' }, caps: { vision: true } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 }, modalities: { vision: true } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  const last = r.messages[r.messages.length - 1]
  const content = typeof last.content === 'string' ? last.content : (last.content ?? []).map((p) => p.text ?? '').join(' ')
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  if (content.startsWith('Attach')) return sse(res, { tool_calls: [call('p1', 'share_preview', { title: 'Demo app', command: command(), port: appPort })] }), end(res, 'tool_calls')
  const hasImage = typeof last.content === 'object' && (last.content ?? []).some((p) => p.type === 'image_url')
  sse(res, { content: hasImage ? 'I see an image.' : 'Answered through the tunnel.' })
  return end(res)
})

const listen = (srv) => new Promise((resolve) => srv.listen(0, '127.0.0.1', () => resolve(srv.address().port)))
const freePort = async () => {
  const s = net.createServer()
  const p = await listen(s)
  await new Promise((r) => s.close(r))
  return p
}

run(
  async ({ page, ctx, errors }) => {
    await listen(fake)
    appPort = await freePort()

    // The app is served by the Service Worker of the relay, from the pod through the tunnel.
    await page.waitForSelector('#frame', { timeout: 30000 })
    const stats = await page.evaluate(async () => {
      const r = await window.tunnel.pc.getStats()
      const by = new Map([...r.values()].map((s) => [s.id, s]))
      return [...r.values()]
        .filter((s) => s.type === 'candidate-pair' && s.state === 'succeeded')
        .map((p) => (by.get(p.localCandidateId)?.candidateType ?? '?') + '/' + (by.get(p.remoteCandidateId)?.candidateType ?? '?'))
        .join(' ')
    })
    assert(stats.includes('relay'), 'the app is loaded through the tunnel, TURN pair: ' + stats)
    // The bootstrap page is an installable app (PWA): the manifest and the icons come from the
    // relay. Chromium reports nothing but the incognito context of the tests.
    const cdp = await ctx.newCDPSession(page)
    const { installabilityErrors } = await cdp.send('Page.getInstallabilityErrors')
    const pwa = installabilityErrors.map((e) => e.errorId).filter((id) => id !== 'in-incognito')
    assert(pwa.length === 0, 'the bootstrap page is installable: ' + pwa.join(', '))
    // The pages of the relay and of the pod open on phones too: their viewport never zooms.
    const bootMeta = await page.getAttribute('meta[name=viewport]', 'content')
    assert(bootMeta.includes('maximum-scale=1'), 'the bootstrap page does not zoom: ' + bootMeta)
    assert((await page.evaluate(() => getComputedStyle(document.documentElement).touchAction)) === 'manipulation', 'the bootstrap page refuses pinch and double-tap zoom')

    // The app window is the iframe of the bootstrap page; the keyboard of the page types
    // into the element focused inside it.
    const app = page.mainFrame().childFrames()[0]
    await app.waitForSelector('.home .home-bar', { timeout: 30000 })
    await app.waitForSelector('.project-list .project-card, .ws-dir')
    if (await app.locator('.project-open:has-text("demo")').count()) await app.click('.project-open:has-text("demo")')
    else await app.click('.ws-dir:has-text("demo")')
    await app.waitForSelector('.menubar')
    await app.waitForTimeout(400)

    // What the first load cost, phase by phase (#71): the e2e report prints it, so a slow
    // reload can be read where it is paid (the tunnel, the identity link, the assets).
    const first = await page.evaluate(() => ({
      ...window.bootTimings,
      channels: window.httpChannels,
      bytes: window.httpBytes,
      total: Math.round(performance.now()),
    }))
    console.log('first load (ms):', JSON.stringify(first))
    // The journal runs to the end of the load (#75): the app tells its start, its first message
    // from the pod and its home page, and the journal stays readable once the app is open.
    const boot = await page.evaluate(() => window.bootJournal)
    for (const line of ['Signaling on the identity link', 'Opening the IDE', 'The IDE starts', 'The IDE talks to the pod', 'Home page shown'])
      assert(boot.includes(line), `the journal of the load says "${line}":\n${boot}`)
    console.log(boot)
    assert(first.app > 0 && first.ready !== undefined && first.channels > 0, 'the phases of the load are measured: ' + JSON.stringify(first))

    // Editing: typing and saving go through the rpc channel, the file lands on disk.
    await page.keyboard.press('Control+Shift+n')
    await app.waitForSelector('.pick-input')
    await page.keyboard.type('notes.txt')
    await app.waitForFunction(() => document.querySelector('.pick-item.selected')?.textContent.includes('notes.txt'))
    await page.keyboard.press('Enter')
    await app.waitForSelector('.pane.active .ed-content')
    await app.click('.pane.active .ed-content')
    await page.keyboard.press('Control+Home')
    await page.keyboard.type('// via the relay ')
    await page.keyboard.press('Control+s')
    await app.waitForTimeout(600)
    assert(fs.readFileSync(WS + '/demo/notes.txt', 'utf8').startsWith('// via the relay'), 'edit saved to disk through the tunnel')

    // Terminal: its own channel (term:<id>).
    await app.focus('.pane.active .ed-content')
    await page.keyboard.press('Control+Shift+Backquote')
    await app.waitForSelector('.xterm', { timeout: 15000 })
    await page.keyboard.type('echo relay-$((6*7))\n')
    await app.waitForTimeout(1200)
    assert((await app.textContent('.xterm-rows')).includes('relay-42'), 'terminal echo through its channel')

    // A big file: over the size of one message, so the request goes on xfer:<n>. It is
    // created by the terminal, so the pod knows it (a file written from outside would not
    // be in the picker list).
    const big = 'x'.repeat(1500000)
    await page.keyboard.type('yes x | head -c 1500000 > big.txt\n')
    for (let i = 0; i < 60 && !fs.existsSync(WS + '/demo/big.txt'); i++) await page.waitForTimeout(500)
    assert(fs.readFileSync(WS + '/demo/big.txt', 'utf8').length === big.length, 'the terminal wrote 1.5 MB on disk')
    await app.waitForSelector(`.explorer .tree-row[data-path="${WS}/demo/big.txt"]`, { timeout: 15000 })
    await app.dblclick(`.explorer .tree-row[data-path="${WS}/demo/big.txt"]`)
    await app.waitForSelector('.pane.active .tab.active:has-text("big.txt")')
    await app.waitForSelector('.pane.active .ed-content')
    await app.waitForFunction(() => document.querySelector('.pane.active .ed-content').textContent.length > 100000, null, { timeout: 20000 })
    assert((await app.textContent('.pane.active .ed-content')).replace(/\n$/, '').length === big.length, '1.5 MB file read on its own channel')
    await app.click('.pane.active .ed-content')
    await page.keyboard.press('Control+Home')
    await page.keyboard.type('saved ')
    await page.keyboard.press('Control+s')
    let saved = ''
    for (let i = 0; i < 20 && !saved.startsWith('saved '); i++) {
      await app.waitForTimeout(500)
      saved = fs.readFileSync(WS + '/demo/big.txt', 'utf8')
    }
    assert(saved.startsWith('saved ') && saved.length === big.length + 6, '1.5 MB file saved through the tunnel')

    // Assistant: an attachment and an answer, over the tunnel.
    await app.click('.rail-right .rail-btn[title="AI assistant"]')
    await app.waitForSelector('.ai-empty')
    await app.click('.ai-empty button:has-text("Add a model server")')
    await app.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await app.click('.ai-servers button:has-text("Add")')
    await app.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await app.click('.ai-servers .modal-head button')
    await app.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGP8z8DwnwEIGBkZGRgYAAAiBgID0ZfYpQAAAABJRU5ErkJggg==', 'base64')
    await app.setInputFiles('.ai-composer input[type=file]', { name: 'p.png', mimeType: 'image/png', buffer: png })
    await app.waitForSelector('.ai-composer .ai-att:has-text("p.png")')
    await app.fill('.ai-composer .ed-content', 'What do you see?')
    await app.click('[data-testid=send]')
    await app.waitForSelector('.ai-msg.assistant .md:has-text("I see an image.")', { timeout: 20000 })
    assert(await app.isVisible('.ai-msg.user .ai-att img'), 'assistant attachment sent through the tunnel')

    // Preview: the app of the pod is reached through the Service Worker of the relay.
    await app.fill('.ai-composer .ed-content', 'Attach the app')
    await app.click('[data-testid=send]')
    await app.waitForSelector('[data-testid=ai-preview]', { timeout: 20000 })
    const [tab] = await Promise.all([ctx.waitForEvent('page'), app.click('[data-testid=ai-preview-open]')])
    await tab.waitForFunction(() => document.body?.textContent.startsWith('preview app'), null, { timeout: 30000 })
    assert(tab.url().startsWith(`${RELAY}/__preview/`), 'the preview is opened through the Service Worker: ' + tab.url())
    assert((await tab.textContent('body')).includes('webide_token='), 'the preview app sees the cookie of the IDE through the tunnel')
    await tab.close()

    // Settings > Remote access: the relay, the pod id, the state of the link.
    await app.click('.menubar button[title^="Settings"]')
    await app.waitForSelector('.settings')
    await app.click('.settings-nav button:has-text("Remote access")')
    await app.waitForSelector('.settings-body .props')
    const remote = await app.textContent('.settings-body .props')
    assert(remote.includes(RELAY) && remote.includes(POD_ID), 'Settings shows the relay and the pod id: ' + remote.trim().slice(0, 120))
    assert(await app.isVisible('.settings-body .dot-connected'), 'the link to the relay is connected')
    assert((await app.inputValue('[data-testid=relay-url]')) === RELAY, 'Settings > Remote access lets the relay URL be edited: ' + (await app.inputValue('[data-testid=relay-url]')))
    assert(await app.isVisible('[data-testid=relay-off]'), 'a connected pod offers to turn remote access off')
    assert((await app.textContent('.settings-body dd.mono.small')).includes(`#pod=${POD_ID}&t=${TOKEN}`), 'the remote link shown in Settings')
    await app.click('[data-testid=connection-journal] summary')
    await app.waitForFunction(() => document.querySelector('[data-testid=connection-journal] pre')?.textContent.includes('Home page shown'))
    assert(true, 'Settings > Remote access shows the journal of the connection')
    await app.click('.modal-settings .modal-head button')

    // The shell of the relay is kept by the Service Worker (#73): the bootstrap page, its scripts,
    // the identity module, the wordlist and the installable app, under the hash the relay wrote
    // into sw.js.
    const shellPaths = ['/', '/_relay/bootstrap.js', '/_relay/bootstrap.css', '/_relay/routes.js', '/_relay/identity.js', '/_relay/bip39.txt', '/manifest.webmanifest']
    const shell = await page.evaluate(async () => {
      const names = (await caches.keys()).filter((n) => n.startsWith('webide-shell:'))
      const paths = names.length ? (await (await caches.open(names[0])).keys()).map((r) => new URL(r.url).pathname) : []
      return { names, paths }
    })
    assert(shell.names.length === 1 && /^webide-shell:[0-9a-f]{16}$/.test(shell.names[0]), 'the shell has one cache, named by its hash: ' + shell.names.join(', '))
    assert(shellPaths.every((p) => shell.paths.includes(p)), 'the shell is cached: ' + shell.paths.join(' '))

    // The assets the pod says are immutable are kept by the Service Worker (#71): a reload of the
    // page answers them from this origin instead of carrying them through the tunnel. The page of
    // the pod is kept too, under the build its handshake told (#73). /api and the previews are
    // never cached.
    const listed = async () =>
      await page.evaluate(async () => {
        const names = (await caches.keys()).filter((n) => n.startsWith('webide-assets'))
        const urls = []
        for (const n of names) {
          const c = await caches.open(n)
          for (const k of await c.keys()) {
            if (k.url.includes('/_relay/assets-created')) continue // the marker of the cache itself
            const r = await c.match(k)
            urls.push({ url: k.url, size: Number(r?.headers.get('content-length') ?? 0) })
          }
        }
        return { names, urls }
      })
    const warm = await listed()
    const assets = warm.urls.filter((u) => u.url.includes("/assets/"))
    assert(warm.names.length === 1 && warm.names[0].startsWith('webide-assets:' + POD_ID), 'the pod has its own asset cache: ' + warm.names.join(', '))
    assert(/:[0-9a-f]{16}$/.test(warm.names[0]), 'the cache of the pod is named by its build: ' + warm.names[0])
    assert(assets.some((u) => u.url.includes('/assets/index-')), `${assets.length} assets of the app are cached`)
    assert(warm.urls.some((u) => new URL(u.url).pathname === '/app'), 'the page of the pod is cached under its build')
    assert(!warm.urls.some((u) => u.url.includes("/api/") || u.url.includes("/__preview/")), 'no api nor preview in the cache')
    assert(warm.urls.every((u) => u.size <= 8 * 1024 * 1024), 'every cached entry stays under the cap')

    // A reload of the page: the same app, far fewer channels and far fewer bytes through the tunnel,
    // and the shell answered by the Service Worker, never by the relay (#73).
    const fromNet = []
    const onResponse = (r) => {
      const u = new URL(r.url())
      if (u.origin === RELAY && shellPaths.includes(u.pathname) && !r.fromServiceWorker()) fromNet.push(u.pathname)
    }
    page.on('response', onResponse)
    const t0 = Date.now()
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.waitForSelector('#frame', { timeout: 30000 })
    const again = page.mainFrame().childFrames()[0]
    await again.waitForSelector('.home .home-bar', { timeout: 30000 })
    await again.waitForSelector('.project-list .project-card, .ws-dir')
    await again.waitForTimeout(400)
    page.off('response', onResponse)
    const re = await page.evaluate(() => ({ ...window.bootTimings, channels: window.httpChannels, bytes: window.httpBytes, paths: window.httpPaths, total: Math.round(performance.now()) }))
    console.log(`  load first:  ${JSON.stringify(first)}`)
    console.log(`  load reload: ${JSON.stringify(re)} (${Date.now() - t0} ms to the app)`)
    assert(re.channels < first.channels, `a reload opens fewer tunnel channels: ${first.channels} → ${re.channels}`)
    // One WebSocket (#75): the identity link opens beside the device, and the offer leaves on it
    // as soon as both are ready.
    assert(re.link <= re.device + 5, `the identity link opens beside the device: device ${re.device} ms, link ${re.link} ms`)
    assert(re.signal <= Math.max(re.link, re.device) + 5, `the signaling starts once the link and the device are ready: link ${re.link} ms, device ${re.device} ms, signal ${re.signal} ms`)
    assert(re.bytes * 2 < first.bytes, `a reload carries far fewer bytes through the tunnel: ${first.bytes} → ${re.bytes}`)
    assert(fromNet.length === 0, 'a reload fetches nothing of the shell from the relay: ' + fromNet.join(' '))
    assert(!re.paths.some((p) => p === '/app' || p.includes('/assets/')), 'a reload carries no page nor asset through the tunnel: ' + re.paths.join(' '))

    // The app works after the reload: the project is opened and its file read again.
    if (await again.locator('.project-open:has-text("demo")').count()) await again.click('.project-open:has-text("demo")')
    else await again.click('.ws-dir:has-text("demo")')
    await again.waitForSelector('.menubar')
    await again.waitForSelector(`.explorer .tree-row[data-path="${WS}/demo/notes.txt"]`, { timeout: 15000 })
    await again.dblclick(`.explorer .tree-row[data-path="${WS}/demo/notes.txt"]`)
    await again.waitForSelector('.pane.active .tab.active:has-text("notes.txt")')
    assert((await again.textContent('.pane.active .ed-content')).includes('via the relay'), 'the app works after the reload')

    // A pod updated to another version leaves its old cache behind: the Service Worker drops it
    // when the page declares the new one, and the next asset lands in the cache of that version.
    const oldName = warm.names[0]
    const oneAsset = warm.urls.find((u) => u.url.includes('/assets/')).url
    await page.evaluate((pod) => navigator.serviceWorker.controller.postMessage({ kind: 'own', pod, key: '', keys: [], version: 'v9.9.9' }), POD_ID)
    await page.waitForTimeout(600)
    const moved = await page.evaluate(async (u) => {
      await fetch(u)
      return (await caches.keys()).filter((n) => n.startsWith('webide-assets'))
    }, oneAsset)
    assert(!moved.includes(oldName) && moved.length === 1 && moved[0].endsWith(':v9.9.9'), 'the cache of the old version is dropped: ' + moved.join(', '))

    // A wrong token: the pod refuses the proof (the relay never sees the token).
    const bad = await ctx.newPage()
    await bad.goto(link('deadbeefdeadbeefdeadbeefdeadbeefdeadbeef'), { waitUntil: 'domcontentloaded' })
    await bad.waitForSelector('.err', { timeout: 30000 })
    assert((await bad.textContent('.err')).includes('connection could not be established'), 'a wrong token is refused by the pod')
    // The Details panel tells what the connection did, step by step (#74): the tunnel came up
    // through TURN, and the pod refused the proof.
    assert(await bad.isVisible('#trace-lines'), 'a failed connection shows its Details panel, open')
    const journal = await bad.textContent('#trace-lines')
    for (const line of ['Signaling on the identity link', 'TURN credentials received', 'Answer of the pod received', 'ICE: connected', 'Path relayed by TURN', 'Error: '])
      assert(journal.includes(line), `the journal says "${line}":\n${journal}`)
    assert(!journal.includes(TOKEN) && !journal.includes('deadbeef'), 'the journal holds no token')
    await bad.screenshot({ path: OUT + '/relay-journal.png' })
    // The install prompt Chromium hands the page is offered in its footer (#73). The tests run in
    // an incognito context, where Chromium hands none: the event is the test's.
    assert(!(await bad.isVisible('[data-testid=install]')), 'no install button without a prompt')
    await bad.evaluate(() => {
      const e = new Event('beforeinstallprompt')
      e.prompt = async () => (window.prompted = true)
      window.dispatchEvent(e)
    })
    await bad.click('[data-testid=install]')
    assert(await bad.evaluate(() => window.prompted === true), 'the install button shows the prompt of the browser')
    await bad.close()

    // A pod that never answers the offer (#74): the line under the step says what it waits for,
    // and after 15 s the page says the pod does not answer, with Retry, while it keeps waiting.
    const stuck = await ctx.newPage()
    // The signaling rides the identity link (#75): its answers are the ones dropped.
    await stuck.routeWebSocket(/\/_relay\/identity/, (ws) => {
      const server = ws.connectToServer()
      server.onMessage((m) => {
        if (!String(m).includes('"answer"')) ws.send(m)
      })
      ws.onMessage((m) => server.send(m))
    })
    await stuck.goto(link(TOKEN), { waitUntil: 'domcontentloaded' })
    await stuck.waitForFunction(() => document.getElementById('sub')?.textContent.includes('waiting for the pod to answer'), null, { timeout: 15000 })
    await stuck.waitForSelector('[data-testid=stall]', { timeout: 25000 })
    assert((await stuck.textContent('[data-testid=stall]')).includes('does not answer the offer'), 'a pod that does not answer is said so')
    assert(await stuck.isVisible('[data-testid=stall] button'), 'a stalled step offers Retry')
    assert(await stuck.isVisible('#trace-lines'), 'the Details panel is open while the page connects')
    await stuck.setViewportSize({ width: 390, height: 900 })
    await stuck.screenshot({ path: OUT + '/relay-stall.png' })
    await stuck.close()

    // The relay stopped: the pod keeps working on 127.0.0.1, and Settings shows the link as
    // disconnected (the pod notices it and reconnects with backoff when the relay comes back).
    const local = await ctx.newPage()
    await local.goto(`${process.env.E2E_URL}/?token=${TOKEN}`)
    await local.waitForSelector('.home .home-bar', { timeout: 15000 })
    assert(await local.evaluate(() => window.parent === window), '127.0.0.1 still works with the same pod')

    const relayPid = Number(process.env.E2E_RELAY_PID)
    if (!relayPid) throw new Error('E2E_RELAY_PID not set (run through e2e/run.sh)')
    process.kill(relayPid)

    // The caches are warm, the relay is gone: the app must not come back out of the cache. The
    // page of the pod is always carried by the tunnel, so a dead connection never shows an IDE.
    await page.reload({ waitUntil: 'domcontentloaded' }).catch(() => {})
    await page.waitForTimeout(1500)
    assert(!(await page.isVisible('#frame')), 'a reload without the relay shows no app, cached assets or not')
    // The shell itself is cached (#73): the page of the relay says what failed, not the browser.
    assert(await page.isVisible('#status .card'), 'a reload without the relay shows the cached bootstrap page')
    await local.click('.home-head button[title="Settings"]')
    await local.waitForSelector('.settings')
    await local.click('.settings-nav button:has-text("Remote access")')
    await local.waitForSelector('.settings-body .props')
    for (let i = 0; i < 30 && !(await local.isVisible('.settings-body .dot-disconnected')); i++) await local.waitForTimeout(500)
    assert(await local.isVisible('.settings-body .dot-disconnected'), 'the link to the relay shows disconnected after the relay stopped')
    await local.click('.modal-settings .modal-head button')
    await local.waitForFunction(() => document.querySelector('.project-list .project-card, .ws-dir'), null, { timeout: 15000 })
    assert(await local.isVisible('.project-list .project-card, .ws-dir'), 'the pod still serves its projects with the relay down')

    // The relay comes back: the pod reconnects by itself (backoff), and the link shows
    // connected again.
    // It comes back as a deploy would, with another shell (#73): its installable app changed.
    const { spawn } = require('child_process')
    const dist = require('path').join(__dirname, '../../pod/webdist/dist')
    const shellDir = fs.mkdtempSync(require('os').tmpdir() + '/relay-app-')
    fs.cpSync(dist, shellDir, { recursive: true })
    fs.writeFileSync(shellDir + '/manifest.webmanifest', fs.readFileSync(shellDir + '/manifest.webmanifest', 'utf8').replace('"Web IDE"', '"Web IDE "'))
    const relay = spawn(process.env.E2E_BIN, ['-data', process.env.E2E_RELAY_DATA, '-addr', '127.0.0.1:' + process.env.E2E_RELAY_PORT, '-turn-ip', '127.0.0.1', '-turn-port', process.env.E2E_TURN_PORT, '-static', shellDir, 'relay'], { stdio: 'ignore' })
    await local.click('.home-head button[title="Settings"]')
    await local.waitForSelector('.settings')
    await local.click('.settings-nav button:has-text("Remote access")')
    // The backoff of the pod can reach 30 s between two dial attempts.
    for (let i = 0; i < 140 && !(await local.isVisible('.settings-body .dot-connected')); i++) await local.waitForTimeout(500)
    assert(await local.isVisible('.settings-body .dot-connected'), 'the pod reconnects to the relay after it is restarted')

    // The page still runs on the old shell: the browser installs the new Service Worker in the
    // background, the page offers to reload, and the reload runs on the new shell — the old one is
    // dropped.
    await page.evaluate(() => navigator.serviceWorker.getRegistration().then((r) => r.update()))
    await page.waitForSelector('#update:not([hidden]) button', { timeout: 20000 })
    await Promise.all([page.waitForEvent('framenavigated', { predicate: (f) => f === page.mainFrame(), timeout: 20000 }), page.click('#update button')])
    await page.waitForSelector('#frame', { state: 'visible', timeout: 30000 })
    const renewed = await page.evaluate(async () => (await caches.keys()).filter((n) => n.startsWith('webide-shell:')))
    assert(renewed.length === 1 && renewed[0] !== shell.names[0], `the reload runs on the new shell: ${shell.names[0]} → ${renewed.join(', ')}`)
    assert((await page.evaluate(() => fetch('/manifest.webmanifest').then((r) => r.text()))).includes('"Web IDE "'), 'the new shell is the one of the relay')
    relay.kill()
    fs.rmSync(shellDir, { recursive: true, force: true })
    // The cached shell of the page kept trying the relay while it was down: those refusals were
    // the point of the test, not an error of the page.
    const down = RELAY.replace(/^http/, 'ws') + '/_relay/'
    for (let i = errors.length - 1; i >= 0; i--) if (errors[i].includes(down) && errors[i].includes('ERR_CONNECTION_REFUSED')) errors.splice(i, 1)
    await local.close()

    assert(!errors.some((e) => e.includes('404')), 'no missing asset through the Service Worker')
    await page.screenshot({ path: OUT + '/relay-home.png' })
    // The fake model server keeps node alive otherwise: the suite would never end.
    fake.closeAllConnections()
    fake.close()
  },
  { url: link(TOKEN) },
)
