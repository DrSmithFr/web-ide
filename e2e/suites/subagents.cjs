// Sub-agents: the parent delegates with spawn_agent (a card in its thread); the child notes
// and asks; the parent, woken by the question, asks the user first (ask_user), then replies;
// the change of the child waits for the user (toast "Sub-agent … asks to change a file",
// approved in the child thread, with its header and its task); its report wakes the parent;
// the child is nested under its parent in the side bar.
const http = require('http')
const fs = require('fs')
const { run, openProject, assert, OUT, WS } = require('../common.cjs')

const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))
const call = (res, id, name, args) => {
  sse(res, { tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }] })
  end(res, 'tool_calls')
}
const say = (res, s) => (sse(res, { content: s }), end(res))

let child = ''
const requests = []
const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  requests.push(r)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const last = r.messages[r.messages.length - 1]
  const content = text(last)
  if (text(r.messages[0]).includes('# You are a sub-agent')) {
    if (last.role === 'user') {
      sse(res, {
        tool_calls: [
          { index: 0, id: 'n1', type: 'function', function: { name: 'agent_note', arguments: JSON.stringify({ title: 'Found the greeting', text: 'It is in src/main.go.' }) } },
          { index: 1, id: 'k1', type: 'function', function: { name: 'agent_ask', arguments: JSON.stringify({ question: 'Which word should replace Bonjour?' }) } },
        ],
      })
      return end(res, 'tool_calls')
    }
    if (content.includes('Answer of your parent: Hello')) return call(res, 'e1', 'edit_file', { path: 'src/main.go', old_string: 'return fmt.Sprintf("Bonjour %s", g.Name)', new_string: 'return fmt.Sprintf("Hello %s", g.Name)' })
    if (last.role === 'tool') return call(res, 'p1', 'agent_report', { summary: 'Bonjour is now Hello in src/main.go.', files_changed: ['src/main.go'], status: 'done' })
    return say(res, 'unexpected')
  }
  if (content === 'Delegate the greeting') return call(res, 's1', 'spawn_agent', { title: 'Change the greeting', task: 'Replace the greeting Bonjour', files: ['src/main.go'] })
  if (content.includes('asks] Which word')) {
    child = /Sub-agent (\w+) \(/.exec(content)[1]
    return call(res, 'q1', 'ask_user', { questions: [{ question: 'Which word instead of Bonjour?', options: [{ label: 'Hello' }, { label: 'Hi' }] }] })
  }
  if (last.role === 'tool' && content.includes('→ Hello')) return call(res, 'r1', 'agent_reply', { child, answer: 'Hello' })
  if (content.includes('report: done]')) return say(res, 'The sub-agent changed the greeting.')
  return say(res, 'Waiting for the sub-agent.')
})

run(async ({ page }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    await page.fill('.ai-composer textarea', 'Delegate the greeting')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-child]:has-text("Change the greeting")', { timeout: 20000 })
    assert(true, 'a card for the sub-agent in the parent thread')
    const childReq = () => requests.find((r) => text(r.messages[0]).includes('# You are a sub-agent'))
    await page.waitForFunction(() => document.querySelector('[data-testid=ai-ask-question]'), null, { timeout: 20000 })
    const cr = childReq()
    const names = cr.tools.map((t) => t.function.name)
    assert(text(cr.messages[1]).includes('Replace the greeting Bonjour') && !text(cr.messages[1]).includes('Delegate') && names.includes('agent_report') && !names.includes('spawn_agent') && !names.includes('ask_user'), 'the child gets its task in a fresh context, with its own tools')

    // The question of the child woke the parent, which asks the user first.
    assert(await page.isVisible('[data-testid=ai-event-question]:has-text("Which word should replace Bonjour?")'), 'the question of the child in the parent thread')
    assert(await page.isVisible('[data-testid=ai-event-note]:has-text("Found the greeting")'), 'the note of the child in the parent thread')
    assert((await page.textContent('[data-testid=ai-child-status]')).includes('Waiting for its parent'), 'the card says the child waits for its parent')
    await page.screenshot({ path: OUT + '/subagent-question.png' })
    await page.click('.ai-ask-option:has-text("Hello")')
    await page.waitForSelector('[data-testid=ai-ask-send]')
    await page.click('[data-testid=ai-ask-send]')

    // The child goes on; its change waits for the user, announced in the parent window.
    await page.waitForSelector('.toast:has-text("Sub-agent “Change the greeting” asks to change a file")', { timeout: 20000 })
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('Bonjour'), 'nothing written before the user confirms')
    await page.click('.toast:has-text("Sub-agent") button:has-text("Open")')
    await page.waitForSelector('[data-testid=ai-child-header]')
    assert((await page.textContent('[data-testid=ai-child-header]')).includes('Sub-agent of'), 'the child thread says whose sub-agent it is')
    await page.waitForSelector('[data-testid=ai-task]:has-text("Replace the greeting Bonjour")', { timeout: 5000 })
    assert(true, 'the child thread starts with its task')
    await page.click('[data-testid=ai-approval] button:has-text("Apply")')
    await page.waitForFunction(() => document.querySelector('[data-testid=ai-child-header] .badge')?.textContent.includes('Done'), null, { timeout: 20000 })
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('Hello %s'), 'the change of the child applied once confirmed')

    // Back to the parent: the report woke it.
    await page.click('[data-testid=ai-child-parent]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("The sub-agent changed the greeting.")', { timeout: 20000 })
    assert((await page.textContent('[data-testid=ai-event-report]')).includes('Bonjour is now Hello in src/main.go.'), 'the report of the child in the parent thread')
    await page.waitForFunction(() => document.querySelector('[data-testid=ai-child-status]')?.textContent.includes('Done'), null, { timeout: 5000 })
    assert(true, 'the card of the child says it is done')
    await page.screenshot({ path: OUT + '/subagent-report.png' })

    // The side bar nests the child under its parent.
    if (!(await page.isVisible('[data-testid=ai-sidebar]'))) await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.waitForSelector('[data-testid=ai-chat-child]:has-text("Change the greeting")')
    assert(true, 'the child is listed under its parent')
  } finally {
    fake.close()
  }
})
