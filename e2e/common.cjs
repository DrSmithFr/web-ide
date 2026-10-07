// Shared helpers of the browser tests. run.sh sets E2E_URL, E2E_TOKEN, E2E_WS, E2E_OUT.
const fs = require('fs')
const path = require('path')
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
  const ctx = await browser.newContext({ viewport: opts.viewport ?? { width: 1440, height: 900 }, isMobile: !!opts.mobile, hasTouch: !!opts.mobile, deviceScaleFactor: opts.scale ?? 1, permissions: opts.permissions ?? [], locale: opts.locale ?? process.env.E2E_LOCALE ?? 'en-US' })
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
  await page.goto(`${process.env.E2E_URL}/?token=${process.env.E2E_TOKEN}`)
  return { browser, ctx, page, errors }
}

/** Opens the demo project from the home page. */
exports.openProject = async (page) => {
  await page.waitForSelector('.home .home-bar')
  await page.waitForFunction(() => document.querySelector('.project-list .project-card, .ws-dir'))
  // Fresh data: the project is added from the workspace folders.
  if (await page.$('.project-open:has-text("demo")')) await page.click('.project-open:has-text("demo")')
  else await page.click('.ws-dir:has-text("demo")')
  await page.waitForSelector('.menubar')
  await page.waitForTimeout(400)
}

/** Opens a file with "go to file". */
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
  })().catch((e) => {
    console.log('  FAIL ' + e.message.split('\n').slice(0, 3).join(' | '))
    process.exit(1)
  })

/** Text of the active editor (without the trailing sentinel newline). */
exports.text = (page) => page.evaluate(() => document.querySelector('.pane.active .ed-content').textContent.slice(0, -1))
