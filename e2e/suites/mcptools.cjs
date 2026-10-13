// Tools of the IDE in its MCP (#68), called as a CLI agent does: a file and a diff opened in
// the window, a message, a preview offered, the consoles, and a doodle asked to the user that
// comes back as an image.
const fs = require('fs')
const { run, openProject, assert, mcp, WS, OUT } = require('../common.cjs')

const text = (r) => r.result?.content?.[0]?.text ?? JSON.stringify(r)

run(async ({ page }) => {
  await openProject(page)

  // open_file: the file shows in the editor, at its line.
  let r = await mcp('open_file', { path: 'notes.txt', line: 1 })
  assert(!r.result.isError && text(r).includes('notes.txt opened in the editor'), 'open_file answered by the window: ' + text(r))
  await page.waitForFunction(() => document.querySelector('.pane.active .tab.active')?.textContent.includes('notes.txt'))

  // open_diff: the uncommitted change of a tracked file in the diff view.
  fs.appendFileSync(WS + '/demo/notes.txt', 'a line added by the agent\n')
  r = await mcp('open_diff', { path: 'notes.txt' })
  assert(!r.result.isError, 'open_diff: ' + text(r))
  await page.waitForFunction(() => document.querySelector('.pane.active .tab.active')?.textContent.includes('notes.txt (changes)'))

  // show_message: a notification.
  r = await mcp('show_message', { text: 'Tests are green', kind: 'ok' })
  assert(!r.result.isError, 'show_message: ' + text(r))
  await page.waitForSelector('.toast-ok:has-text("Tests are green")')

  // share_preview: offered with a button, nothing started.
  r = await mcp('share_preview', { title: 'Demo app', command: 'sleep 60', port: 39555 })
  assert(text(r).includes('Do not run the command yourself'), 'share_preview: ' + text(r))
  await page.waitForSelector('.toast:has-text("An agent offers to try “Demo app”") button:has-text("Open")')
  assert(text(await mcp('list_previews')) === 'No preview runs for this project.', 'nothing runs before the click')

  // Consoles.
  assert(text(await mcp('list_consoles')).length > 0, 'list_consoles answers')

  // request_doodle: the editor opens on the project window, the drawing comes back.
  const asked = mcp('request_doodle', { title: 'Layout of the page' })
  await page.waitForSelector('[data-testid=doodle] .dd-head h2:has-text("Layout of the page")')
  assert((await page.textContent('[data-testid=dd-attach]')).includes('Send to the agent'), 'the attach button sends to the agent')
  const b = await page.locator('[data-testid=dd-canvas]').boundingBox()
  await page.mouse.move(b.x + b.width * 0.2, b.y + b.height * 0.2)
  await page.mouse.down()
  for (let i = 1; i <= 12; i++) await page.mouse.move(b.x + b.width * (0.2 + (0.5 * i) / 12), b.y + b.height * (0.2 + (0.4 * i) / 12))
  await page.mouse.up()
  await page.screenshot({ path: OUT + '/mcp-doodle.png' })
  await page.click('[data-testid=dd-attach]')
  r = await asked
  const [desc, img] = r.result.content
  assert(desc.text.includes('Doodle "Layout of the page" drawn by the user'), 'description of the drawing: ' + desc.text)
  assert(img.type === 'image' && img.mimeType === 'image/png' && img.data.length > 100, 'the drawing as a PNG')
  await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })

  // Closed without sending.
  const closed = mcp('request_doodle', { title: 'Another one' })
  await page.waitForSelector('[data-testid=doodle] .dd-head h2:has-text("Another one")')
  await page.click('[data-testid=dd-close]')
  assert(text(await closed) === 'The user closed the doodle without sending it.', 'closing answers the agent')
})
