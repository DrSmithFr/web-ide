// Quota (milestone 5): run.sh starts the relay with -quota 1MB and the pod with -force-turn, so
// every byte of the app crosses TURN. The first load passes the quota; the next tunnel gets no
// TURN credential, and the page says why, with what lifts it. Settings > Remote access of the pod
// tells the same month.
const { run, assert, OUT } = require('../common.cjs')

const RELAY = process.env.E2E_RELAY_URL
const POD_ID = process.env.E2E_POD_ID
const TOKEN = process.env.E2E_TOKEN
const link = (t) => `${RELAY}/#pod=${POD_ID}&t=${t}`

run(
  async ({ page, ctx, errors }) => {
    // Under the quota: the app comes through the relay as usual.
    await page.waitForSelector('#frame', { timeout: 30000 })
    // The frame shows before the app is loaded: on a slow runner the rest of its files are
    // still crossing TURN.
    const received = () =>
      page.evaluate(async () => {
        const r = await window.tunnel.pc.getStats()
        let n = 0
        r.forEach((c) => { if (c.type === 'candidate-pair' && c.state === 'succeeded') n += c.bytesReceived ?? 0 })
        return n
      })
    let bytes = await received()
    for (let end = Date.now() + 30000; bytes <= 1024 * 1024 && Date.now() < end; bytes = await received()) await page.waitForTimeout(500)
    assert(bytes > 1024 * 1024, 'the first load carried over the quota through TURN: ' + bytes)

    // The month is spent: the relay issues no TURN credential, and this pod has no direct path.
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.waitForSelector('.quota', { timeout: 30000 })
    assert(!(await page.isVisible('#frame')), 'no tunnel over the quota, TURN forced')
    const note = (await page.textContent('.quota')).trim()
    assert(/quota/i.test(note), 'the page says the quota is reached: ' + note)
    assert(/\d+(\.\d)? (MB|Mo)/.test(note), 'the page shows what the month used: ' + note)
    assert(await page.isVisible('.quota a[href="https://github.com/sponsors/DrSmithFr"]'), 'the quota note points at what lifts it')

    // The pod, on its own origin: Settings > Remote access carries the same state.
    const local = await ctx.newPage()
    await local.goto(`${process.env.E2E_URL}/?token=${TOKEN}`)
    await local.waitForSelector('.home .home-bar', { timeout: 30000 })
    await local.click('.home-head button[title="Settings"]')
    await local.waitForSelector('.settings')
    await local.click('.settings-nav button:has-text("Remote access")')
    await local.waitForSelector('.settings-body .props')
    const remote = (await local.textContent('.settings-body')).replace(/\s+/g, ' ')
    assert(/quota is reached/i.test(remote), 'Settings tells the quota is reached: ' + remote.slice(0, 300))
    assert(await local.isVisible('.settings-body a[href="https://github.com/sponsors/DrSmithFr"]'), 'Settings links what lifts the quota')
    // The pod itself still works: the quota closes the relayed path, not the pod.
    assert(await local.isVisible('.project-list .project-card, .ws-dir'), 'the pod keeps serving its projects over quota')
    await local.screenshot({ path: OUT + '/quota-settings.png' })
    await page.screenshot({ path: OUT + '/quota-page.png' })
  },
  { url: link(TOKEN) },
)
