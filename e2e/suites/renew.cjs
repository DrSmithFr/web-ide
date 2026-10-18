// TURN renewal (#86): run.sh starts the relay with credentials valid 10 s (E2E_TURN_TTL) and the
// pod with -force-turn, so every byte crosses TURN. The page asks fresh credentials at three
// quarters of their life and restarts ICE on the same tunnel: the app keeps working on it, no new
// tunnel is dialled, and the relay never sees an expired credential.
const fs = require('fs')
const { run, assert, OUT } = require('../common.cjs')

const RELAY = process.env.E2E_RELAY_URL
const POD_ID = process.env.E2E_POD_ID
const TOKEN = process.env.E2E_TOKEN
const link = (t) => `${RELAY}/#pod=${POD_ID}&t=${t}`

run(
  async ({ page }) => {
    await page.waitForSelector('#frame', { timeout: 30000 })
    const app = page.mainFrame().childFrames()[0]
    await app.waitForSelector('.home .home-bar', { timeout: 30000 })
    await page.evaluate(() => (window.pcBefore = window.tunnel.pc))

    // Three lives of a credential: the tunnel renewed them each time.
    await page.waitForFunction(() => [...window.tunnels.values()].some((t) => (t.renewed ?? 0) >= 3), null, { timeout: 45000 })
    const same = await page.evaluate(() => window.tunnel.pc === window.pcBefore && window.tunnel.pc.connectionState)
    assert(same === 'connected', 'the same tunnel is still up after three renewals: ' + same)
    const journal = await page.evaluate(() => window.bootJournal)
    assert(journal.includes('TURN credentials renewed'), 'the journal tells the renewal:\n' + journal)
    assert(!journal.includes('Reconnected'), 'no tunnel was dialled again:\n' + journal)
    const pair = await page.evaluate(async () => {
      const r = await window.tunnel.pc.getStats()
      const by = new Map([...r.values()].map((s) => [s.id, s]))
      const t = [...r.values()].find((s) => s.type === 'transport')
      const p = by.get(t?.selectedCandidatePairId)
      return p ? by.get(p.localCandidateId)?.candidateType + '/' + by.get(p.remoteCandidateId)?.candidateType : '?'
    })
    assert(pair.includes('relay'), 'the renewed path is still a TURN one: ' + pair)

    // The app works on the renewed tunnel: a project and a terminal (their own channels).
    await app.waitForSelector('.project-list .project-card, .ws-dir')
    if (await app.locator('.project-open:has-text("demo")').count()) await app.click('.project-open:has-text("demo")')
    else await app.click('.ws-dir:has-text("demo")')
    await app.waitForSelector('.menubar')
    await app.focus('.menubar')
    await page.keyboard.press('Control+Shift+Backquote')
    await app.waitForSelector('.xterm', { timeout: 15000 })
    await page.keyboard.type('echo renewed-$((6*7))\n')
    await app.waitForFunction(() => document.querySelector('.xterm-rows')?.textContent.includes('renewed-42'), null, { timeout: 10000 })

    const log = fs.readFileSync(process.env.E2E_RELAY_LOG, 'utf8')
    assert(!log.includes('Expired time-windowed'), 'the relay saw no expired credential:\n' + log.split('\n').filter((l) => l.includes('Expired')).slice(0, 5).join('\n'))
    await page.screenshot({ path: OUT + '/renew.png' })
  },
  { url: link(TOKEN) },
)
