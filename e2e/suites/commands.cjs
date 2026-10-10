// Saved commands of the project: quick access of the empty Console, modal list + detail
// (bash editor), ▶ menu, one console per command with Restart in the same tab, ad hoc
// command saved, .ide/commands.json.
const fs = require('fs')
const { run, openProject, assert, OUT, WS } = require('../common.cjs')

const demo = WS + '/demo'
const file = demo + '/.ide/commands.json'
const tabs = (page) => page.$$eval('.console-tabs .tab .tab-title', (l) => l.map((e) => e.textContent))

run(async ({ page }) => {
  // Dialogs (confirm): answered by `answer`, their text kept in `asked`.
  let asked = ''
  let answer = true
  page.on('dialog', (d) => ((asked = d.message()), answer ? d.accept() : d.dismiss()))
  await openProject(page)
  if (!(await page.isVisible('.console-tabs'))) await page.keyboard.press('Alt+F12')
  await page.waitForSelector('[data-testid=quick-commands]')

  // Empty Console without commands: New terminal and Create a command.
  assert(await page.isVisible('[data-testid=quick-commands] button:has-text("New terminal")'), 'empty Console offers a new terminal')
  await page.click('[data-testid=quick-edit-commands]:has-text("Create a command")')
  await page.waitForSelector('.cmd-modal [data-testid=cmd-name]')

  // First command, from the empty state: a long-running server in a sub-folder.
  await page.fill('[data-testid=cmd-name]', 'Dev server')
  await page.selectOption('[data-testid=cmd-flag]', 'serve')
  await page.fill('[data-testid=cmd-description]', 'Prints its folder then waits')
  await page.fill('[data-testid=cmd-cwd]', 'src')
  await page.fill('[data-testid=cmd-port]', '5173')
  await page.click('[data-testid=cmd-command] .ed-content')
  await page.keyboard.type('echo "dev in $(pwd)"\nsleep 300')
  assert(await page.isVisible('[data-testid=cmd-command] .ed-content'), 'command in the editor of the IDE')
  const highlighted = await page.evaluate(() => [...(CSS.highlights?.keys() ?? [])].some((k) => /string|keyword|variable/.test(k)))
  assert(highlighted, 'bash highlighted')

  // Second one, duplicated then changed: a build that ends.
  await page.click('[data-testid=cmd-duplicate]')
  await page.fill('[data-testid=cmd-name]', 'Build')
  await page.selectOption('[data-testid=cmd-flag]', 'build')
  await page.fill('[data-testid=cmd-cwd]', '')
  await page.click('[data-testid=cmd-command] .ed-content')
  await page.keyboard.press('Control+a')
  await page.keyboard.type('echo built-ok')
  // Order: Alt+↑ moves Build first.
  await page.click('.cmd-item.selected')
  await page.keyboard.press('Alt+ArrowUp')
  const names = () => page.$$eval('[data-testid=cmd-item] .cmd-item-name', (l) => l.map((e) => e.textContent))
  assert((await names()).join() === 'Build,Dev server', 'Alt+↑ reorders: ' + (await names()))
  await page.screenshot({ path: OUT + '/commands-modal.png' })
  await page.click('[data-testid=cmd-save]')
  await page.waitForSelector('[data-testid=cmd-save]:disabled')
  const saved = JSON.parse(fs.readFileSync(file, 'utf8')).commands
  assert(saved.length === 2 && saved[0].name === 'Build' && !saved[0].port && saved[1].cwd === 'src' && saved[1].port === 5173 && saved[1].command === 'echo "dev in $(pwd)"\nsleep 300', 'saved in .ide/commands.json: ' + JSON.stringify(saved))
  assert(fs.readFileSync(demo + '/.ide/.gitignore', 'utf8').includes('*'), '.ide ignored by git')

  // Unsaved changes: closing asks.
  await page.fill('[data-testid=cmd-name]', 'Build!')
  answer = false
  await page.keyboard.press('Escape')
  await page.waitForTimeout(200)
  assert(/unsaved/.test(asked) && (await page.isVisible('.cmd-modal')), 'closing with unsaved changes asks')
  answer = true
  await page.click('.cmd-modal .modal-foot button:has-text("Cancel")')
  await page.waitForSelector('.cmd-modal', { state: 'detached' })

  // Quick access: the commands of the project, Enter runs.
  await page.waitForSelector('[data-testid=quick-command]:has-text("Dev server")')
  assert((await page.$$eval('[data-testid=quick-command] .quick-cmd-name', (l) => l.map((e) => e.textContent))).join() === 'Build,Dev server', 'quick access in the saved order')
  await page.focus('[data-testid=quick-command]:has-text("Build")')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.console-tabs .tab.active:has-text("Dev server")')
  await page.waitForFunction(() => /dev in .*\/demo\/src/.test(document.querySelector('.term-slot[style*="block"] .xterm-rows')?.textContent ?? ''), null, { timeout: 10000 })
  assert(true, 'the command runs in its folder')

  // ▶ menu: running dot; Build runs in a second tab.
  await page.click('[data-testid=console-commands]')
  await page.waitForSelector('.ctx-menu')
  assert(await page.isVisible('.ctx-item:has-text("Dev server"):has-text("●")'), 'running command marked in the menu')
  assert(await page.isVisible('.ctx-item:has-text("Open “Dev server” in a preview"):has-text(":5173")'), 'a serve command with a port opens as a preview')
  assert(!(await page.isVisible('.ctx-item:has-text("Open “Build”")')), 'not a build command')
  await page.click('.ctx-item:has-text("Build")')
  await page.waitForSelector('.console-tabs .tab.active:has-text("Build")')
  await page.waitForSelector('.console-tabs .tab:has-text("Build") .ok')
  assert((await tabs(page)).join() === 'Dev server,Build', 'tabs: ' + (await tabs(page)))

  // Running again: Restart offered; the console keeps the place of its tab.
  asked = ''
  await page.click('[data-testid=console-commands]')
  await page.click('.ctx-item:has-text("Dev server")')
  await page.waitForFunction(() => document.querySelectorAll('.console-tabs .tab').length === 2 && document.querySelector('.console-tabs .tab.active')?.textContent.includes('Dev server'))
  assert(/Restart/.test(asked), 'Restart asked: ' + asked)
  assert((await tabs(page)).join() === 'Dev server,Build', 'restarted in the same tab: ' + (await tabs(page)))
  // An ended command runs again without asking, in its tab.
  asked = ''
  await page.click('[data-testid=console-commands]')
  await page.click('.ctx-item:has-text("Build")')
  await page.waitForSelector('.console-tabs .tab.active:has-text("Build")')
  await page.waitForTimeout(300)
  assert(asked === '' && (await tabs(page)).length === 2, 'an ended command restarts without asking')

  // Ad hoc command saved as a command.
  await page.click('[data-testid=console-commands]')
  await page.click('.ctx-item:has-text("Run a command…")')
  await page.fill('.modal input', 'echo adhoc-1')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.console-tabs .tab.active:has-text("echo adhoc-1")')
  await page.click('[data-testid=console-commands]')
  await page.click('.ctx-item:has-text("as a command")')
  await page.waitForSelector('.cmd-modal [data-testid=cmd-name]')
  assert((await page.inputValue('[data-testid=cmd-name]')) === 'echo adhoc-1', 'draft named after the command')
  await page.click('[data-testid=cmd-run]')
  await page.waitForSelector('.cmd-modal', { state: 'detached' })
  await page.waitForSelector('.console-tabs .tab.active:has-text("echo adhoc-1") .ok')
  assert(JSON.parse(fs.readFileSync(file, 'utf8')).commands.length === 3, 'Save and run saved the third command')

  // Delete from the modal.
  await page.click('[data-testid=console-commands]')
  await page.click('.ctx-item:has-text("Edit commands…")')
  await page.click('[data-testid=cmd-item]:has-text("Dev server")')
  assert(await page.isVisible('[data-testid=cmd-open-app]'), 'Start and open the app offered for a serve command with a port')
  await page.click('[data-testid=cmd-item]:has-text("echo adhoc-1")')
  assert(!(await page.isVisible('[data-testid=cmd-open-app]')), 'not for another command')
  await page.click('[data-testid=cmd-delete]')
  await page.click('[data-testid=cmd-save]')
  await page.waitForSelector('[data-testid=cmd-save]:disabled')
  assert(JSON.parse(fs.readFileSync(file, 'utf8')).commands.length === 2, 'command deleted')
  await page.screenshot({ path: OUT + '/commands-console.png' })
})
