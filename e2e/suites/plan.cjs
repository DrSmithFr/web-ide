// AI assistant, Plan / Build modes: Shift+Tab, Plan prompt and tools (scratch files only,
// exit_plan_mode), dedicated Plan model, bash guess (reading and project builds run, a change asks),
// plan card and its execution in Build (changes in the project without asking), compaction
// asked by the model.
const fs = require('fs')
const http = require('http')
const path = require('path')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

const SCRATCH = path.join(WS, '..', 'scratch') // WEBIDE_SCRATCH of run.sh
const requests = []
const summaries = []
const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 200, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))
const calls = (list) => list.map(([id, name, args], index) => ({ index, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }))
const toolNames = (r) => (r.tools ?? []).map((t) => t.function.name)

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: ['fake-model', 'fake-plan', 'fake-small'].map((id) => ({ id, status: { value: 'loaded' } })) }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  if (text(r.messages[0]).startsWith('You summarize')) {
    summaries.push(r)
    sse(res, { content: 'SUMMARY of the work.' })
    return end(res)
  }
  requests.push(r)
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user' && !text(m).startsWith('Summary'))
  const ask = text(lastUser)
  const tools = msgs.filter((m) => m.role === 'tool')
  if (ask.startsWith('Plan the')) {
    if (last.role === 'user')
      return (
        sse(res, {
          tool_calls: calls([
            ['p1', 'bash', { command: 'git status; ls $(pwd)/src && cd src && go vet . 2>&1 | tail -3' }],
            ['p2', 'bash', { command: 'rm -rf src' }],
            ['p3', 'edit_file', { path: 'src/main.go', old_string: 'Bonjour', new_string: 'Salut' }],
            ['p5', 'write_file', { path: SCRATCH + '/draft.md', content: 'draft' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    return sse(res, { tool_calls: calls([['p4', 'exit_plan_mode', { plan: '## Plan\n1. Replace Bonjour with Salut in `src/main.go`\n2. Run the tests' }]]) }), end(res, 'tool_calls')
  }
  if (ask.startsWith('The plan is accepted')) {
    if (last.role === 'user') return sse(res, { tool_calls: calls([['e1', 'edit_file', { path: 'src/main.go', old_string: '"Bonjour %s"', new_string: '"Salut %s"' }]]) }), end(res, 'tool_calls')
    if (last.role === 'tool' && last.tool_call_id === 'e1') return sse(res, { tool_calls: calls([['c1', 'compact_conversation', { instructions: 'keep the plan' }]]) }), end(res, 'tool_calls')
    sse(res, { content: 'Plan carried out.' })
    return end(res)
  }
  sse(res, { content: `Answer (${r.model}).` })
  end(res)
})

run(async ({ page }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  page.on('dialog', (d) => d.accept())
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    // Dedicated Plan model.
    await page.click('.ai-tab:has-text("Plan mode")')
    await page.selectOption('[data-testid=plan-settings] select[name=planServer]', { index: 1 })
    await page.waitForSelector('[data-testid=plan-settings] select[name=planModel] option[value="fake-plan"]', { state: 'attached' })
    await page.selectOption('[data-testid=plan-settings] select[name=planModel]', 'fake-plan')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    // Shift+Tab: Plan mode.
    await page.click('.ai-composer .ed-content')
    await page.keyboard.press('Shift+Tab')
    await page.waitForSelector('[data-testid=ai-mode].plan:has-text("fake-plan")')
    assert(true, 'Shift+Tab switches to Plan mode (dedicated model shown)')
    assert(await page.$eval('.ai-composer-bar', (b) => b.firstElementChild.dataset.testid === 'ai-mode'), 'the mode button starts the composer bar')

    await page.fill('.ai-composer .ed-content', 'Plan the renaming')
    await page.keyboard.press('Control+Enter')
    // The command that changes something waits for the user.
    await page.waitForSelector('[data-testid=ai-approval]:has-text("rm -rf src")', { timeout: 10000 })
    const r0 = requests[0]
    assert(r0.model === 'fake-plan', 'request sent to the model of the Plan mode')
    assert(r0.messages[0].content.includes('Plan mode') && r0.messages[0].content.includes('exit_plan_mode'), 'system prompt of the Plan mode')
    const names = toolNames(r0)
    assert(names.includes('write_file') && names.includes('exit_plan_mode') && names.includes('compact_conversation'), 'tools of the Plan mode: writing (scratch files), with exit_plan_mode: ' + names.join(','))
    await page.click('[data-testid=ai-approval] button:has-text("Refuse")')
    await page.waitForSelector('[data-testid=ai-plan]', { timeout: 10000 })
    const res1 = requests[1].messages.filter((m) => m.role === 'tool')
    const byId = (id) => text(res1.find((m) => m.tool_call_id === id) ?? { content: '' })
    assert(byId('p1').startsWith('Exit code 0'), 'reading and build commands in the project run without asking: ' + byId('p1').slice(0, 80))
    assert(byId('p2').includes('refused') && fs.existsSync(WS + '/demo/src/main.go'), 'changing command refused, nothing deleted')
    assert(byId('p3').includes('Plan mode') && fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('Bonjour'), 'edit_file refused in the project in Plan mode')
    assert(fs.readFileSync(SCRATCH + '/draft.md', 'utf8') === 'draft', 'a scratch file is written without asking in Plan mode: ' + byId('p5'))
    const card = await page.waitForSelector('[data-testid=ai-plan] .md:has-text("Replace Bonjour with Salut")', { timeout: 5000 }).then(() => true, () => false)
    assert(card, 'plan card shown')
    await page.waitForSelector('[data-testid=send]')
    assert(requests.length === 2, 'the turn stops after the plan is presented')
    assert(await page.isVisible('.ai-plan-badge'), 'answer marked Plan')
    await page.screenshot({ path: OUT + '/plan-card.png' })

    // Execute the plan: Build mode, model of the conversation, edit then compaction by the model.
    await page.click('[data-testid=ai-plan] button:has-text("Execute this plan")')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Plan carried out.")', { timeout: 15000 })
    const r2 = requests[2]
    assert(r2.model === 'fake-model' && toolNames(r2).includes('edit_file') && !toolNames(r2).includes('exit_plan_mode'), 'carried out in Build mode with the model of the conversation')
    assert(!(await page.isVisible('[data-testid=ai-mode].plan')), 'the mode goes back to Build')
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('"Salut %s"'), 'the plan is carried out (file changed without asking)')
    assert(summaries.length === 1 && summaries[0].messages[0].content.includes('keep the plan'), 'compaction asked by the model, with its instructions')
    assert(await page.isVisible('[data-testid=ai-summary]'), 'summary shown in the conversation')
    await page.click('.ai-compacted-toggle')
    assert((await page.textContent('[data-testid=ai-plan]')).includes('carried out'), 'plan marked carried out (in the compacted messages)')
    await page.screenshot({ path: OUT + '/plan-done.png' })
  } finally {
    fake.close()
  }
})
