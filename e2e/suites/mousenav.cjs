// Side buttons of the mouse (back, forward): they move in the history of the focused part,
// wherever the pointer is: the editor (positions and tabs), the console tabs, the
// conversations of the assistant. The browser never leaves the project.
const http = require('http')
const { run, openProject, open, assert, OUT } = require('../common.cjs')

const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))
const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const msgs = JSON.parse(body).messages
  const last = [...msgs].reverse().find((m) => m.role === 'user')
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const chunk = (delta, reason = null) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: reason }] })}\n\n`)
  chunk({ content: `Answer to ${text(last)}` })
  chunk({}, 'stop')
  res.end('data: [DONE]\n\n')
})

/** A real press of a side button ('back' or 'forward') over the element. */
async function side(page, button, selector) {
  const box = await page.locator(selector).first().boundingBox()
  const x = box.x + box.width / 2
  const y = box.y + box.height / 2
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y })
  await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button, buttons: button === 'back' ? 8 : 16, clickCount: 1 })
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button, buttons: 0, clickCount: 1 })
  await cdp.detach()
  await page.waitForTimeout(300)
}

async function ask(page, message) {
  await page.click('.ai-composer .ed-content')
  await page.fill('.ai-composer .ed-content', message)
  await page.keyboard.press('Control+Enter')
  await page.waitForSelector(`.ai-msg.assistant:not(.live) .md:has-text("Answer to ${message}")`, { timeout: 20000 })
  await page.waitForSelector('[data-testid=send]', { timeout: 10000 })
}

run(async ({ page }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  try {
    await openProject(page)
    const inProject = () => page.evaluate(() => location.pathname.startsWith('/project/'))
    const tab = () => page.evaluate(() => document.querySelector('.pane.active .tab.active')?.textContent ?? '')

    // Editor: opened files and tab clicks are in the history of Ctrl+Alt+Left.
    await open(page, 'main.go')
    await open(page, 'notes.txt')
    await page.click('.pane.active .tab:has-text("main.go")')
    await page.waitForTimeout(200)
    await side(page, 'back', '.pane.active .ed-content')
    assert((await tab()).includes('notes.txt') && (await inProject()), 'back: the tab shown before, the page stays on the project')
    await side(page, 'back', '.pane.active .ed-content')
    assert((await tab()).includes('main.go'), 'back again: the file opened before')
    await side(page, 'forward', '.pane.active .ed-content')
    assert((await tab()).includes('notes.txt'), 'forward: the next one')
    await page.keyboard.press('Control+Alt+ArrowLeft')
    await page.waitForTimeout(200)
    assert((await tab()).includes('main.go'), 'the keyboard shares the same history')

    // Console: two terminals; the focused console moves, even with the pointer on the editor.
    await page.keyboard.press('Alt+F12')
    await page.waitForSelector('.zone-bottomLeft')
    await page.click('.zone-bottomLeft .empty-state button')
    await page.waitForSelector('.zone-bottomLeft .console-tabs .tab')
    await page.click('.zone-bottomLeft button[title^="New terminal"]')
    await page.waitForFunction(() => document.querySelectorAll('.zone-bottomLeft .console-tabs .tab').length === 2)
    const consoleTab = () => page.evaluate(() => [...document.querySelectorAll('.zone-bottomLeft .console-tabs .tab')].findIndex((t) => t.classList.contains('active')))
    const second = await consoleTab()
    await page.click(`.zone-bottomLeft .console-tabs .tab >> nth=${1 - second}`)
    await page.waitForTimeout(200)
    const before = await tab()
    await side(page, 'back', '.pane.active .ed-content')
    assert((await consoleTab()) === second && (await tab()) === before && (await inProject()), 'back with the console focused: the console shown before, the editor unchanged')
    await side(page, 'forward', '.zone-bottomLeft .xterm:visible')
    assert((await consoleTab()) === 1 - second, 'forward: the next console')

    // Assistant: two conversations.
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')
    await ask(page, 'one')
    await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.click('.ai-new-chat')
    if (await page.isVisible('.ai-sidebar')) await page.click('.ai-panel button[title="Conversations of the project"]')
    await ask(page, 'two')
    await page.click('.ai-messages')
    const shows = (m) => page.isVisible(`.ai-msg.assistant .md:has-text("Answer to ${m}")`)
    await side(page, 'back', '.ai-messages')
    assert((await shows('one')) && !(await shows('two')) && (await inProject()), 'back in the assistant: the conversation shown before')
    await side(page, 'forward', '.ai-messages')
    assert((await shows('two')) && !(await shows('one')), 'forward: the next conversation')
    await page.screenshot({ path: OUT + '/mousenav.png' })

    // Elsewhere (the explorer), the side buttons do nothing.
    await page.keyboard.press('Alt+1')
    await page.waitForSelector('.zone-left [role=tree]')
    await side(page, 'back', '.zone-left [role=tree]')
    assert((await inProject()) && (await shows('two')) && (await tab()).includes('main.go'), 'back on the explorer: nothing moves, the page stays')
  } finally {
    fake.close()
  }
})
