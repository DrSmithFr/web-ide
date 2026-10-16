// Desks (docs/desks.md): each desk of a project has its own layout; the URL names the desk; two
// windows on two desks do not touch each other, two on the same desk mirror; a tab moves to
// another desk; desks are renamed and removed; the home page opens the last desk of the device.
// A desk in Grid mode is a tree of tiles (files, tools, consoles) driven by the keyboard; in
// Scroll mode its columns go side by side in a strip, 1 to 3 in sight.
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
  assert(!(await page.isVisible('.pane:has([data-tool=git]) > .tabbar')) && (await page.isVisible('[data-tool=git] [data-testid=tile-close]')), 'a tool alone in its tile has no tab bar, a close button instead')
  await launch('New terminal')
  await page.waitForSelector('.pane .tile-console .xterm')
  assert((await panes()) === 2 && (await activeTool()) === 'console', 'a terminal opens in a new tile beside')
  assert(!(await page.isVisible('.pane:has(.tile-console) > .tabbar')) && (await page.isVisible('.tile-console [data-testid=tile-close]')), 'a console alone in its tile has no tab bar either')
  // Its corner opens the console in a window of its own; the Git tile opens its tool window.
  const [conWin] = await Promise.all([ctx.waitForEvent('page'), page.click('.tile-console [data-testid=tile-detach]')])
  await conWin.waitForSelector('.detached .xterm')
  assert(new URL(conWin.url()).pathname.includes('/console/'), 'a console tile opens in its own window: ' + conWin.url())
  await conWin.close()
  const [gitWin] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-tool=git] [data-testid=tile-detach]')])
  await gitWin.waitForSelector('.detached .tool-full .panel')
  assert(new URL(gitWin.url()).pathname.endsWith('/tool/git'), 'a tool tile opens in its own window')
  await gitWin.close()
  await page.click('.pane .tile-console .xterm')
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
  await page.waitForTimeout(150)
  const step = w0 - (await width())
  await page.keyboard.press('Shift+ArrowLeft')
  await page.waitForTimeout(200)
  const w1 = await width()
  await page.keyboard.press('Escape')
  await page.waitForSelector('[data-testid=tile-resizing]', { state: 'detached' })
  await page.keyboard.press('ArrowLeft') // a plain key again, not a resize
  await page.waitForTimeout(200)
  assert(Math.abs(step - 12) < 2 && Math.abs(w0 - w1 - 60) < 3 && Math.abs((await width()) - w1) < 2, `resize mode: 12 px per arrow, 48 with Shift, Escape ends it (${w0} → ${w0 - step} → ${w1})`)

  // Closing a tile; the desk is restored after a reload.
  await page.keyboard.press('Alt+w')
  await page.waitForFunction(() => document.querySelectorAll('.pane').length === 2)
  assert(!(await page.$('[data-tool=git]')), 'closing the tab of a tile closes the tile')
  await page.waitForTimeout(800)
  await page.reload()
  await page.waitForSelector('.workbench.grid-desk .pane .tile-console .xterm')
  assert((await panes()) === 2, 'the Grid desk and its tiles are restored after a reload')

  // Back to Full: rails and zones again; the console tile hides and its tool shows in its zone.
  await page.click(`[data-testid=desk-${gridDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Full layout')
  await page.waitForSelector('.rail-left')
  await page.waitForSelector('.zone[data-tool=console] .xterm')
  const shownPanes = () => page.$$eval('.pane', (l) => l.filter((p) => p.getBoundingClientRect().width > 0).length)
  assert((await shownPanes()) === 1 && (await page.isVisible('.pane.active .ed-content')) && !(await page.isVisible('.pane .tile-console')), 'switching to Full hides the console tile, shows the Console tool in its zone and keeps the file')
  assert(!(await page.$('[data-testid=zone-as-tab]')), 'a zone has no Open as a tab button')
  await page.click(`[data-testid=desk-${gridDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Grid of tiles')
  await page.waitForSelector('.workbench.grid-desk .pane .tile-console .xterm')
  assert((await shownPanes()) === 2, 'back to Grid the console tile is there again')

  // ---- Scroll mode: columns side by side, 3 in sight on a desktop, 2 on a tablet.
  await page.click('[data-testid=desk-add]')
  await page.waitForFunction((d) => /\/desk\/\d+$/.test(location.pathname) && !location.pathname.endsWith('/' + d), gridDesk)
  const scrollDesk = path(page).split('/').pop()
  await page.click(`[data-testid=desk-${scrollDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Scrolling columns')
  await page.waitForSelector('[data-testid=scroll-strip]')
  assert(!(await page.$('.rail-left')), 'a Scroll desk has no rails')
  const cols = () => page.$$eval('.scroll-col', (l) => l.length)
  const strip = () => page.$eval('[data-testid=scroll-strip]', (s) => ({ left: s.scrollLeft, width: s.clientWidth }))
  const colOf = () => page.evaluate(() => {
    const c = document.querySelector('.pane.active')?.closest('.scroll-col')
    return c ? { i: [...c.parentElement.children].indexOf(c), width: c.getBoundingClientRect().width, left: c.getBoundingClientRect().left, right: c.getBoundingClientRect().right } : null
  })
  const inSight = () => page.evaluate(() => {
    const s = document.querySelector('[data-testid=scroll-strip]').getBoundingClientRect()
    const c = document.querySelector('.pane.active')?.closest('.scroll-col')?.getBoundingClientRect()
    return !!c && c.left >= s.left - 2 && c.right <= s.right + 2
  })
  await launch('Git')
  await page.waitForSelector('.scroll-col [data-tool=git] .panel')
  await launch('New terminal')
  await page.waitForFunction(() => document.querySelectorAll('.scroll-col .tile-console').length === 1)
  await launch('New terminal')
  await page.waitForFunction(() => document.querySelectorAll('.scroll-col .tile-console').length === 2)
  const third = (await strip()).width / 3
  const c3 = await colOf()
  assert((await cols()) === 3 && Math.abs(c3.width - third) < 2 && (await strip()).left === 0, `three tiles make three columns, each a third of the view, all in sight (${c3.width} of ${(await strip()).width}, scrolled ${(await strip()).left})`)
  await launch('New terminal')
  await page.waitForFunction(() => document.querySelectorAll('.scroll-col').length === 4)
  await page.waitForFunction(() => document.querySelector('[data-testid=scroll-strip]').scrollLeft > 0)
  await page.waitForTimeout(400) // smooth scroll
  assert((await colOf()).i === 3 && (await inSight()), 'a fourth tile opens a column on the right and scrolls it into view')
  await page.waitForFunction(() => document.activeElement?.classList.contains('xterm-helper-textarea') && document.querySelector('.pane.active')?.contains(document.activeElement), null, { timeout: 3000 }).catch(() => {})
  assert(await page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea')), 'the new terminal has the keyboard, not the buttons of its corner')
  assert(await page.isVisible('[data-testid=scroll-overview] .scroll-sight'), 'the overview strip shows when the columns overflow')
  await page.screenshot({ path: OUT + '/desks-scroll.png' })

  // Tablet width: two columns in sight, live.
  await page.setViewportSize({ width: 1000, height: 800 })
  await page.waitForFunction(() => {
    const s = document.querySelector('[data-testid=scroll-strip]')
    return Math.abs(s.querySelector('.scroll-col').getBoundingClientRect().width - s.clientWidth / 2) < 2
  })
  assert(true, 'at 1000 px two columns are in sight')

  // The keyboard focus moves the view back to the first column.
  for (let i = 0; i < 3; i++) await page.keyboard.press('Control+Alt+Shift+ArrowLeft')
  await page.waitForFunction(() => document.querySelector('.pane.active [data-tool=git]'))
  await page.waitForFunction(() => document.querySelector('[data-testid=scroll-strip]').scrollLeft === 0)
  assert((await colOf()).i === 0, 'focusing the column on the left scrolls it into view')

  // Width cycle: a half of the view, then two thirds.
  await page.keyboard.press('Control+Alt+Shift+KeyF')
  await page.waitForFunction(() => {
    const s = document.querySelector('[data-testid=scroll-strip]')
    return Math.abs(document.querySelector('.pane.active').closest('.scroll-col').getBoundingClientRect().width - (s.clientWidth * 2) / 3) < 2
  })
  assert(true, 'Ctrl+Alt+Shift+F gives the column the next width (2/3 of the view)')

  // Move the column right, stack its tile in the column on the left, take it out again.
  await page.keyboard.press('Control+Alt+Shift+PageDown')
  await page.waitForFunction(() => {
    const c = document.querySelector('.pane.active [data-tool=git]')?.closest('.scroll-col')
    return c && [...c.parentElement.children].indexOf(c) === 1
  })
  assert(true, 'Ctrl+Alt+Shift+PageDown moves the column right')
  await page.keyboard.press('Control+Alt+Shift+KeyS')
  await page.waitForFunction(() => document.querySelectorAll('.scroll-col').length === 3 && document.querySelectorAll('.pane').length === 4)
  assert((await colOf()).i === 0 && (await page.$$eval('.scroll-col:first-child .pane', (l) => l.length)) === 2, 'Ctrl+Alt+Shift+S stacks the tile under the column on its left')
  await page.keyboard.press('Control+Alt+Shift+KeyU')
  await page.waitForFunction(() => document.querySelectorAll('.scroll-col').length === 4)
  assert((await colOf()).i === 1 && (await activeTool()) === 'git', 'Ctrl+Alt+Shift+U takes it out to a column of its own')

  // A mark of the overview focuses its column.
  await page.click('[data-testid=scroll-overview] .scroll-mark:nth-child(4)')
  await page.waitForFunction(() => document.querySelector('.scroll-mark:nth-child(4)').classList.contains('active'))
  await page.waitForTimeout(400)
  assert((await colOf()).i === 3 && (await inSight()), 'a mark of the overview brings its column into view')

  // Dragging the right edge of a column widens it.
  const w2 = (await colOf()).width
  const h = await page.$eval('.scroll-col:nth-child(4) > .scroll-handle', (e) => { const r = e.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 } })
  await page.mouse.move(h.x, h.y)
  await page.mouse.down()
  await page.mouse.move(h.x - 120, h.y, { steps: 4 })
  await page.mouse.move(h.x + 100, h.y, { steps: 4 })
  await page.mouse.up()
  assert(Math.abs((await colOf()).width - w2 - 100) < 3, `dragging the edge of a column changes its width (${w2} → ${(await colOf()).width})`)

  // Saved with the desk; Grid and back keeps every tile.
  await page.waitForTimeout(800)
  await page.reload()
  await page.waitForSelector('[data-testid=scroll-strip] .tile-console .xterm')
  assert((await cols()) === 4, 'the Scroll desk and its columns are restored after a reload')
  await page.click(`[data-testid=desk-${scrollDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Grid of tiles')
  await page.waitForSelector('.workbench.grid-desk .split')
  assert(!(await page.$('[data-testid=scroll-strip]')) && (await panes()) === 4, 'switching to Grid keeps the four tiles')
  await page.click(`[data-testid=desk-${scrollDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Scrolling columns')
  await page.waitForSelector('[data-testid=scroll-strip]')
  assert((await cols()) === 4, 'and back to Scroll gives the same columns')

  // Files group, tools stay alone: a file opened from a tool makes a group of files, the next one joins it.
  await page.click('.scroll-col [data-tool=git] .panel')
  await open(page, 'main.go')
  assert((await cols()) === 5 && (await page.$$eval('.pane.active .tab', (l) => l.length)) === 1 && (await page.$$eval('.pane:has([data-tool=git]) > .tabbar .tab', (l) => l.length)) === 1, `a file opened while a tool has the focus opens a group of files beside it (${await cols()} columns, ${await page.$$eval('.pane.active .tab', (l) => l.length)} tabs, ${await page.$$eval('.pane:has([data-tool=git]) > .tabbar .tab', (l) => l.length)} beside Git)`)
  await page.click('.scroll-col [data-tool=git] .panel')
  await open(page, 'notes.txt')
  assert((await cols()) === 5 && (await page.$$eval('.pane.active .tab', (l) => l.length)) === 2, 'the next file joins the group of files')
  await page.click('.pane.active .tab:has-text("notes.txt")', { button: 'right' })
  assert(!(await page.$('.ctx-menu >> text=Open as a tab')), 'no Open as a tab in the tab menu')
  await page.keyboard.press('Escape')

  // The kanban board, a tab among the files in Full, gets a tile of its own back in Scroll.
  await page.click(`[data-testid=desk-${scrollDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Full layout')
  await page.waitForSelector('.rail-left')
  assert((await shownPanes()) === 1 && (await page.$$eval('.pane.active > .tabbar .tab', (l) => l.length)) === 2, 'in Full only the group of files shows')
  await page.click('.rail .rail-btn[title="Kanban"]')
  await page.click('[data-testid=kanban-open-board]')
  await page.waitForFunction(() => document.querySelectorAll('.pane.active > .tabbar .tab').length === 3)
  await page.click(`[data-testid=desk-${scrollDesk}]`, { button: 'right' })
  await page.click('.ctx-menu >> text=Scrolling columns')
  await page.waitForFunction(() => document.querySelectorAll('.scroll-col').length === 6)
  const counts = await page.$$eval('.pane', (l) => l.map((p) => p.querySelectorAll(':scope > .tabbar .tab').length).sort())
  assert(JSON.stringify(counts) === JSON.stringify([1, 1, 1, 1, 1, 2]), 'back in Scroll the kanban board leaves the group of files for a column of its own: ' + JSON.stringify(counts))

  // A console closed from the Console tool leaves its tile a message and a button to close it.
  await launch('Console')
  await page.waitForSelector('.pane.active [data-tool=console] .tab')
  // The consoles are those of the project: close them until a tile of this desk loses its own.
  for (let i = 0; i < 10 && !(await page.$('[data-testid=tile-gone]')); i++) {
    await page.click('.pane.active [data-tool=console] .tab', { button: 'right' })
    await page.click('.ctx-menu >> text=Close')
    await page.waitForTimeout(300)
  }
  await page.waitForSelector('[data-testid=tile-gone]')
  const goneBefore = await page.$$eval('[data-testid=tile-gone]', (l) => l.length)
  const panesBefore = await panes()
  await page.click('[data-testid=tile-gone] button')
  await page.waitForFunction((n) => document.querySelectorAll('.pane').length === n, panesBefore - 1, { timeout: 5000 }).catch(() => {})
  assert((await panes()) === panesBefore - 1 && (await page.$$eval('[data-testid=tile-gone]', (l) => l.length)) === goneBefore - 1, 'the tile of a closed console closes with its button')
})
