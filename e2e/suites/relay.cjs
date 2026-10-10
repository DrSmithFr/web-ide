// Relay (milestone 1): a browser on another network opens a project of a pod through the
// relay. run.sh starts the relay and the pod with -relay and -force-turn, so the selected
// candidate pair is a TURN relay one. The app is loaded from the pod through the tunnel
// (Service Worker), the token is proved inside the DataChannel, terminals and big transfers
// have their own channel, previews are served through the Service Worker; a wrong token is
// refused; 127.0.0.1 still works with the same pod.
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

    // The app window is the iframe of the bootstrap page; the keyboard of the page types
    // into the element focused inside it.
    const app = page.mainFrame().childFrames()[0]
    await app.waitForSelector('.home .home-bar', { timeout: 30000 })
    await app.waitForSelector('.project-list .project-card, .ws-dir')
    if (await app.locator('.project-open:has-text("demo")').count()) await app.click('.project-open:has-text("demo")')
    else await app.click('.ws-dir:has-text("demo")')
    await app.waitForSelector('.menubar')
    await app.waitForTimeout(400)

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
    await app.fill('.ai-composer textarea', 'What do you see?')
    await app.click('[data-testid=send]')
    await app.waitForSelector('.ai-msg.assistant .md:has-text("I see an image.")', { timeout: 20000 })
    assert(await app.isVisible('.ai-msg.user .ai-att img'), 'assistant attachment sent through the tunnel')

    // Preview: the app of the pod is reached through the Service Worker of the relay.
    await app.fill('.ai-composer textarea', 'Attach the app')
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
    assert((await app.textContent('.settings-body dd.mono.small')).includes(`#pod=${POD_ID}&t=${TOKEN}`), 'the remote link shown in Settings')
    await app.click('.modal-settings .modal-head button')

    // A wrong token: the pod refuses the proof (the relay never sees the token).
    const bad = await ctx.newPage()
    await bad.goto(link('deadbeefdeadbeefdeadbeefdeadbeefdeadbeef'), { waitUntil: 'domcontentloaded' })
    await bad.waitForSelector('.err', { timeout: 30000 })
    assert((await bad.textContent('.err')).includes('connection could not be established'), 'a wrong token is refused by the pod')
    await bad.close()

    // The relay stopped: the pod keeps working on 127.0.0.1, and Settings shows the link as
    // disconnected (the pod notices it and reconnects with backoff when the relay comes back).
    const local = await ctx.newPage()
    await local.goto(`${process.env.E2E_URL}/?token=${TOKEN}`)
    await local.waitForSelector('.home .home-bar', { timeout: 15000 })
    assert(await local.evaluate(() => window.parent === window), '127.0.0.1 still works with the same pod')

    const relayPid = Number(process.env.E2E_RELAY_PID)
    if (!relayPid) throw new Error('E2E_RELAY_PID not set (run through e2e/run.sh)')
    process.kill(relayPid)
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
    const { spawn } = require('child_process')
    const relay = spawn(process.env.E2E_BIN, ['-data', process.env.E2E_RELAY_DATA, '-addr', '127.0.0.1:' + process.env.E2E_RELAY_PORT, '-turn-ip', '127.0.0.1', 'relay'], { stdio: 'ignore' })
    await local.click('.home-head button[title="Settings"]')
    await local.waitForSelector('.settings')
    await local.click('.settings-nav button:has-text("Remote access")')
    // The backoff of the pod can reach 30 s between two dial attempts.
    for (let i = 0; i < 140 && !(await local.isVisible('.settings-body .dot-connected')); i++) await local.waitForTimeout(500)
    assert(await local.isVisible('.settings-body .dot-connected'), 'the pod reconnects to the relay after it is restarted')
    relay.kill()
    await local.close()

    assert(!errors.some((e) => e.includes('404')), 'no missing asset through the Service Worker')
    await page.screenshot({ path: OUT + '/relay-home.png' })
  },
  { url: link(TOKEN) },
)
