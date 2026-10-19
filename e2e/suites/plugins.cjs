// Plugins (#113): the test plugin hello (WEBIDE_TEST_PLUGINS) adds a tool in the right zone, an
// RPC method and an assistant tool of the MCP; disabled on the project in Settings > Plugins,
// its icon, its command and its tool go, its place in the zone stays. Docker (#114) is a
// built-in plugin: disabled, its tools go, the Port forwards and previews tool stays. The
// Database explorer (#115) too: its tabs are views of it, a console keeps its text in its tab,
// and a desk saved before (sql tabs and sqlText) is migrated. Exec (#116) provides terminal:
// off, the Console goes and Docker with it (it needs terminal); on again, the terminals are back.
// Search (#117) gives Search Everywhere its Text tab: off, the tab and the Search tool go. Git
// (#118) gives the diff view its git sides: off, a diff tab says so, open_diff is refused.
// Previews (#119) needs terminal: off (or Exec off), its tool and list_previews go.
const fs = require('fs')
const path = require('path')
const { execSync } = require('child_process')
const { run, openProject, assert, mcp, WS } = require('../common.cjs')

const text = (r) => r.result?.content?.[0]?.text ?? JSON.stringify(r)
const tools = () =>
  fetch(process.env.E2E_URL + '/mcp', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + process.env.E2E_TOKEN },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' }),
  })
    .then((r) => r.json())
    .then((r) => r.result.tools.map((t) => t.name))

