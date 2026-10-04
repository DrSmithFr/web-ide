// Popups: Search Everywhere (tabs over files, symbols, actions and text).
const { run, openProject, open, assert, OUT } = require('../common.cjs')

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
})
