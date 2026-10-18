// Shared helpers of the browser tests. run.sh sets E2E_URL, E2E_TOKEN, E2E_WS, E2E_OUT.
const fs = require('fs')
const path = require('path')
const http = require('http')
const crypto = require('crypto')
const { chromium } = require('playwright-core')

function chrome() {
  if (process.env.CHROME) return process.env.CHROME
  const cache = path.join(process.env.HOME, '.cache/ms-playwright')
  const dirs = fs.existsSync(cache) ? fs.readdirSync(cache).filter((d) => /^chromium-\d+$/.test(d)).sort().reverse() : []
  for (const d of dirs) {
    const p = path.join(cache, d, 'chrome-linux64/chrome')
    if (fs.existsSync(p)) return p
  }
  for (const p of ['/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/google-chrome']) if (fs.existsSync(p)) return p
  throw new Error('Chromium not found: set CHROME=/path/to/chrome')
}

exports.WS = process.env.E2E_WS
exports.OUT = process.env.E2E_OUT

exports.start = async (opts = {}) => {
  const browser = await chromium.launch({ executablePath: chrome(), headless: true, args: opts.args ?? [] })
  // English interface unless E2E_LOCALE says otherwise (the browser follows the system language).
  const ctx = await browser.newContext({ viewport: opts.viewport ?? { width: 1440, height: 900 }, isMobile: !!opts.mobile, hasTouch: !!opts.mobile, permissions: opts.permissions ?? [], locale: opts.locale ?? process.env.E2E_LOCALE ?? 'en-US' })
  // The suites written for the Build mode start new conversations in it (the default of the
  // page is the Orchestrator).
  if (!opts.orchestrator)
    await ctx.addInitScript(() => {
      try {
        const p = JSON.parse(localStorage.getItem('webide.llm.prefs') || '{}')
        if (!('defaultMode' in p)) localStorage.setItem('webide.llm.prefs', JSON.stringify({ ...p, defaultMode: 'build' }))
      } catch {}
    })
  const page = await ctx.newPage()
  const errors = []
  page.on('console', (m) => m.type() === 'error' && errors.push('console: ' + m.text()))
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message))
  // The relay suite opens the bootstrap page of the relay (its own url); the others the pod.
  await page.goto(opts.url ?? `${process.env.E2E_URL}/?token=${process.env.E2E_TOKEN}`)
  return { browser, ctx, page, errors }
}

/** Opens the demo project from the home page. */
exports.openProject = async (page) => {
  await page.waitForSelector('.home .home-bar')
  await page.waitForFunction(() => document.querySelector('.project-list .project-card, .ws-dir'))
  // Fresh data: the project is added from the folders of the projects folder.
  if (await page.$('.project-open:has-text("demo")')) await page.click('.project-open:has-text("demo")')
  else await page.click('.ws-dir:has-text("demo")')
  await page.waitForSelector('.menubar')
  await page.waitForTimeout(400)
}

/** Opens a file with "go to file". */
/** Text of the message box of the assistant (an editor: its last block ends with a newline). */
exports.composerText = (page, scope = '') => page.$eval((scope + ' .ai-composer .ed-content').trim(), (e) => e.textContent.replace(/\n$/, ''))

exports.open = async (page, name) => {
  await page.keyboard.press('Control+Shift+n')
  await page.waitForSelector('.pick-input')
  await page.keyboard.type(name)
  await page.waitForFunction((n) => document.querySelector('.pick-item.selected')?.textContent.includes(n), name)
  await page.keyboard.press('Enter')
  await page.waitForFunction((n) => document.querySelector('.pane.active .tab.active')?.textContent.includes(n), name)
  await page.waitForSelector('.pane.active .ed-content')
  await page.waitForTimeout(300)
}

let failed = 0
exports.assert = (cond, msg) => {
  if (cond) console.log('  ok   ' + msg)
  else {
    console.log('  FAIL ' + msg)
    failed++
    process.exitCode = 1
  }
}

/** Runs a test body, closes the browser, reports page errors as failures. */
exports.run = (body, opts) =>
  (async () => {
    const t = await exports.start(opts)
    try {
      await body(t)
    } catch (e) {
      // A screenshot of the page at the failure helps more than the selector alone.
      await t.page.screenshot({ path: path.join(exports.OUT, 'failure.png') }).catch(() => {})
      throw e
    } finally {
      if (t.errors.length) exports.assert(false, 'page errors:\n    ' + t.errors.join('\n    '))
      await t.browser.close()
    }
  })().then(
    // The suite is over: a fake server still holding a request of the pod (close() waits for
    // it) must not keep node alive, the pod being stopped only after node exits.
    () => process.exit(process.exitCode ?? 0),
    (e) => {
      console.log('  FAIL ' + e.message.split('\n').slice(0, 3).join(' | '))
      process.exit(1)
    },
  )

