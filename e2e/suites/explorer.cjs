// File explorer: file type icons, click to select and double click to open, git colors
// (untracked, added, ignored), folder marks, expand/collapse all, locate, options.
const fs = require('fs')
const { execFileSync } = require('child_process')
const { run, openProject, open, assert, WS, OUT } = require('../common.cjs')

run(async ({ page }) => {
  const demo = WS + '/demo'
  fs.writeFileSync(demo + '/.gitignore', '*.log\n')
  fs.writeFileSync(demo + '/build.log', 'log\n')
  fs.mkdirSync(demo + '/gen')
  fs.writeFileSync(demo + '/gen/generated.go', 'package gen\n')
  fs.mkdirSync(demo + '/tests')
  fs.writeFileSync(demo + '/tests/app_test.go', 'package tests\n')
  await openProject(page)
  await page.waitForSelector('.explorer .tree-row')
  const row = (name) => `.explorer .tree-row[data-path="${demo}/${name}"]`

  // File type icons.
  await page.click(row('src'))
  await page.dblclick(row('src'))
  await page.waitForSelector(row('src/main.go'))
  const kind = (name) => page.getAttribute(row(name) + ' .file-icon', 'data-kind')
  assert((await kind('src/main.go')) === 'GO' && (await kind('src/app.php')) === 'PHP' && (await kind('go.mod')) === 'GO', 'language badges')
  assert((await kind('notes.txt')) === 'line' && (await kind('.gitignore')) === 'line' && (await kind('gen')) === 'folder', 'other recognized files and folders')

  // A click selects, a double click opens and focuses the editor.
  await page.click(row('notes.txt'))
  assert(await page.isVisible(row('notes.txt') + '.selected'), 'click selects the row')
  assert(!(await page.isVisible('.pane.active .tab:has-text("notes.txt")')), 'click does not open the file')
  await page.dblclick(row('notes.txt'))
  await page.waitForSelector('.pane.active .tab.active:has-text("notes.txt")')
  await page.waitForFunction(() => document.activeElement?.classList.contains('ed-content'))
  assert(true, 'double click opens the file and focuses the editor')

  // Git colors: untracked red, added green, ignored.
  assert(await page.isVisible(row('notes.txt') + ' .tree-name.git-untracked'), 'untracked file')
  assert(await page.isVisible(row('build.log') + ' .tree-name.git-ignored'), 'file ignored by .gitignore')
  execFileSync('git', ['add', 'go.mod'], { cwd: demo })
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await page.waitForSelector(row('go.mod') + ' .tree-name.git-added', { timeout: 10000 })
  assert(true, 'added file')
  assert(await page.isVisible(row('src') + ' .tree-name.git-untracked'), 'folder of untracked files')

  // Folder marks.
  const mark = async (name, label) => {
    await page.click(row(name), { button: 'right' })
    await page.click(`.ctx-menu .ctx-item:has-text("${label}")`)
    await page.waitForSelector(row(name) + `[data-mark]`)
  }
  await mark('gen', 'Excluded folder')
  await mark('tests', 'Test folder')
  await mark('src', 'Source folder')
  const marks = JSON.parse(fs.readFileSync(demo + '/.ide/folders.json', 'utf8')).folders
  assert(marks.gen === 'excluded' && marks.tests === 'tests' && marks.src === 'source', 'marks saved in .ide/folders.json')
  assert(await page.isVisible(row('gen') + '.excluded .file-icon.mark-excluded'), 'excluded folder dimmed, orange icon')
  await page.keyboard.press('Control+Shift+n')
  await page.waitForSelector('.pick-input')
  await page.keyboard.type('generated')
  await page.waitForTimeout(500)
  assert(!(await page.isVisible('.pick-item:has-text("generated.go")')), 'excluded folder left out of "go to file"')
  await page.keyboard.press('Escape')

  // Expand all (not the excluded folder), collapse all.
  await page.click('[data-testid=explorer-expand]')
  await page.waitForSelector(row('tests/app_test.go'))
  assert(!(await page.isVisible(row('gen/generated.go'))), 'expand all skips the excluded folder')
  await page.screenshot({ path: OUT + '/explorer.png' })
  await page.click('[data-testid=explorer-collapse]')
  await page.waitForFunction(() => !document.querySelector('.explorer .tree-row[aria-expanded=true]'))
  assert(true, 'collapse all')

  // Locate the active file.
  await open(page, 'main.go')
  await page.click('[data-testid=explorer-locate]')
  await page.waitForSelector(row('src/main.go') + '.selected')
  assert(true, 'locate opens the folders and selects the file')

  // Options.
  const option = async (label) => {
    await page.click('[data-testid=explorer-options]')
    await page.click(`.ctx-menu .ctx-item:has-text("${label}")`)
  }
  await option('Show the hidden files')
  await page.waitForSelector(row('.gitignore'), { state: 'detached' })
  assert(true, 'hidden files hidden')
  await option('Show the excluded folders')
  await page.waitForSelector(row('gen'), { state: 'detached' })
  assert(true, 'excluded folders hidden')
  await option('Open files with a single click')
  await page.click(row('go.mod'))
  await page.waitForSelector('.pane.active .tab.active:has-text("go.mod")')
  assert(true, 'single click option opens the file')
})
