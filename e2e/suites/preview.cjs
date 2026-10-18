// App previews (share_preview): the model offers the app, nothing runs before the click; the
// click starts the command in a console, waits for its port and opens the app in a new tab
// through the proxy of the pod (127.0.0.1 here: the fake tailscale of e2e/bin is not running);
// a second click reuses it; a request without the cookie of the IDE is refused; Stop and the
// end of the command kill the URL; the Previews tab of the Docker tool lists them. A saved
// serve command opens from the ▶ menu of the Console, and share_preview without command
// offers it (one console for both).
const fs = require('fs')
const http = require('http')
const net = require('net')
const { run, openProject, assert, OUT, WS } = require('../common.cjs')

const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}

let appPort = 0
let savedPort = 0
const command = () => `node -e "require('http').createServer((q, s) => s.end('preview app ' + q.headers.host + ' ' + (q.headers.cookie || '-'))).listen(${appPort}, '127.0.0.1')"`

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const last = r.messages[r.messages.length - 1]
  if (last.role === 'tool') {
    sse(res, { content: 'Click the card to try it.' })
    return end(res)
  }
  // "the saved one": no command, the saved serve command of the project is offered.
  const args = String(last.content).includes('saved one') ? {} : { title: 'Demo app', command: command(), port: appPort }
  sse(res, { tool_calls: [{ index: 0, id: 'p1', type: 'function', function: { name: 'share_preview', arguments: JSON.stringify(args) } }] })
  end(res, 'tool_calls')
})

const listen = (srv) => new Promise((resolve) => srv.listen(0, '127.0.0.1', () => resolve(srv.address().port)))
const freePort = async () => {
  const s = net.createServer()
  const p = await listen(s)
  await new Promise((r) => s.close(r))
  return p
}
// GET without the cookie of the IDE: [status, body], [0, ''] when nothing answers.
const get = (url) =>
  new Promise((resolve) => {
    http
      .get(url, { timeout: 2000 }, (res) => {
        let body = ''
        res.on('data', (d) => (body += d))
        res.on('end', () => resolve([res.statusCode, body]))
      })
      .on('error', () => resolve([0, '']))
      .on('timeout', function () {
        this.destroy()
        resolve([0, ''])
      })
  })