/** Text of the active editor (without the trailing sentinel newline). */
exports.text = (page) => page.evaluate(() => document.querySelector('.pane.active .ed-content').textContent.slice(0, -1))

/** Calls a tool of the MCP endpoint of the pod, as Claude Code does (in the demo project by default). */
exports.mcp = (name, args = {}) =>
  fetch(process.env.E2E_URL + '/mcp', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + process.env.E2E_TOKEN },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: { cwd: exports.WS + '/demo', ...args } } }),
  }).then((r) => r.json())

/** Opens a folded section of the ticket view by its title. */
exports.unfold = (page, title) => page.click(`.tk-section.folded .tk-section-toggle:has-text("${title}")`)

/** Dispatches a two-finger pinch on an element and tells whether the page refused it (the
 * guards of the phone layout). A Touch cannot be built with new Touch() in Chromium. */
exports.pinchRefused = (page, sel) =>
  page.evaluate((s) => {
    const el = document.querySelector(s)
    if (!el) return null
    const touch = (id, x, y) => new Touch({ identifier: id, target: el, clientX: x, clientY: y, pageX: x, pageY: y, screenX: x, screenY: y })
    const fire = (type, touches) => {
      const e = new TouchEvent(type, { bubbles: true, cancelable: true, touches, targetTouches: touches, changedTouches: touches })
      el.dispatchEvent(e)
      return e.defaultPrevented
    }
    const a = touch(1, 60, 60)
    const b = touch(2, 160, 60)
    fire('touchstart', [a, b])
    return fire('touchmove', [touch(1, 60, 90), touch(2, 160, 30)])
  }, sel)

const b64u = (b) => Buffer.from(b).toString('base64url')

/** The VAPID token (ES256) verifies with the key the relay sends with it. */
function vapidOK(authz) {
  const m = /^vapid t=([^,]+), k=(.+)$/.exec(authz || '')
  if (!m) return false
  const [head, claims, sig] = m[1].split('.')
  const k = Buffer.from(m[2], 'base64url')
  const key = crypto.createPublicKey({ key: { kty: 'EC', crv: 'P-256', x: b64u(k.subarray(1, 33)), y: b64u(k.subarray(33)) }, format: 'jwk' })
  return crypto.verify('sha256', Buffer.from(head + '.' + claims), { key, dsaEncoding: 'ieee-p1363' }, Buffer.from(sig, 'base64url'))
}

/** RFC 8291 decryption, the side of the browser. */
function decryptPush(buf, ua, auth) {
  const salt = buf.subarray(0, 16)
  const idlen = buf[20]
  const asPub = buf.subarray(21, 21 + idlen)
  const body = buf.subarray(21 + idlen)
  const secret = ua.computeSecret(asPub)
  const info = Buffer.concat([Buffer.from('WebPush: info\0'), ua.getPublicKey(), asPub])
  const ikm = Buffer.from(crypto.hkdfSync('sha256', secret, auth, info, 32))
  const cek = Buffer.from(crypto.hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: aes128gcm\0'), 16))
  const nonce = Buffer.from(crypto.hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: nonce\0'), 12))
  const d = crypto.createDecipheriv('aes-128-gcm', cek, nonce)
  d.setAuthTag(body.subarray(-16))
  const plain = Buffer.concat([d.update(body.subarray(0, -16)), d.final()])
  return plain.subarray(0, plain.lastIndexOf(2)).toString()
}

/** The fake push service of the suites (headless Chromium cannot reach a real one): it listens on
 * the port run.sh gives, records every POST it gets and decrypts its body with the keys of the
 * subscription it made — a suite replaces the subscription of the browser with that one (an
 * endpoint the relay lets through with -push-test-endpoint). */
exports.pushService = () => {
  const port = Number(process.env.E2E_PUSH_PORT)
  const ua = crypto.createECDH('prime256v1')
  ua.generateKeys()
  const auth = crypto.randomBytes(16)
  const endpoint = `http://127.0.0.1:${port}/push/${crypto.randomBytes(8).toString('hex')}`
  const received = []
  const server = http.createServer(async (req, res) => {
    const chunks = []
    for await (const c of req) chunks.push(c)
    const body = Buffer.concat(chunks)
    let payload = null
    try {
      payload = JSON.parse(decryptPush(body, ua, auth))
    } catch {}
    received.push({ path: req.url, vapid: vapidOK(req.headers.authorization), encoding: req.headers['content-encoding'], topic: req.headers.topic, raw: body, payload })
    res.writeHead(201).end()
  })
  return {
    server,
    received,
    endpoint,
    keys: { p256dh: b64u(ua.getPublicKey()), auth: b64u(auth) },
    listen: () => new Promise((resolve) => server.listen(port, '127.0.0.1', () => resolve(port))),
  }
}
