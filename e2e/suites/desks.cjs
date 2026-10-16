// Desks (docs/desks.md): each desk of a project has its own layout; the URL names the desk; two
// windows on two desks do not touch each other, two on the same desk mirror; a tab moves to
// another desk; desks are renamed and removed; the home page opens the last desk of the device.
// A desk in Grid mode is a tree of tiles (files, tools, consoles) driven by the keyboard.
const { run, openProject, open, assert, OUT } = require('../common.cjs')

const tabs = (p) => p.$$eval('.pane .tab .tab-title', (e) => e.map((x) => x.textContent.trim()))
const path = (p) => new URL(p.url()).pathname

run(async ({ ctx, page }) => {
  page.on('dialog', (d) => d.accept())
  await openProject(page)
  await open(page, 'main.go')
  await open(page, 'notes.txt')
  const base = path(page)
  assert(!base.includes('/desk/'), 'desk 1 is the project path: ' + base)
  assert((await page.getAttribute('[data-testid=desk-1]', 'href')).endsWith(base), 'desk 1 is a link to the project path')
  assert((await page.getAttribute('[data-testid=desk-1]', 'aria-current')) === 'page', 'desk 1 is marked as shown')

  // A new desk: empty layout, its own URL; the page is not reloaded.
  await page.evaluate(() => (window.__kept = true))
  await page.click('[data-testid=desk-add]')
  await page.waitForFunction(() => location.pathname.endsWith('/desk/2'))
  await page.waitForFunction(() => !document.querySelector('.pane .tab'))
  assert((await tabs(page)).length === 0, 'desk 2 starts with no tab')
  assert(await page.evaluate(() => window.__kept === true), 'switching desks does not reload the page')
  await open(page, 'app.php')
  await page.screenshot({ path: OUT + '/desks-2.png' })

  // A second window on desk 1: its own layout, untouched by desk 2.
  const other = await ctx.newPage()
  other.on('dialog', (d) => d.accept())
  await other.goto(new URL(base, page.url()).href)
  await other.waitForSelector('.menubar')
  await other.waitForFunction(() => document.querySelectorAll('.pane .tab').length === 2)
  let t1 = await tabs(other)
  assert(t1.includes('main.go') && t1.includes('notes.txt') && !t1.includes('app.php'), 'desk 1 keeps its tabs: ' + t1)
  await open(other, 'go.mod')
  await page.waitForTimeout(800)
  assert(JSON.stringify(await tabs(page)) === JSON.stringify(['app.php']), 'a window on desk 2 is not touched by desk 1')

  // Back to desk 1 by its link: the change of the other window is there; the history goes back.
  await page.click('[data-testid=desk-1]')
  await page.waitForFunction(() => document.querySelectorAll('.pane .tab').length === 3)
  assert(path(page) === base, 'the link of desk 1 switches the window')
  await page.goBack()
  await page.waitForFunction(() => location.pathname.endsWith('/desk/2') && document.querySelectorAll('.pane .tab').length === 1)
  assert(true, 'back in the history returns to desk 2')

  // Two windows on the same desk mirror each other.
  await page.click('[data-testid=desk-1]')
  await page.waitForFunction(() => document.querySelectorAll('.pane .tab').length === 3)
  await other.hover('.pane .tab:has-text("go.mod")')
  await other.click('.pane .tab:has-text("go.mod") .tab-close')
  await other.waitForFunction(() => document.querySelectorAll('.pane .tab').length === 2, null, { timeout: 5000 }).catch(() => {})
  await page.waitForFunction(() => document.querySelectorAll('.pane .tab').length === 2, null, { timeout: 5000 }).catch(() => {})
  assert((await tabs(page)).length === 2, 'two windows on desk 1 mirror a closed tab: ' + (await tabs(page)))

  // A middle click opens the desk in a new tab.
  const [popup] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-testid=desk-2]', { button: 'middle' })])
  await popup.waitForSelector('.menubar')
  await popup.waitForFunction(() => document.querySelectorAll('.pane .tab').length === 1)
  assert(path(popup).endsWith('/desk/2') && (await tabs(popup))[0] === 'app.php', 'middle click opens desk 2 in a new tab')
  await popup.close()

  // Ctrl+Alt+2 goes to desk 2.
  await page.click('.pane .ed-content')
  await page.keyboard.press('Control+Alt+Digit2')
  await page.waitForFunction(() => location.pathname.endsWith('/desk/2') && document.querySelectorAll('.pane .tab').length === 1, null, { timeout: 5000 }).catch(() => {})
  assert((await tabs(page))[0] === 'app.php', 'Ctrl+Alt+2 shows desk 2: ' + path(page) + ' ' + (await tabs(page)))

  // The tab menu sends a tab to desk 1: it leaves here and shows there at once.
  await page.click('.pane .tab:has-text("app.php")', { button: 'right' })
  await page.click('.ctx-menu >> text=Move to desk 1')
  await page.waitForFunction(() => !document.querySelector('.pane .tab'))
  await other.waitForFunction(() => [...document.querySelectorAll('.pane .tab')].some((t) => t.textContent.includes('app.php')), null, { timeout: 5000 }).catch(() => {})
  assert((await tabs(other)).includes('app.php'), 'the tab moved to desk 1 shows in its window: ' + (await tabs(other)))

  // A desk keeps its layout after a reload; the home page opens the last desk of the device.
  await open(page, 'notes.txt')
  await page.waitForTimeout(800)
  await page.reload()
  await page.waitForSelector('.menubar')
  await page.waitForFunction(() => document.querySelectorAll('.pane .tab').length === 1)
  assert(path(page).endsWith('/desk/2') && (await tabs(page))[0] === 'notes.txt', 'desk 2 restored after a reload')
  const home = await ctx.newPage()
  await home.goto(new URL('/', page.url()).href)
  await home.waitForSelector('.project-open:has-text("demo")')
  assert((await home.getAttribute('.project-open:has-text("demo")', 'href')).endsWith('/desk/2'), 'the home page links to the last desk of the device')
  await home.close()

  // Rename desk 2 from the other window: every window shows the name.
  await other.click('[data-testid=desk-2]', { button: 'right' })
  await other.click('.ctx-menu >> text=Rename…')
  await other.fill('.modal input', 'Review')
  await other.keyboard.press('Enter')
  await page.waitForFunction(() => document.querySelector('[data-testid=desk-2]')?.textContent.trim() === 'Review')
  assert(true, 'a renamed desk shows its name in every window')

  // Remove desk 2 from desk 1: the window on it goes back to desk 1.
  await other.click('[data-testid=desk-2]', { button: 'right' })
  await other.click('.ctx-menu >> text=Remove the desk')
  await page.waitForFunction(() => !location.pathname.includes('/desk/'), null, { timeout: 5000 })
  assert(!(await page.$('[data-testid=desk-2]')), 'a removed desk leaves the switcher and its window goes to desk 1')
  await page.screenshot({ path: OUT + '/desks-removed.png' })

  // ---- Grid mode: a new desk of tiles, no rails nor zones.
  await other.close()
  await page.click('[data-testid=desk-add]')
  await page.waitForFunction(() => /\/desk\/\d+$/.test(location.pathname))
  const gridDesk = path(page).split('/').pop()
  await page.click(`[data-testid=desk-${gridDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Grid of tiles')
  await page.waitForSelector('.workbench.grid-desk')
  assert(!(await page.$('.rail-left')) && (await page.isVisible('[data-testid=tile-launcher]')), 'a Grid desk has no rails and offers the tile launcher')

  const panes = () => page.$$eval('.pane', (l) => l.length)
  const activeTool = () => page.evaluate(() => {
    const p = document.querySelector('.pane.active')
    return p?.querySelector('[data-tool]')?.getAttribute('data-tool') ?? (p?.querySelector('.tile-console') ? 'console' : p?.querySelector('.ed-content') ? 'file' : '')
  })
  const launch = async (label) => {
    await page.keyboard.press('Control+Alt+Enter')
    await page.waitForSelector('.pick-input')
    await page.keyboard.type(label)
    await page.waitForFunction((l) => document.querySelector('.pick-input')?.value === l && document.querySelector('.pick-item.selected')?.textContent.includes(l), label)
    await page.keyboard.press('Enter')
  }
  await launch('Git')
  await page.waitForSelector('.pane .tile-tool[data-tool=git] .panel')
  assert((await panes()) === 1, 'the first tile takes the empty pane')
  await launch('New terminal')
  await page.waitForSelector('.pane .tile-console .xterm')
  assert((await panes()) === 2 && (await activeTool()) === 'console', 'a terminal opens in a new tile beside')
  const xOf = (sel) => page.$eval(sel, (e) => e.getBoundingClientRect().left)
  assert((await xOf('.pane .tile-console')) > (await xOf('.pane [data-tool=git]')), 'by default a new tile opens on the right')
  assert(!!(await page.$('.pane.active.next-row')), 'the active tile marks its right side, where the next tile goes')
  // Ctrl+Alt+V: the next tile opens below; the marker and the bar button follow.
  await page.keyboard.press('Control+Alt+KeyV')
  await page.waitForSelector('.pane.active.next-col')
  assert((await page.getAttribute('[data-testid=tile-split]', 'data-split')) === 'col', 'Ctrl+Alt+V switches new tiles to below')
  await launch('File')
  await page.waitForSelector('.pick.se')
  await page.keyboard.type('main.go')
  await page.waitForSelector('.pick-item.selected:has-text("main.go")')
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.querySelectorAll('.pane').length === 3 && document.querySelector('.pane.active .tab.active')?.textContent.includes('main.go'))
  assert(true, 'a file chosen in the launcher opens in a third tile')
  await page.waitForTimeout(500) // the editor of the file takes the focus once loaded
  await page.screenshot({ path: OUT + '/desks-grid.png' })

  // Directional focus: the file is below the terminal, the terminal right of Git.
  await page.keyboard.press('Control+Alt+Shift+ArrowUp')
  await page.waitForFunction(() => document.querySelector('.pane.active .tile-console'))
  await page.keyboard.press('Control+Alt+Shift+ArrowLeft')
  await page.waitForFunction(() => document.querySelector('.pane.active [data-tool=git]') && document.querySelector('.pane.active')?.contains(document.activeElement))
  assert((await activeTool()) === 'git', 'Ctrl+Alt+Shift+arrows move the focus between tiles')

  // Swap with the tile on the right (from Search Everywhere: no default shortcut).
  const before = await page.$eval('.pane.active', (p) => p.getBoundingClientRect().left)
  await page.keyboard.press('Shift')
  await page.keyboard.press('Shift')
  await page.waitForSelector('.pick.se')
  await page.keyboard.type('Swap with the tile on the right')
  await page.waitForSelector('.pick-item.selected:has-text("Swap with the tile on the right")')
  await page.keyboard.press('Enter')
  await page.waitForFunction((x) => {
    const p = document.querySelector('.pane.active')
    return p?.querySelector('[data-tool=git]') && p.getBoundingClientRect().left > x
  }, before, { timeout: 5000 }).catch(() => {})
  assert((await activeTool()) === 'git' && (await page.$eval('.pane.active', (p) => p.getBoundingClientRect().left)) > before, 'a tile swaps with its neighbour and keeps the focus')

  // Resize mode: the arrows grow the focused tile, Escape ends it.
  const width = () => page.$eval('.pane.active', (p) => p.getBoundingClientRect().width)
  const w0 = await width()
  await page.keyboard.press('Control+Alt+Shift+KeyR')
  await page.waitForSelector('[data-testid=tile-resizing]')
  await page.keyboard.press('ArrowLeft')
  await page.keyboard.press('Shift+ArrowLeft')
  await page.waitForTimeout(200)
  const w1 = await width()
  await page.keyboard.press('Escape')
  await page.waitForSelector('[data-testid=tile-resizing]', { state: 'detached' })
  await page.keyboard.press('ArrowLeft') // a plain key again, not a resize
  await page.waitForTimeout(200)
  assert(w1 < w0 - 100 && Math.abs((await width()) - w1) < 2, `resize mode: the arrows resize the tile, Escape ends it (${w0} → ${w1})`)

  // Closing a tile; the desk is restored after a reload.
  await page.keyboard.press('Alt+w')
  await page.waitForFunction(() => document.querySelectorAll('.pane').length === 2)
  assert(!(await page.$('[data-tool=git]')), 'closing the tab of a tile closes the tile')
  await page.waitForTimeout(800)
  await page.reload()
  await page.waitForSelector('.workbench.grid-desk .pane .tile-console .xterm')
  assert((await panes()) === 2, 'the Grid desk and its tiles are restored after a reload')

  // Back to Full: rails and zones again, the tiles stay as tabs.
  await page.click(`[data-testid=desk-${gridDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Full layout')
  await page.waitForSelector('.rail-left')
  assert((await panes()) === 2 && !!(await page.$('.pane .tile-console')), 'switching to Full keeps the tiles as tabs')

  // A tool leaves its zone for a tab, and can show in its zone at the same time.
  await page.click('.rail .rail-btn[title="Git"]')
  await page.waitForSelector('.zone[data-tool=git] .panel')
  await page.click('.zone[data-tool=git] [data-testid=zone-as-tab]')
  await page.waitForSelector('.pane .tile-tool[data-tool=git] .panel')
  assert(!(await page.$('.zone[data-tool=git]')), 'Open as a tab moves the tool from its zone to a tab')
  await page.click('.rail .rail-btn[title="Git"]')
  await page.waitForSelector('.zone[data-tool=git] .panel')
  assert((await page.$$('[data-tool=git] .panel')).length === 2, 'a tool shows in its zone and as a tab at the same time')
  await page.screenshot({ path: OUT + '/desks-tool-twice.png' })
})
