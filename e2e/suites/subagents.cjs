// Sub-agents: the parent delegates with spawn_agent (a card in its thread); the child notes
// and asks; the parent, woken by the question, asks the user first (ask_user), then replies;
// the change of the child out of the project waits for the user (toast "Sub-agent … asks to change a file",
// approved in the child thread, with its header and its task); its report wakes the parent;
// while it works the child is listed under its parent among the active conversations, then in
// the history of the day. Then a cloud server (OpenAI-compatible,
// with a key and a typed model) offered to sub-agents: the parent picks it for a child, whose
// card shows its model, tokens and cost.
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
// The cloud provider: needs its key, has no /models (the model is typed in the settings).
const cloudAuth = []
const cloud = http.createServer(async (req, res) => {
  cloudAuth.push(req.headers.authorization)
  if (req.headers.authorization !== 'Bearer sk-e2e') return res.writeHead(401).end('{"error":{"message":"bad key"}}')
  if (req.url !== '/v1/chat/completions') return res.writeHead(404).end()
  for await (const _ of req);
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  sse(res, { tool_calls: [{ index: 0, id: 'c1', type: 'function', function: { name: 'agent_report', arguments: JSON.stringify({ summary: 'Reviewed in the cloud.' }) } }] })
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }], usage: { prompt_tokens: 100, completion_tokens: 20, cost: 0.5 } })}\n\n`)
  res.end('data: [DONE]\n\n')
})
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
    if (content.includes('Answer of your parent: Hello')) return call(res, 'e1', 'edit_file', { path: '../greeting.txt', old_string: 'Bonjour', new_string: 'Hello' })
    if (last.role === 'tool') return call(res, 'p1', 'agent_report', { summary: 'Bonjour is now Hello in src/main.go.', files_changed: ['src/main.go'], status: 'done' })
    return say(res, 'unexpected')
  }
  if (content === 'Review in the cloud') return call(res, 'c1', 'spawn_agent', { title: 'Cloud review', task: 'Review main.go', server: 'Cloud', model: 'cloud-coder' })
  if (content.includes('report: done]\nReviewed in the cloud.')) return say(res, 'The cloud reviewed it.')
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
  await new Promise((r) => cloud.listen(0, '127.0.0.1', r))
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    fs.writeFileSync(WS + '/greeting.txt', 'Bonjour\n') // out of the project: its change asks
    await page.fill('.ai-composer .ed-content', 'Delegate the greeting')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=ai-child]:has-text("Change the greeting")', { timeout: 20000 })
    assert(true, 'a card for the sub-agent in the parent thread')
    const childReq = () => requests.find((r) => text(r.messages[0]).includes('# You are a sub-agent'))
    await page.waitForFunction(() => document.querySelector('[data-testid=ai-ask-question]'), null, { timeout: 20000 })
    const cr = childReq()
    const names = cr.tools.map((t) => t.function.name)
    assert(text(cr.messages[1]).includes('Replace the greeting Bonjour') && !text(cr.messages[1]).includes('Delegate') && names.includes('agent_report') && !names.includes('spawn_agent') && !names.includes('ask_user'), 'the child gets its task in a fresh context, with its own tools')

    // Both wait (the parent for the user, the child for its parent): listed first, the child
    // under its parent.
    await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.waitForSelector('[data-testid=ai-side-active] [data-testid=ai-chat-child]:has-text("Change the greeting")', { timeout: 5000 })
    assert((await page.textContent('.ai-side-group-name')) === 'Active', 'the active conversations come first, the working sub-agent under its parent')
    await page.waitForSelector('[data-testid=ai-side-active] .ai-chat-item:not(.nested) [data-testid=ai-dot-waiting]', { timeout: 5000 })
    assert(await page.isVisible('[data-testid=ai-chat-child] [data-testid=ai-dot-waiting]'), 'orange dots: the parent waits for the user, the child for its parent')
    await page.click('.ai-panel button[title="Conversations of the project"]')

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
    assert(fs.readFileSync(WS + '/greeting.txt', 'utf8') === 'Bonjour\n', 'nothing written before the user confirms')
    await page.click('.toast:has-text("Sub-agent") button:has-text("Open")')
    await page.waitForSelector('[data-testid=ai-child-header]')
    assert((await page.textContent('[data-testid=ai-child-header]')).includes('Sub-agent of'), 'the child thread says whose sub-agent it is')
    await page.waitForSelector('[data-testid=ai-task]:has-text("Replace the greeting Bonjour")', { timeout: 5000 })
    assert(true, 'the child thread starts with its task')
    await page.click('[data-testid=ai-approval] button:has-text("Apply")')
    await page.waitForFunction(() => document.querySelector('[data-testid=ai-child-header] .badge')?.textContent.includes('Done'), null, { timeout: 20000 })
    assert(fs.readFileSync(WS + '/greeting.txt', 'utf8') === 'Hello\n', 'the change of the child applied once confirmed')

    // Back to the parent: the report woke it.
    await page.click('[data-testid=ai-child-parent]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("The sub-agent changed the greeting.")', { timeout: 20000 })
    assert((await page.textContent('[data-testid=ai-event-report]')).includes('Bonjour is now Hello in src/main.go.'), 'the report of the child in the parent thread')
    await page.waitForFunction(() => document.querySelector('[data-testid=ai-child-status]')?.textContent.includes('Done'), null, { timeout: 5000 })
    assert(true, 'the card of the child says it is done')
    await page.screenshot({ path: OUT + '/subagent-report.png' })

    // Ended, the child goes to the history of the day, beside its parent.
    if (!(await page.isVisible('[data-testid=ai-sidebar]'))) await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.waitForSelector('.ai-side-group:has(.ai-side-group-name:text-is("Today")) .ai-chat-item:not(.nested):has-text("Change the greeting")', { timeout: 5000 })
    assert(!(await page.isVisible('[data-testid=ai-side-active]')), 'the ended sub-agent is in the history of the day, nothing active left')
    assert(!(await page.isVisible('.ai-chat-item:has-text("Change the greeting") .ai-agent-dot')), 'no dot on an ended sub-agent')

    // A cloud server for the sub-agents: kind, key, a typed model, a note.
    await page.click('.ai-panel button[title^="Settings"]')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${cloud.address().port}/v1`)
    await page.selectOption('.ai-servers select[name=kind]', 'openai')
    await page.fill('.ai-servers input[name=name]', 'Cloud')
    await page.fill('.ai-servers input[name=apiKey]', 'sk-e2e')
    await page.click('[data-testid=model-conf-add]')
    await page.fill('[data-testid=model-conf] input[name=modelId]', 'cloud-coder')
    await page.check('.ai-servers input[name=children]')
    await page.fill('.ai-servers input[name=note]', 'strong reviewer, paid')
    await page.click('.ai-servers form button.primary')
    await page.waitForSelector('.ai-server-row:has-text("Cloud")')
    assert((await page.textContent('.ai-server-row:has-text("Cloud")')).includes('OpenAI-compatible provider'), 'the cloud server is listed with its kind')
    await page.click('.ai-tab:has-text("Sub-agents")')
    await page.selectOption('select[name=childServer]', { label: 'Cloud' })
    await page.waitForFunction(() => [...document.querySelectorAll('select[name=childModel] option')].some((o) => o.value === 'cloud-coder'))
    assert(true, 'the typed model is offered as default model of the sub-agents')
    await page.screenshot({ path: OUT + '/subagent-settings.png' })
    await page.click('.ai-servers .modal-head button')
    assert(!JSON.stringify(await page.evaluate(() => document.body.innerHTML)).includes('sk-e2e'), 'the key never reaches the page')

    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer .ed-content', 'Review in the cloud')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("The cloud reviewed it.")', { timeout: 20000 })
    const usage = await page.textContent('[data-testid=ai-child]:has-text("Cloud review") [data-testid=ai-child-usage]')
    assert(usage.includes('cloud-coder') && usage.includes('120') && usage.includes('0.50'), 'the card of the child shows its model, tokens and cost: ' + usage)
    const parentReq = requests.find((r) => text(r.messages[r.messages.length - 1]) === 'Review in the cloud')
    assert(text(parentReq.messages[0]).includes('- Cloud, models: cloud-coder — strong reviewer, paid'), 'the parent prompt lists the server for sub-agents with its note')
    assert(cloudAuth.length > 0 && cloudAuth.every((a) => a === 'Bearer sk-e2e'), 'the child ran on the cloud server, with its key')
  } finally {
    cloud.close()
    fake.close()
  }
})
