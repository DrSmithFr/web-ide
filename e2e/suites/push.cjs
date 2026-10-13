// Push notifications (relay milestone 6): a paired browser enables them in Settings >
// Notifications, the pod notifies it when an agent turn ends while it shows no window, and the
// relay forwards ciphertext only. run.sh starts the relay with -push-test-endpoint and the pod
// with -relay; the pod joins the account with the phrase.
//
// Headless Chromium cannot reach a real push service: the suite replaces the subscription of
// the browser with one whose keys it made (Node ECDH), runs the fake push service it points
// to, checks the VAPID signature and decrypts the body (RFC 8291) there, then hands the
// payload to the Service Worker (CDP ServiceWorker.deliverPushMessage), reads the notification
// it shows and clicks it.
const fs = require('fs')
const http = require('http')
const { execFileSync } = require('child_process')
const { run, assert, pushService } = require('../common.cjs')

const ORIGIN = `http://localhost:${process.env.E2E_RELAY_PORT}`
const POD_ID = process.env.E2E_POD_ID
const PHRASE = 'lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper'

// The fake push service, with the keys of the subscription it made: the suite decrypts what the
// pod sends.
const push = pushService()
const { endpoint, received } = push

// The fake model server: one answer per message.
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

const listen = (srv, port = 0) => new Promise((resolve) => srv.listen(port, '127.0.0.1', () => resolve(srv.address().port)))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
async function waitFor(f, ms = 15000) {
  for (const end = Date.now() + ms; Date.now() < end; await sleep(200)) {
    const v = f()
    if (v) return v
  }
  return null
}

/** Waits until the pod is registered with the relay (its signaling path stops answering 404). */
async function waitOnline() {
  for (let i = 0; i < 40; i++) {
    const code = await new Promise((res) => {
      http.get(`http://127.0.0.1:${process.env.E2E_RELAY_PORT}/_relay/signal?pod=${POD_ID}&session=probe`, (r) => {
        r.resume()
        res(r.statusCode)
      }).on('error', () => res(0))
    })
    if (code !== 404 && code !== 0) return true
    await sleep(500)
  }
  return false
}

