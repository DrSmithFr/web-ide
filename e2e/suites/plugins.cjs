// Plugins (#113): the test plugin hello (WEBIDE_TEST_PLUGINS) adds a tool in the right zone, an
// RPC method and an assistant tool of the MCP; disabled on the project in Settings > Plugins,
// its icon, its command and its tool go, its place in the zone stays.
const fs = require('fs')
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
  await page.keyboard.press('Escape')

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
})