run(async ({ page, ctx }) => {
  page.on('dialog', (d) => d.accept())
  await listen(fake)
  appPort = await freePort()
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="Assistant"]')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    await page.fill('.ai-composer .ed-content', 'Let me try the app')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Click the card")', { timeout: 20000 })
    await page.waitForSelector('[data-testid=ai-preview]')
    assert((await page.textContent('[data-testid=ai-preview]')).includes('Demo app') && (await page.textContent('[data-testid=ai-preview-open]')).includes('Start and open the app'), 'card of share_preview shown')
    await new Promise((r) => setTimeout(r, 500))
    assert((await get(`http://127.0.0.1:${appPort}/`))[0] === 0, 'nothing runs before the click')
    const consoles = () => page.$$eval('.bottom .console-tabs .tab', (e) => e.filter((x) => x.textContent.includes('node -e')).length)
    assert((await consoles()) === 0, 'no console before the click')

    // Click: console, port awaited, the app in a new tab.
    const [tab] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-testid=ai-preview-open]')])
    await tab.waitForFunction(() => document.body?.textContent.startsWith('preview app'), null, { timeout: 30000 })
    const url = tab.url()
    const shown = await tab.textContent('body')
    assert(url.startsWith('http://127.0.0.1:') && !url.includes(`:${appPort}/`), 'the app opens on the URL of the preview: ' + url)
    assert(shown.startsWith(`preview app localhost:${appPort} webide_token=`), 'the app sees its own host, and the cookie of the IDE (a Web IDE in development needs it): ' + shown)
    await page.waitForSelector('[data-testid=ai-preview-url]')
    assert((await consoles()) === 1, 'the command runs in a console')
    assert(await page.isVisible('[data-testid=ai-preview-local]'), 'the card says the preview is local without Tailscale')
    assert(!(await page.$('[data-testid=ai-preview-public]')), 'no public button without Tailscale')
    await page.screenshot({ path: OUT + '/preview-card.png' })

    // A second click reuses it.
    const [tab2] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-testid=ai-preview-open]')])
    await tab2.waitForLoadState()
    assert(tab2.url() === url && (await consoles()) === 1, 'a second click opens the same preview')
    await tab2.close()

    // Without the cookie of the IDE: refused.
    const [code, body] = await get(url)
    assert(code === 401 && body.includes('private'), `refused without the cookie: ${code}`)

    // Listed in the Docker tool.
    if (!(await page.isVisible('[data-testid=docker-tab-previews]'))) await page.click('.rail-right .rail-btn[title="Docker"]')
    await page.click('[data-testid=docker-tab-previews]')
    await page.waitForSelector(`[data-testid=preview-${appPort}]`)
    assert((await page.textContent(`[data-testid=preview-${appPort}]`)).includes('Demo app'), 'listed in the Previews tab')

    // Stop: the command and the URL end.
    await page.click('[data-testid=ai-preview-close]')
    await page.waitForSelector('[data-testid=ai-preview-open]:has-text("Start and open the app")')
    await page.waitForFunction(() => !document.querySelector('[data-testid=docker-previews] .dk-forward'))
    assert((await get(url))[0] === 0, 'the URL is dead once stopped')
    await page.waitForFunction(() => ![...document.querySelectorAll('.bottom .console-tabs .tab')].some((x) => x.textContent.includes('node -e')), null, { timeout: 5000 })
    assert(true, 'its console is closed')

    // Started again, then the command ends: the preview goes with it.
    const [tab3] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-testid=ai-preview-open]')])
    await tab3.waitForFunction(() => document.body?.textContent.startsWith('preview app'), null, { timeout: 30000 })
    const url3 = tab3.url()
    await page.click('.console-tabs .tab:has-text("node -e") .tab-close')
    await page.waitForSelector('[data-testid=ai-preview-open]:has-text("Start and open the app")', { timeout: 5000 })
    assert((await get(url3))[0] === 0, 'the URL is dead once the command stops')

    // A saved serve command with a port: opened from the ▶ menu of the Console.
    savedPort = await freePort()
    fs.mkdirSync(WS + '/demo/.ide', { recursive: true })
    const serve = `node -e "require('http').createServer((q, s) => s.end('saved app')).listen(${savedPort}, '127.0.0.1')"`
    fs.writeFileSync(WS + '/demo/.ide/commands.json', JSON.stringify({ commands: [{ id: 'srv1', name: 'Saved app', command: serve, flag: 'serve', port: savedPort }] }))
    await page.click('[data-testid=console-commands]')
    await page.waitForSelector(`.ctx-item:has-text("Open “Saved app” in a preview"):has-text(":${savedPort}")`)
    const [tab4] = await Promise.all([ctx.waitForEvent('page'), page.click('.ctx-item:has-text("Open “Saved app” in a preview")')])
    await tab4.waitForFunction(() => document.body?.textContent.startsWith('saved app'), null, { timeout: 30000 })
    await page.waitForSelector('.console-tabs .tab.active:has-text("Saved app")', { timeout: 5000 })
    assert(true, 'the saved serve command opens as a preview, in its console')

    // share_preview without command: the saved serve command, already running.
    await page.fill('.ai-composer .ed-content', 'Show me the saved one')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=ai-preview]:has-text("Saved app")', { timeout: 20000 })
    await page.waitForSelector('[data-testid=ai-preview]:has-text("Saved app") [data-testid=ai-preview-open]:has-text("Open the app")')
    assert(true, 'share_preview offers the saved serve command, running already')
    const [tab5] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-testid=ai-preview]:has-text("Saved app") [data-testid=ai-preview-open]')])
    await tab5.waitForLoadState()
    const savedTabs = await page.$$eval('.bottom .console-tabs .tab', (e) => e.filter((x) => x.textContent.includes('Saved app')).length)
    assert(tab5.url() === tab4.url() && savedTabs === 1, 'the same preview and one console for the command: ' + savedTabs)
  } finally {
    fake.close()
  }
})