run(
  async ({ page, ctx, browser }) => {
    await push.listen()
    await listen(llm)

    // The pod joins the account with the phrase (its own key), then restarts to use it.
    execFileSync(process.env.E2E_BIN, ['-data', process.env.E2E_DATA, 'pair', 'join', '--phrase'], { input: PHRASE + '\n' })
    execFileSync('bash', [process.env.E2E_RESTART_POD])
    assert(await waitOnline(), 'the paired pod is online at the relay')

    // The browser: a virtual authenticator for its passkey, the notification permission, and
    // the subscription of the suite in place of the push service of the browser.
    const cdp = await ctx.newCDPSession(page)
    await cdp.send('WebAuthn.enable')
    await cdp.send('WebAuthn.addVirtualAuthenticator', {
      options: { protocol: 'ctap2', ctap2Version: 'ctap2_1', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, hasPrf: true, automaticPresenceSimulation: true },
    })
    await ctx.grantPermissions(['notifications'], { origin: ORIGIN })
    await ctx.addInitScript(({ endpoint, p256dh, auth }) => {
      if (!self.PushManager) return
      const sub = { endpoint, options: {}, toJSON: () => ({ endpoint, keys: { p256dh, auth } }), getKey: () => null, unsubscribe: async () => true }
      let current = null
      PushManager.prototype.subscribe = async () => (current = sub)
      PushManager.prototype.getSubscription = async () => current
    }, { endpoint, p256dh: push.keys.p256dh, auth: push.keys.auth })

    // Pair this browser with the phrase on the bootstrap page.
    await page.goto(`${ORIGIN}/#pod=${POD_ID}`, { waitUntil: 'domcontentloaded' })
    await page.waitForSelector('#pair input', { timeout: 30000 })
    await page.fill('#pair input', PHRASE)
    await page.keyboard.press('Enter')
    await page.waitForSelector('#frame', { state: 'visible', timeout: 30000 })
    const app = page.mainFrame().childFrames()[0]
    await app.waitForSelector('.home .home-bar', { timeout: 30000 })
    if (await app.locator('.project-open:has-text("demo")').count()) await app.click('.project-open:has-text("demo")')
    else await app.click('.ws-dir:has-text("demo")')
    await app.waitForSelector('.menubar', { timeout: 30000 })

    // Settings > Notifications: enable, the switches, a test.
    await app.click('.menubar button[title^="Settings"]')
    await app.click('.settings-nav button:has-text("Notifications")')
    await app.click('[data-testid=push-enable]')
    await app.waitForSelector('[data-testid=push-kind-finished]', { timeout: 20000 })
    assert((await app.locator('[data-testid=notifications] input[type=checkbox]:checked').count()) === 4, 'four kinds, all on')
    await app.click('[data-testid=push-test]')
    const test = await waitFor(() => received[0])
    assert(test && test.path.startsWith('/push/') && test.vapid && test.encoding === 'aes128gcm', 'the relay POSTs to the endpoint, signed with its VAPID key')
    assert(test?.payload?.title === 'Notifications work', 'the test notification decrypts with the keys of the browser: ' + JSON.stringify(test?.payload))
    await app.click('.modal-settings .modal-head button')

    // An agent turn while the window is shown: no notification.
    await app.click('.rail-right .rail-btn[title="AI assistant"]')
    await app.waitForSelector('.ai-empty')
    await app.click('.ai-empty button:has-text("Add a model server")')
    await app.fill('.ai-servers input[name=url]', `127.0.0.1:${llm.address().port}`)
    await app.click('.ai-servers button:has-text("Add")')
    await app.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await app.click('.ai-servers .modal-head button')
    await app.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')
    await app.fill('.ai-composer .ed-content', 'Shown window')
    await app.click('[data-testid=send]')
    await app.waitForSelector('.ai-msg.assistant .md:has-text("Turn done.")', { timeout: 20000 })
    await sleep(1500)
    assert(received.length === 1, 'no notification while a window of the device is visible')

    // The window is hidden (the page reports visibilitychange): the next turn is notified.
    await app.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await sleep(300)
    await app.fill('.ai-composer .ed-content', 'Hidden window')
    await app.click('[data-testid=send]')
    const done = await waitFor(() => received.find((r) => r.payload?.title === 'The agent finished'), 20000)
    assert(done, 'a hidden device is notified when the agent turn ends')
    const url = done?.payload?.url ?? ''
    const open = decodeURIComponent(new URLSearchParams(url.split('#')[1] ?? '').get('open') ?? '')
    assert(url.startsWith(`/#pod=${POD_ID}&open=`) && /^\/project\/[^?]+\?chat=\w+/.test(open), 'the notification opens the conversation: ' + url)
    assert(done?.payload?.body.includes('demo') && done.topic === done.payload.tag, 'it names the project, its topic is the conversation: ' + done?.payload?.body)
    const chatId = open.split('chat=')[1]

    // The Service Worker shows it, and a click opens the conversation in the app.
    await app.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    const regs = new Promise((resolve) => cdp.on('ServiceWorker.workerRegistrationUpdated', (e) => {
      const r = e.registrations.find((x) => x.scopeURL.startsWith(ORIGIN) && !x.isDeleted)
      if (r) resolve(r.registrationId)
    }))
    await cdp.send('ServiceWorker.enable')
    const registrationId = await regs
    await cdp.send('ServiceWorker.deliverPushMessage', { origin: ORIGIN, registrationId, data: JSON.stringify(done.payload) })
    const sw = ctx.serviceWorkers().find((w) => w.url().startsWith(ORIGIN)) ?? (await ctx.waitForEvent('serviceworker'))
    let shown = null
    for (let i = 0; i < 50 && !shown; i++, await sleep(200)) {
      shown = await sw.evaluate(async () => {
        const [n] = await self.registration.getNotifications()
        return n ? { title: n.title, body: n.body, url: n.data?.url } : null
      })
    }
    assert(shown?.title === 'The agent finished' && shown.url === url, 'the Service Worker shows the notification: ' + JSON.stringify(shown))

    // The app leaves the conversation (home page), then the click brings it back.
    await page.evaluate(() => (document.getElementById('frame').src = '/app'))
    await page.mainFrame().childFrames()[0].waitForSelector('.home .home-bar', { timeout: 30000 })
    await sw.evaluate(async () => {
      const [n] = await self.registration.getNotifications()
      self.dispatchEvent(new NotificationEvent('notificationclick', { notification: n }))
    })
    const back = page.mainFrame().childFrames()[0]
    await back.waitForSelector('.ai-msg.assistant .md:has-text("Turn done.")', { timeout: 30000 }).catch(() => {})
    const opened = await back.evaluate(() => [...document.querySelectorAll('.ai-msg.user')].map((e) => e.textContent).join(' | ')).catch(() => '')
    assert(back.url().includes('/project/') && opened.includes('Hidden window'), 'a click opens the conversation in the app: ' + back.url() + ' ' + opened)

    // The relay base and its log hold no title nor project name.
    const relayFiles = fs.readdirSync(process.env.E2E_RELAY_DATA).filter((f) => f.startsWith('relay.db'))
    const dump = Buffer.concat([...relayFiles.map((f) => fs.readFileSync(`${process.env.E2E_RELAY_DATA}/${f}`)), fs.readFileSync(process.env.E2E_RELAY_LOG)])
    const leaks = ['The agent finished', 'Notifications work', 'Hidden window', 'demo', chatId].filter((w) => dump.includes(w))
    assert(leaks.length === 0 && received.every((r) => !r.raw.includes('demo')), 'the relay base and log hold no payload in clear: ' + leaks.join(', '))

    llm.closeAllConnections()
    llm.close()
    push.server.close()
  },
  { url: 'about:blank' },
)
