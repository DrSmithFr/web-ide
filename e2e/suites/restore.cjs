// Session restored after a reload (runs after features, same pod), detached windows.
const { run, openProject, assert, OUT } = require('../common.cjs')
run(async ({ ctx, page }) => {
  await openProject(page)
  await page.waitForTimeout(600)
  const tabsBefore = await page.$$eval('.tab .tab-title', (e) => e.map((x) => x.textContent))
  const panesBefore = (await page.$$('.pane')).length
  console.log('     tabs', JSON.stringify(tabsBefore), 'panes', panesBefore)
  assert(tabsBefore.length >= 4 && panesBefore === 2, 'session restored after reload (tabs and split)')
  // The features suite ends with the console panel closed: the session keeps it closed.
  assert(!(await page.isVisible('.zone-bottomLeft')), 'closed console panel still closed after reload')
  await page.click('.rail-left .rail-btn[title="Console"]')
  await page.waitForSelector('.xterm', { timeout: 5000 }).catch(() => {})
  assert(await page.isVisible('.xterm'), 'terminal still there after reload')
  await page.waitForFunction(() => document.querySelector('.xterm-rows')?.textContent.includes('pod-42'), null, { timeout: 5000 }).catch(() => {})
  const term = await page.textContent('.xterm-rows')
  assert(term.includes('pod-42'), 'terminal scrollback restored')

  // Detached editor window in sync
  const url = new URL(page.url())
  const win = await ctx.newPage()
  await win.goto(url.origin + url.pathname + '/editor')
  await win.waitForSelector('.detached .pane')
  await win.waitForTimeout(500)
  // main window: open notes.txt and type
  await page.click('.rail-left .rail-btn[title="Explorer"]')
  await page.dblclick('.tree-row:has-text("notes.txt")')
  await page.waitForTimeout(300)
  await page.click('.pane.active .ed-content')
  await page.keyboard.press('Control+End')
  await page.keyboard.type('sync-test')
  await page.waitForTimeout(1200)
  const other = await win.$$eval('.ed-content', (els) => els.map((e) => e.textContent).join('|'))
  assert(other.includes('sync-test'), 'detached window receives the unsaved buffer')
  await win.screenshot({ path: OUT + '/s10-detached.png' })
  await page.keyboard.press('Control+z')
  await page.waitForTimeout(500)
  // detached tool window
  const tool = await ctx.newPage()
  await tool.goto(url.origin + url.pathname + '/tool/database')
  await tool.waitForSelector('.db-tool .conn-row')
  assert(true, 'detached tool window')
})
