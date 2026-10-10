// AI assistant, conversation features: side bar order, Ctrl+click on @file, queue of
// messages (after an answer and between tool steps), message edited in place (attachments
// kept), scroll at the end after a reload, reload during an answer (the page attaches to
// the completion still running in the pod) and during a tool (the pod runs the agent: the
// tool goes on), windows following the same conversation.
const http = require('http')
const { run, openProject, assert, OUT, composerText } = require('../common.cjs')

const requests = []
const byScenario = {}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))
const call = (id, name, args) => ({ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } })

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 }, modalities: { vision: true } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  requests.push(r)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const firstUser = msgs.find((m, i) => i > 0 && m.role === 'user')
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user')
  const scenario = text(lastUser).split(' ')[0]
  byScenario[scenario] = (byScenario[scenario] ?? 0) + 1
  if (scenario === 'slow') {
    sse(res, { content: 'Part 1.' })
    for (let i = 0; i < 25 && !res.destroyed; i++) {
      await sleep(100)
      sse(res, { content: ' x' })
    }
    sse(res, { content: ' End.' })
    return end(res)
  }
  if (scenario === 'slow-tool') {
    if (last.role === 'user') return sse(res, { tool_calls: [call('b1', 'bash', { command: 'echo started; sleep 3; echo done' })] }), end(res, 'tool_calls')
    sse(res, { content: `Resumed: ${text(last)}` })
    return end(res)
  }
  if (scenario === 'tool') {
    if (last.role === 'user' && text(last).startsWith('tool')) return sse(res, { tool_calls: [call('q1', 'bash', { command: 'sleep 1; echo ok' })] }), end(res, 'tool_calls')
    sse(res, { content: `Seen: ${text(last)}` })
    return end(res)
  }
  if (scenario === 'wait') {
    await sleep(1200)
    sse(res, { content: 'Finished.' })
    return end(res)
  }
  if (scenario === 'long') {
    for (let i = 1; i <= 40; i++) sse(res, { content: `Paragraph ${i}: some text to fill the conversation and make it scroll.\n\n` })
    sse(res, { content: '```mermaid\ngraph TD\n  A-->B\n  B-->C\n  C-->D\n  D-->E\n```\n\nLast line.' })
    return end(res)
  }
  // A message injected after a tool result.
  if (last.role === 'user' && msgs[msgs.length - 2]?.role === 'tool') {
    sse(res, { content: `Seen: ${text(last)}` })
    return end(res)
  }
  const images = Array.isArray(lastUser.content) ? lastUser.content.filter((p) => p.type === 'image_url').length : 0
  sse(res, { content: `Answer to ${text(lastUser)}${images ? ` (+${images} image)` : ''}` })
  end(res)
})

async function ask(page, message, expect) {
  await page.click('.ai-composer .ed-content')
  await page.fill('.ai-composer .ed-content', message)
  await page.keyboard.press('Control+Enter')
  await page.waitForSelector(`.ai-msg.assistant:not(.live) .md:has-text("${expect}")`, { timeout: 20000 })
  await page.waitForSelector('[data-testid=send]', { timeout: 10000 })
}

const atEnd = (page) => page.$eval('.ai-messages', (el) => el.scrollHeight - el.scrollTop - el.clientHeight)