run(async ({ page }) => {
  await openProject(page)
  const icon = '.rail-group[data-zone=right] .rail-btn[data-id=hello]'

  // The tool of the plugin, after those of the core in its zone, renders what its backend answers.
  await page.waitForSelector(icon)
  const ids = await page.$$eval('.rail-group[data-zone=right] .rail-btn', (l) => l.map((b) => b.dataset.id))
  assert(ids.at(-1) === 'hello', 'the plugin tool comes last in its zone: ' + ids)
  await page.click(icon)
  await page.waitForFunction(() => document.querySelector('[data-testid=hello-text]')?.textContent === 'Hello plugin from demo')

  // Its assistant tool in the MCP, run on the project of the cwd.
  assert((await tools()).includes('hello_greet'), 'hello_greet listed by the MCP')
  let r = await mcp('hello_greet', { name: 'Claude' })
  assert(!r.result.isError && text(r) === 'Hello Claude from demo', 'hello_greet: ' + text(r))

  // Its command in Search Everywhere.
  const palette = async (q) => {
    await page.waitForTimeout(300)
    await page.keyboard.press('Control+Shift+a')
    await page.click('.pick.se .pick-input')
    await page.keyboard.press('Control+a')
    await page.keyboard.type(q)
  }
  // Settings closed by their button (the focus may be on the page after a change).
  const closeSettings = async () => {
    await page.click('.modal-settings .modal-head button[title^="Close"]')
    await page.waitForSelector('.modal-settings', { state: 'detached' })
  }
  await palette('hello')
  await page.waitForSelector('.pick-item:has-text("Hello")')
  await page.keyboard.press('Escape')

  // Disabled in Settings > Plugins: the icon goes, the MCP refuses the tool on this project.
  await palette('settings')
  await page.waitForSelector('.pick-item.selected:has-text("Settings")')
  await page.keyboard.press('Enter')
  await page.click('.settings-nav button:has-text("Plugins")')
  await page.uncheck('[data-testid=plugin-hello]')
  await page.waitForSelector(icon, { state: 'detached' })
  const saved = JSON.parse(fs.readFileSync(WS + '/demo/.ide/plugins.json', 'utf8'))
  assert(JSON.stringify(saved.disabled) === '["hello"]', 'disabled in .ide/plugins.json: ' + JSON.stringify(saved))
  r = await mcp('hello_greet', {})
  assert(r.result.isError && text(r).includes('the Hello plugin is disabled on this project'), 'refused when disabled: ' + text(r))
  await closeSettings()

  // Still off after a reload.
  await page.reload()
  await page.waitForSelector('.rail-group[data-zone=right] .rail-btn[data-id=info]')
  assert(!(await page.$(icon)), 'still disabled after a reload')

  // Enabled again: back at its place.
  await palette('settings')
  await page.waitForSelector('.pick-item.selected:has-text("Settings")')
  await page.keyboard.press('Enter')
  await page.click('.settings-nav button:has-text("Plugins")')
  await page.check('[data-testid=plugin-hello]')
  await page.waitForSelector(icon)
  r = await mcp('hello_greet', {})
  assert(!r.result.isError, 'enabled again: ' + text(r))
  await closeSettings()

  // Docker: its tab chosen is kept in the desk; disabled, it goes with its assistant tools and
  // its command, the Port forwards and previews tool stays; enabled again, it is back on its tab.
  const docker = '.rail-group[data-zone=bottomRight] .rail-btn[data-id=docker]'
  const forwards = '.rail-group[data-zone=bottomRight] .rail-btn[data-id=forwards]'
  await page.waitForSelector(forwards)
  await page.click(docker)
  await page.click('[data-testid=docker-tab-disk]')
  assert((await tools()).includes('docker_ps'), 'docker_ps listed by the MCP')
  const setPlugin = async (id, on) => {
    await palette('settings')
    await page.waitForSelector('.pick-item.selected:has-text("Settings")')
    await page.keyboard.press('Enter')
    await page.click('.settings-nav button:has-text("Plugins")')
    await (on ? page.check(`[data-testid=plugin-${id}]`) : page.uncheck(`[data-testid=plugin-${id}]`))
    await closeSettings()
  }
  const setDocker = (on) => setPlugin('docker', on)
  await setDocker(false)
  await page.waitForSelector(docker, { state: 'detached' })
  await page.click(forwards)
  await page.waitForSelector('[data-testid=forwards-tab-previews][aria-selected=true]')
  r = await mcp('docker_ps', {})
  assert(r.result.isError && text(r).includes('the Docker plugin is disabled on this project'), 'docker_ps refused: ' + text(r))
  await palette('docker')
  await page.waitForTimeout(300)
  assert(!(await page.$('.pick-item:has-text("Docker")')), 'no Docker command in Search Everywhere')
  await page.keyboard.press('Escape')
  await setDocker(true)
  await page.click(docker)
  await page.waitForSelector('[data-testid=docker-tab-disk][aria-selected=true]')
  r = await mcp('docker_ps', {})
  assert(!text(r).includes('disabled'), 'docker_ps back: ' + text(r))

  // Database explorer: a console on a SQLite base, its text kept in its tab.
  await page.click('.rail-btn[data-id=database]')
  await page.waitForSelector('.db-tool')
  await page.click('.db-tool .empty-state button:has-text("Add a connection")')
  await page.click('.conn-new button:has-text("SQLite")')
  await page.fill('.conn-form input[placeholder="var/data.db"]', 'plugins.db')
  await page.click('.conn-form button:has-text("Save")')
  await page.waitForTimeout(400)
  await page.click('.modal-head .icon-btn')
  await page.click('.conn-row')
  await page.click('.db-tool button[title^="Open a console"]')
  await page.waitForSelector('.sql-console .ed-content')
  await page.click('.sql-console .ed-content')
  await page.keyboard.type('SELECT 42 AS answer;')
  await page.keyboard.press('Control+Enter')
  await page.waitForFunction(() => document.querySelector('.sql-result .grid')?.textContent.includes('42'))
  await page.waitForTimeout(800) // the text is saved after 500 ms
  const consoleText = () => page.textContent('.sql-console .ed-content')

  // Disabled: no icon, the console tab says its plugin is off; enabled again, the text is back.
  await setPlugin('database', false)
  await page.waitForSelector('.rail-btn[data-id=database]', { state: 'detached' })
  await page.waitForSelector('.pane:has-text("The plugin of this tab is disabled")')
  await setPlugin('database', true)
  await page.waitForSelector('.sql-console .ed-content')
  assert((await consoleText()).includes('SELECT 42 AS answer;'), 'the console keeps its text: ' + (await consoleText()))

  // A desk saved before #115 (a sql tab, its text in sqlText) opens as a console of the plugin.
  const dir = path.join(process.env.E2E_DATA, 'desks')
  const file = fs.readdirSync(dir).map((f) => path.join(dir, f)).find((f) => fs.readFileSync(f, 'utf8').includes('plugin:database/sql'))
  const pid = fs.readFileSync(path.join(process.env.E2E_TMP, 'pod.pid'), 'utf8').trim()
  execSync(`kill -TERM ${pid}; while kill -0 ${pid} 2>/dev/null; do sleep 0.1; done`)
  const desk = JSON.parse(fs.readFileSync(file, 'utf8'))
  const sess = desk.session ?? desk
  sess.sqlText = {}
  for (const t of Object.values(sess.tabs)) {
    if (t.kind !== 'plugin:database/sql') continue
    Object.assign(t, { kind: 'sql', connId: t.state.connId, db: t.state.db })
    sess.sqlText[t.id] = 'SELECT 7; -- before #115'
    delete t.state
    delete t.key
  }
  fs.writeFileSync(file, JSON.stringify(desk))
  execSync(`bash ${path.join(process.env.E2E_TMP, 'launch-pod.sh')}`)
  await page.reload()
  await page.waitForSelector('.sql-console .ed-content', { timeout: 15000 })
  assert((await consoleText()).includes('SELECT 7; -- before #115'), 'a console of an old desk keeps its text: ' + (await consoleText()))

  // Exec: a terminal, then Exec off (its dependents named in a confirmation): no Console, Docker
  // off for want of terminal, the console tools of the MCP refused.
  page.on('dialog', (d) => d.accept())
  await page.click('.pane.active .ed-content').catch(() => {})
  await page.keyboard.press('Control+Shift+Backquote')
  await page.waitForSelector('.xterm-helper-textarea')
  await page.waitForTimeout(500)
  await page.keyboard.type('echo plugin-$((6*7))')
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.querySelector('.xterm-rows')?.textContent.includes('plugin-42'))
  const consoleIcon = '.rail-btn[data-id=console]'
  await setPlugin('exec', false)
  await page.waitForSelector(consoleIcon, { state: 'detached' })
  assert(!(await page.$(docker)), 'Docker off with Exec')
  assert(!(await page.$(forwards)), 'Previews off with Exec')
  await palette('settings')
  await page.waitForSelector('.pick-item.selected:has-text("Settings")')
  await page.keyboard.press('Enter')
  await page.click('.settings-nav button:has-text("Plugins")')
  assert((await page.textContent('[data-plugin=docker]')).includes('needs terminal'), 'Docker says what it lacks')
  await closeSettings()
  r = await mcp('list_consoles', {})
  assert(r.result.isError && text(r).includes('terminal'), 'list_consoles refused: ' + text(r))
  await setPlugin('exec', true)
  // The Console comes back shown, as it was.
  await page.waitForSelector(consoleIcon)
  if (!(await page.isVisible('.xterm-rows'))) await page.click(consoleIcon)
  await page.waitForFunction(() => document.querySelector('.xterm-rows')?.textContent.includes('plugin-42'))
  assert(await page.$(docker), 'Docker back with Exec')

  // Search: its tool in the left zone, before Git; the Text tab of Search Everywhere is its.
  const seTabs = async () => {
    await palette('x')
    await page.waitForSelector('.pick.se')
    const list = await page.$$eval('.se-tab', (l) => l.map((b) => b.textContent))
    await page.keyboard.press('Escape')
    return list
  }
  const left = await page.$$eval('.rail-group[data-zone=left] .rail-btn', (l) => l.map((b) => b.dataset.id))
  assert(left.indexOf('search') === left.indexOf('git') - 1, 'Search before Git: ' + left)
  assert(JSON.stringify(await seTabs()) === JSON.stringify(['All', 'Files', 'Symbols', 'Actions', 'Text']), 'the tabs of Search Everywhere')
  await setPlugin('search', false)
  await page.waitForSelector('.rail-btn[data-id=search]', { state: 'detached' })
  assert(JSON.stringify(await seTabs()) === JSON.stringify(['All', 'Files', 'Symbols', 'Actions']), 'no Text tab with Search off')
  await palette('notes')
  await page.keyboard.press('Shift+Tab') // Symbols
  await page.keyboard.press('Shift+Tab') // Files
  await page.waitForSelector('.se-tab.active:has-text("Files")')
  await page.waitForSelector('.pick-item:has-text("notes.txt")')
  await page.keyboard.press('Escape')
  await setPlugin('search', true)
  await page.waitForSelector('.rail-btn[data-id=search]')
  assert((await seTabs()).includes('Text'), 'the Text tab is back')

  // Git: a diff tab of a change, then Git off: no Git tool, the tab falls back, open_diff refused.
  fs.appendFileSync(WS + '/demo/notes.txt', 'a line for the diff\n')
  r = await mcp('open_diff', { path: 'notes.txt' })
  assert(!r.result.isError, 'open_diff: ' + text(r))
  await page.waitForSelector('.diff-view .diff-line.add:has-text("a line for the diff")')
  assert((await page.textContent('.diff-view .toolbar')).includes('working tree'), 'the label of the git source')
  await setPlugin('git', false)
  await page.waitForSelector('.rail-btn[data-id=git]', { state: 'detached' })
  await page.waitForSelector('.pane:has-text("The plugin of this tab is disabled")')
  r = await mcp('open_diff', { path: 'notes.txt' })
  assert(r.result.isError && text(r).includes('Git plugin is off'), 'open_diff refused: ' + text(r))
  await setPlugin('git', true)
  await page.waitForSelector('.diff-view .diff-line.add:has-text("a line for the diff")')
  await page.waitForSelector('.rail-btn[data-id=git]')

  // Previews: off, its tool goes and list_previews is refused; on again, both are back.
  assert(!text(await mcp('list_previews', {})).includes('terminal'), 'list_previews answers')
  await setPlugin('previews', false)
  await page.waitForSelector(forwards, { state: 'detached' })
  r = await mcp('list_previews', {})
  assert(r.result.isError && text(r).includes('previews'), 'list_previews refused: ' + text(r))
  await setPlugin('previews', true)
  await page.waitForSelector(forwards)
  assert(!(await mcp('list_previews', {})).result.isError, 'list_previews back')
})
