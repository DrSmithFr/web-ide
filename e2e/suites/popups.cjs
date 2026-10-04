// Popups: Search Everywhere (tabs over files, symbols, actions and text), Recent Files and
// the switcher.
const { execSync } = require('child_process')
const { run, openProject, open, assert, OUT, WS } = require('../common.cjs')

run(async ({ page }) => {
  await openProject(page)
  await open(page, 'main.go')
  const tab = () => page.textContent('.se-tab.active')
  const sections = () => page.$$eval('.se-section', (l) => l.map((e) => e.textContent))

  // Double Shift: All, with the files and the actions in their sections.
  await page.click('.pane .ed-content')
  await page.keyboard.press('Shift')
  await page.keyboard.press('Shift')
  await page.waitForSelector('.pick.se')
  assert((await tab()) === 'All', 'double Shift opens Search Everywhere on All')
  await page.keyboard.type('app')
  await page.waitForSelector('.pick-item:has-text("app.php")')
  await page.keyboard.press('Control+a')
  await page.keyboard.type('split')
  await page.waitForSelector('.pick-item:has-text("Split right")')
  assert((await sections()).includes('Actions'), 'All shows the actions in their section ' + (await sections()))
  await page.screenshot({ path: OUT + '/popups-se-all.png' })

  // Tab / Shift+Tab change the tab; the query stays.
  await page.keyboard.press('Tab')
  assert((await tab()) === 'Files', 'Tab: Files tab')
  await page.keyboard.press('Shift+Tab')
  await page.keyboard.press('Shift+Tab')
  assert((await tab()) === 'Text', 'Shift+Tab wraps around to Text')
  await page.keyboard.press('Control+a')
  await page.keyboard.type('Println')
  await page.waitForSelector('.pick-item.selected mark:has-text("Println")')
  assert((await page.textContent('.pick-item.selected .pick-detail')).includes('main.go:13'), 'text match with its file and line')
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.querySelector('[data-testid=status-cursor]')?.textContent.startsWith('13:'))
  assert(!(await page.$('.pick.se')), 'Enter opens the match and closes the popup')

  // The palette and Go to file open their own tab, the previous query selected.
  await page.keyboard.press('Control+Shift+a')
  await page.waitForSelector('.pick.se')
  assert((await tab()) === 'Actions', 'the command palette is the Actions tab')
  assert((await page.evaluate(() => getSelection().toString() || document.activeElement.value.slice(document.activeElement.selectionStart, document.activeElement.selectionEnd))) === 'Println', 'previous query selected')
  await page.keyboard.press('Control+Shift+n')
  assert((await tab()) === 'Files', 'Go to file switches the open popup to Files')
  await page.keyboard.press('Escape')
  assert(!(await page.$('.pick.se')), 'Escape closes the popup')

  // Navigate menu: the entry with its double Shift hint.
  await page.click('.menubar .menu-btn:has-text("Navigate")')
  assert(await page.isVisible('.ctx-item:has-text("Search everywhere") kbd:has-text("Double Shift")'), 'Navigate menu: Search everywhere, double Shift')
  await page.keyboard.press('Escape')
  // Shift+letter while typing does not open it.
  await page.click('.pane .ed-content')
  await page.keyboard.type('AB')
  await page.waitForTimeout(100)
  assert(!(await page.$('.pick.se')), 'Shift with letters does not open the popup')
  await page.keyboard.press('Control+z')

  // Recent Files: the files last shown, the previous one selected.
  await open(page, 'app.php')
  await open(page, 'notes.txt')
  const files = () => page.$$eval('.rf-files .pick-label', (l) => l.map((e) => e.textContent))
  await page.keyboard.press('Control+e')
  await page.waitForSelector('.pick.rf')
  assert(JSON.stringify((await files()).slice(0, 3)) === JSON.stringify(['notes.txt', 'app.php', 'main.go']), 'recent files, most recent first ' + (await files()))
  assert((await page.textContent('.rf-files .pick-item.selected')).includes('app.php') && (await page.textContent('.rf-foot')) === 'src/app.php', 'the previous file is selected, its path below')
  assert(await page.isVisible('.rf-tools .pick-item:has-text("Explorer") kbd:has-text("Alt+1")'), 'the tools with their shortcut on the left')
  await page.screenshot({ path: OUT + '/popups-recent.png' })
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.querySelector('.pane.active .tab.active')?.textContent.includes('app.php'))
  assert(!(await page.$('.pick.rf')), 'Enter opens the file')

  // Left: the tools; a filter matching only a tool selects it.
  await page.keyboard.press('Control+e')
  await page.keyboard.press('ArrowLeft')
  assert(await page.isVisible('.rf-tools .pick-item.selected'), 'ArrowLeft selects a tool')
  await page.keyboard.press('ArrowRight')
  await page.keyboard.type('Docker')
  await page.waitForSelector('.rf-tools .pick-item.selected:has-text("Docker")')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.zone[data-tool=docker]')
  assert(true, 'a tool chosen in Recent Files is shown')
  await page.keyboard.press('Alt+9')
  await page.waitForFunction(() => !document.querySelector('.zone[data-tool=docker]'))

  // Ctrl+E again: the changed files only (unsaved, or changed for git).
  execSync('git add -A && git -c user.name=e2e -c user.email=e2e@x commit -qm init', { cwd: WS + '/demo' })
  // A commit made outside the IDE is seen when the window gets the focus back.
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await page.waitForFunction(() => !document.querySelector('.tree-name.git-untracked'))
  await page.click('.pane .ed-content')
  await page.keyboard.type('x')
  await page.keyboard.press('Control+e')
  await page.keyboard.press('Control+e')
  await page.waitForSelector('.rf-title:has-text("Recently changed files")')
  assert(JSON.stringify(await files()) === JSON.stringify(['app.php']) && (await page.isVisible('.rf-dirty')), 'changed files only: the unsaved one ' + (await files()))
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+z')

  // Delete forgets a recent file.
  await page.keyboard.press('Control+e')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Delete')
  assert(!(await files()).includes('main.go') && (await files()).length === 2, 'Delete removes main.go from the recent files ' + (await files()))
  await page.keyboard.press('Escape')
  await open(page, 'main.go')

  // Switcher: Ctrl held, Tab moves down, releasing Ctrl opens the selection.
  await page.keyboard.down('Control')
  await page.keyboard.press('Tab')
  await page.waitForSelector('.pick.rf')
  await page.keyboard.press('Tab')
  assert((await page.textContent('.rf-files .pick-item.selected')).includes('notes.txt'), 'the switcher moves down while Ctrl is held')
  await page.keyboard.up('Control')
  await page.waitForFunction(() => document.querySelector('.pane.active .tab.active')?.textContent.includes('notes.txt'))
  assert(!(await page.$('.pick.rf')), 'releasing Ctrl opens the selected file')

  // The recent files are kept in the session.
  await page.reload()
  await page.waitForSelector('.pane .ed-content')
  await page.keyboard.press('Control+e')
  await page.waitForSelector('.pick.rf')
  assert(JSON.stringify(await files()) === JSON.stringify(['notes.txt', 'main.go', 'app.php']), 'recent files kept after a reload ' + (await files()))
  await page.keyboard.press('Escape')
})