run(async ({ page, ctx }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  page.on('dialog', (d) => d.accept())
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    // Side bar: search first, then the new conversation button.
    await page.click('.ai-panel button[title="Conversations of the project"]')
    const order = await page.$$eval('.ai-sidebar > *', (e) => e.map((x) => x.className))
    assert(order[0].includes('ai-side-search') && order[1].includes('ai-new-chat'), 'side bar: search, then new conversation')
    await page.click('.ai-panel button[title="Conversations of the project"]')

    // The message box: Mode, Dictate, Attach, Options; a Markdown editor where Enter adds a
    // line and continues the lists, @paths stand out, Ctrl+S sends.
    const xs = await page.evaluate(() => ['[data-testid=ai-mode]', '.ai-mic', '[data-testid=ai-attach]', '[data-testid=ai-options]'].map((s) => document.querySelector('.ai-composer-bar ' + s).getBoundingClientRect().left))
    assert(xs.every((x, i) => i === 0 || x > xs[i - 1]), 'bar order: Mode, Dictate, Attach, Options')
    assert((await page.textContent('.ai-editor .ed-hint')) === 'Message…', 'short placeholder')
    // The LED strip takes the color of each mode (a click on the mode button cycles through the four).
    const leds = []
    for (let i = 0; i < 4; i++) {
      await page.waitForTimeout(400)
      leds.push(await page.$eval('[data-testid=ai-led]', (e) => getComputedStyle(e).backgroundColor))
      await page.click('[data-testid=ai-mode]')
    }
    assert(new Set(leds).size === 4, 'a LED color per mode ' + leds.join(' '))
    await page.click('.ai-composer .ed-content')
    for (const k of ['Steps:', 'Enter', '- one', 'Enter', 'two', 'Enter', 'Enter', '1. a', 'Enter', 'b', 'Enter', 'Enter', '- [x] done', 'Enter', 'next', 'Enter', 'Enter', 'see @src/main.go']) {
      if (k === 'Enter') await page.keyboard.press('Enter')
      else await page.keyboard.type(k)
    }
    await page.keyboard.press('Escape')
    const listed = 'Steps:\n- one\n- two\n1. a\n2. b\n- [x] done\n- [ ] next\nsee @src/main.go'
    assert((await composerText(page)) === listed, 'Enter adds a line and continues the lists ' + JSON.stringify(await composerText(page)))
    assert(await page.waitForFunction(() => CSS.highlights.get('ai-ref')?.size === 1, null, { timeout: 3000 }).then(() => true, () => false), 'the @path is highlighted')
    await page.screenshot({ path: OUT + '/chat-composer-editor.png' })
    await page.keyboard.press('Control+s')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Answer to Steps:")', { timeout: 20000 })
    await page.waitForSelector('[data-testid=send]', { timeout: 10000 })
    assert(text(requests.at(-1).messages.at(-1)) === listed && (await composerText(page)) === '', 'Ctrl+S sends the message, the box is emptied')

    // Full screen: the box takes the whole tool; Esc comes back with the same text, sending leaves it.
    await page.click('[data-testid=ai-full]')
    await page.waitForSelector('.ai-messages', { state: 'hidden' })
    const fill = await page.evaluate(() => document.querySelector('.ai-composer').offsetHeight / document.querySelector('.ai-main').offsetHeight)
    assert(fill > 0.6, 'full screen: the thread is hidden, the box fills the tool (' + Math.round(fill * 100) + ' %)')
    await page.keyboard.type('A long message')
    await page.keyboard.press('Escape')
    await page.waitForSelector('.ai-messages', { state: 'visible' })
    assert((await composerText(page)) === 'A long message', 'Esc leaves the full screen, the text kept')
    await page.keyboard.press('Control+Shift+KeyE')
    await page.waitForSelector('.ai-messages', { state: 'hidden' })
    await page.screenshot({ path: OUT + '/chat-composer-full.png' })
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Answer to A long message")', { timeout: 20000 })
    await page.waitForSelector('[data-testid=send]', { timeout: 10000 })
    assert(await page.isVisible('.ai-messages'), 'the shortcut opens the full screen, sending leaves it')

    // Ctrl+click on an @file opens it.
    await ask(page, 'Look at @src/main.go', 'Answer to Look at @src/main.go')
    await page.click('.ai-msg.user .ai-mention:has-text("@src/main.go")', { modifiers: ['Control'] })
    await page.waitForFunction(() => document.querySelector('.pane.active .tab.active')?.textContent.includes('main.go'), null, { timeout: 5000 }).catch(() => {})
    assert((await page.textContent('.pane.active .tab.active').catch(() => '')).includes('main.go'), 'Ctrl+click on @file opens the file')
    // The editor takes the focus once open: come back to the message box.
    await page.waitForTimeout(400)
    await page.click('.ai-composer .ed-content')

    // Queue: a message written during an answer is sent after it.
    await page.fill('.ai-composer .ed-content', 'wait a bit')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=stop]')
    await page.fill('.ai-composer .ed-content', 'next')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=ai-queue]:has-text("next")')
    assert(true, 'message queued during the answer')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Answer to next")', { timeout: 15000 })
    assert(!(await page.isVisible('[data-testid=ai-queue]')), 'queue emptied and message sent after the answer')

    // Queue between tool steps: the message joins the conversation after the tool result.
    await page.fill('.ai-composer .ed-content', 'tool then')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-tool.running', { timeout: 10000 })
    await page.fill('.ai-composer .ed-content', 'and this too')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Seen: and this too")', { timeout: 15000 })
    const inj = requests[requests.length - 1].messages.slice(-2)
    assert(inj[0].role === 'tool' && inj[1].role === 'user' && text(inj[1]) === 'and this too', 'message injected between two tool steps')
    await page.waitForSelector('[data-testid=send]')

    // Edit in place, attachment kept, the answer regenerated from there.
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGP8z8DwnwEIGBkZGRgYAAAiBgID0ZfYpQAAAABJRU5ErkJggg==', 'base64')
    await page.setInputFiles('.ai-composer input[type=file]', { name: 'p.png', mimeType: 'image/png', buffer: png })
    await page.waitForSelector('.ai-composer .ai-att:has-text("p.png")')
    await ask(page, 'Describe', 'Answer to Describe (+1 image)')
    const before = await page.$$eval('.ai-msg.user', (e) => e.length)
    await page.hover('.ai-msg.user:has-text("Describe")')
    await page.click('.ai-msg.user:has-text("Describe") .ai-act[title="Edit"]')
    await page.waitForSelector('[data-testid=ai-edit] textarea')
    await page.fill('[data-testid=ai-edit] textarea', 'Describe in detail')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Answer to Describe in detail (+1 image)")', { timeout: 10000 })
    assert((await page.$$eval('.ai-msg.user', (e) => e.length)) === before && !(await page.isVisible('.md:has-text("Answer to Describe (+1 image)")')), 'message edited in place, answer generated again, image kept')

    // The thread grows again between our scroll to the end and its event: still following.
    await page.evaluate(async () => {
      const list = document.querySelector('.ai-messages')
      const pad = (h) => list.firstElementChild.appendChild(Object.assign(document.createElement('div'), { className: 'e2e-pad', style: `height:${h}px` }))
      pad(100)
      list.scrollTop = list.scrollHeight
      pad(400)
      await new Promise((ok) => requestAnimationFrame(() => requestAnimationFrame(ok)))
    })
    await page.waitForTimeout(200)
    const grown = await atEnd(page)
    await page.evaluate(() => document.querySelectorAll('.e2e-pad').forEach((e) => e.remove()))
    assert(grown < 40, `a step arriving right after the scroll to the end is followed (gap ${Math.round(grown)} px)`)

    // Long answer, reload: the view ends at the bottom.
    await ask(page, 'long text', 'Last line.')
    await page.reload()
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Last line.")', { timeout: 10000 })
    await page.waitForSelector('.md-mermaid-svg svg', { timeout: 15000 }).catch(() => {})
    await page.waitForTimeout(800)
    const gap = await atEnd(page)
    assert(gap < 40, `after a reload, the conversation is shown down to the end (gap ${Math.round(gap)} px)`)

    // Reload during an answer: the page attaches to the completion running in the pod.
    await page.waitForSelector('[data-testid=send]')
    await page.fill('.ai-composer .ed-content', 'slow stream')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.live .md:has-text("Part 1. x x")', { timeout: 10000 })
    await page.reload()
    await page.waitForSelector('.ai-msg.live .md:has-text("Part 1.")', { timeout: 10000 })
    assert(true, 'after the reload, the running answer is shown with what was written')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("End.")', { timeout: 15000 })
    const full = await page.$$eval('.ai-msg.assistant:not(.live) .md', (e) => e[e.length - 1].textContent)
    assert(full.startsWith('Part 1.') && (full.match(/x/g) ?? []).length === 25 && byScenario.slow === 1, `full answer, without a new request (${byScenario.slow} request)`)
    await page.screenshot({ path: OUT + '/chat-resume.png' })

    // Reload during a tool: the agent runs in the pod, the tool goes on and so does the agent.
    await page.waitForSelector('[data-testid=send]')
    await page.fill('.ai-composer .ed-content', 'slow-tool now')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-tool.running', { timeout: 10000 })
    const streamed = await page.waitForSelector('.ai-tool.running .ai-term:has-text("started")', { timeout: 2500 }).then(() => true, () => false)
    assert(streamed, 'the running block is open and follows the output of the command')
    assert(/\d/.test((await page.textContent('.ai-tool.running .ai-dur').catch(() => '')) ?? ''), 'the running block counts its duration')
    await page.reload()
    const resumed = await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Resumed: Exit code 0")', { timeout: 15000 }).then(() => true, () => false)
    const rows = await page.$$eval('.ai-tool', (e) => e.filter((x) => x.textContent.includes('sleep 3')).map((x) => x.className))
    const done = await page.$$eval('.ai-tool', (e) => e.filter((x) => x.textContent.includes('sleep 3')).map((x) => ({ term: !!x.querySelector('.ai-term'), dur: x.querySelector('.ai-dur')?.textContent ?? '' })))
    assert(done.length === 1 && !done[0].term && /^[3-9][.\d]* s$/.test(done[0].dur), 'the block folds when it ends and keeps its duration: ' + JSON.stringify(done))
    assert(resumed && rows.length === 1 && !rows[0].includes('error') && byScenario['slow-tool'] === 2, `tool not interrupted by the reload, then the agent goes on (${rows.length} tool, ${byScenario['slow-tool']} requests)`)

    // Another window opened during an answer follows its stream.
    await page.waitForSelector('[data-testid=send]')
    const toolUrl = new URL(new URL(page.url()).pathname + '/tool/assistant', page.url()).href
    const other = await ctx.newPage()
    await other.goto(toolUrl)
    await other.waitForSelector('.ai-panel.detached .ai-composer')
    const slowBefore = byScenario.slow
    await page.click('.ai-composer .ed-content')
    await page.fill('.ai-composer .ed-content', 'slow one')
    await page.keyboard.press('Control+Enter')
    await other.waitForSelector('.ai-msg.live .md:has-text("Part 1. x")', { timeout: 10000 })
    assert(true, 'the other window follows the running answer')
    await other.fill('.ai-composer .ed-content', 'from the other window')
    await other.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=ai-queue]:has-text("from the other window")', { timeout: 5000 })
    assert(true, 'the other window writes too: its message is queued for the next step')
    await other.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("End.")', { timeout: 15000 })
    await page.waitForSelector('.ai-msg.user:has-text("from the other window")', { timeout: 15000 })
    assert(byScenario.slow === slowBefore + 1, 'answer received in the other window without a new request for it')

    // Stop from the window that follows.
    await page.waitForSelector('[data-testid=send]')
    await page.fill('.ai-composer .ed-content', 'slow two')
    await page.keyboard.press('Control+Enter')
    await other.waitForSelector('.ai-msg.live .md:has-text("Part 1. x")', { timeout: 10000 })
    await other.click('[data-testid=stop]')
    await page.waitForSelector('.ai-error:has-text("Stopped")', { timeout: 10000 })
    assert(true, 'Stop from the other window stops the answer')

    // The running window closes: the other one takes over and gets the end.
    await page.waitForSelector('[data-testid=send]')
    await other.waitForSelector('[data-testid=send]', { timeout: 10000 })
    await page.fill('.ai-composer .ed-content', 'slow three')
    await page.keyboard.press('Control+Enter')
    await other.waitForSelector('.ai-msg.live .md:has-text("Part 1. x")', { timeout: 10000 })
    await page.close()
    await other.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("End.")', { timeout: 15000 })
    const ends = await other.$$eval('.ai-msg.assistant:not(.live) .md', (e) => e.filter((x) => x.textContent.includes('End.')).length)
    assert(ends >= 2, 'the remaining window takes the conversation over and receives the end')
    await other.screenshot({ path: OUT + '/chat-other-window.png' })

    // Every window closed during an answer: the pod goes on, a new window shows the end.
    await other.waitForSelector('[data-testid=send]', { timeout: 10000 })
    const slowFour = byScenario.slow
    await other.fill('.ai-composer .ed-content', 'slow four')
    await other.keyboard.press('Control+Enter')
    await other.waitForSelector('.ai-msg.live .md:has-text("Part 1. x")', { timeout: 10000 })
    await other.close()
    await sleep(4000)
    const back = await ctx.newPage()
    await back.goto(toolUrl)
    await back.waitForSelector('.ai-panel.detached .ai-composer')
    await back.waitForFunction(() => [...document.querySelectorAll('.ai-msg.assistant:not(.live) .md')].filter((x) => x.textContent.includes('End.')).length >= 3, null, { timeout: 15000 })
    assert(byScenario.slow === slowFour + 1 && (await back.isVisible('[data-testid=send]')), 'the answer went on without any window, shown once a window opens again')
  } finally {
    fake.close()
  }
})
