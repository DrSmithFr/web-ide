// Kanban and the assistant: ask_user (questions one at a time, recap, answers sent back),
// kanban_create / kanban_list from any conversation, writing tools only when linked.
const http = require('http')
const { run, openProject, assert, OUT } = require('../common.cjs')

const requests = []
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
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  requests.push(r)
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const firstUser = text(msgs.find((m) => m.role === 'user') ?? {})
  if (firstUser.startsWith('Write the implementation plan')) {
    if (last.role === 'user')
      return (
        sse(res, {
          tool_calls: calls([
            ['s1', 'kanban_set_plan', { plan: '## Approach\n1. Add the route\n2. Test', goals: ['The route answers', 'The tests pass'] }],
            ['s2', 'kanban_move', { status: 'ready' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Plan saved.' })
    return end(res)
  }
  if (firstUser.startsWith('Develop ticket')) {
    const ids = [...msgs[0].content.matchAll(/\(id (\d+)\)/g)].map((m) => Number(m[1]))
    if (last.role === 'user')
      return (
        sse(res, {
          tool_calls: calls([
            ['d1', 'kanban_goal', { action: 'check', id: ids[0] }],
            ['d2', 'kanban_goal', { action: 'check', id: ids[1] }],
            ['d3', 'kanban_move', { status: 'done' }],
            ['d4', 'kanban_move', { status: 'review', test_summary: 'Open **/export** and check the CSV.' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Development finished.' })
    return end(res)
  }
  if (last.role === 'user')
    return (
      sse(res, {
        content: 'A few questions.',
        tool_calls: calls([
          [
            'q1',
            'ask_user',
            {
              questions: [
                { question: 'Which format?', header: 'Format', options: [{ label: 'CSV (recommended)', description: 'simple' }, { label: 'JSON' }] },
                { question: 'Which columns?', options: [{ label: 'id' }, { label: 'total' }, { label: 'date' }], multiple: true },
                { question: 'Priority?', options: [{ label: 'High' }, { label: 'Normal' }] },
              ],
            },
          ],
        ]),
      }),
      end(res, 'tool_calls')
    )
  if (last.role === 'tool' && last.tool_call_id === 'q1')
    return (
      sse(res, {
        tool_calls: calls([
          ['k1', 'kanban_create', { title: 'Export JSON', description: 'Columns: id, date', type: 'feature', priority: 'high', files: ['src/main.go'] }],
          ['k2', 'kanban_list', {}],
          ['k3', 'kanban_set_plan', { plan: 'x', goals: ['y'] }],
        ]),
      }),
      end(res, 'tool_calls')
    )
  sse(res, { content: 'Ticket created.' })
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
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    await page.fill('.ai-composer textarea', 'Write down a ticket for the export')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-question]', { timeout: 10000 })
    const names = toolNames(requests[0])
    assert(['kanban_list', 'kanban_get', 'kanban_create', 'ask_user'].every((n) => names.includes(n)), 'reading kanban tools and ask_user offered')
    assert(!names.includes('kanban_set_plan') && !names.includes('kanban_move'), 'no changing tool without a linked ticket')
    assert(requests[0].messages[0].content.includes('ask_user'), 'the prompt introduces ask_user')
    await page.waitForSelector('[data-testid=send]')
    assert(requests.length === 1, 'the turn stops on the questions')

    // Question 1: a single choice moves on.
    assert((await page.textContent('[data-testid=ai-ask-question]')).includes('Which format'), 'first question shown')
    await page.click('.ai-ask-option:has-text("JSON")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Which columns")')
    assert(true, 'single choice: next question')
    // Question 2: several choices.
    await page.click('.ai-ask-option:has-text("id")')
    await page.click('.ai-ask-option:has-text("date")')
    await page.click('[data-testid=ai-ask-next]')
    // Question 3: free answer.
    await page.fill('[data-testid=ai-ask-free]', 'Very high')
    await page.click('[data-testid=ai-ask-next]')
    await page.waitForSelector('.ai-ask-recap')
    const recap = await page.textContent('.ai-ask-recap')
    assert(recap.includes('JSON') && recap.includes('id ; date') && recap.includes('Very high'), 'summary: ' + recap)
    await page.screenshot({ path: OUT + '/ask-recap.png' })
    await page.click('[data-testid=ai-ask-send]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Ticket created.")', { timeout: 15000 })
    const answer = text(requests[1].messages.find((m) => m.role === 'tool' && m.tool_call_id === 'q1'))
    assert(answer.includes('Which format?') && answer.includes('→ JSON') && answer.includes('id ; date') && answer.includes('Very high'), 'answers sent back to the model: ' + answer)
    assert(await page.isVisible('[data-testid=ai-ask] .badge:has-text("answered")'), 'card marked answered')

    const res = requests[2].messages.filter((m) => m.role === 'tool')
    const by = (id) => text(res.find((m) => m.tool_call_id === id))
    assert(by('k1').includes('Ticket #1 created'), 'kanban_create : ' + by('k1'))
    assert(by('k2').includes('#1 [New]') && by('k2').includes('Export JSON'), 'kanban_list : ' + by('k2'))
    assert(by('k3').includes('not linked to a ticket'), 'kanban_set_plan refused without a linked ticket')

    await page.click('.rail-left .rail-btn[title="Kanban"]')
    await page.waitForSelector('[data-testid=kanban-panel] [data-testid=ticket-card-1]:has-text("Export JSON")')
    await page.click('[data-testid=ticket-card-1]')
    await page.waitForSelector('[data-testid=ticket-view]')
    assert((await page.textContent('.tk-side')).includes('src/main.go'), 'file linked by the model')
    await page.click('.tk-section-toggle:has-text("History")')
    assert(await page.isVisible('.tk-events .badge:has-text("assistant")'), 'history: created by the assistant')

    // Plan generated by the model from the ticket.
    const before = requests.length
    await page.click('[data-testid=ticket-plan-generate]')
    await page.waitForSelector('[data-testid=ticket-status]:has-text("Ready")', { timeout: 15000 })
    const rp = requests[before]
    const sys = rp.messages[0].content
    assert(sys.includes('Ticket linked to this conversation') && sys.includes('implementation plan') && sys.includes('# Ticket #1 · Export JSON'), 'prompt of the Plan role with the ticket')
    const pn = toolNames(rp)
    assert(pn.includes('kanban_set_plan') && pn.includes('kanban_move') && !pn.includes('edit_file') && !pn.includes('exit_plan_mode'), 'tools of the ticket plan: ' + pn.join(','))
    assert(await page.isVisible('[data-testid=ai-ticket-bar]:has-text("#1")'), 'ticket bar in the assistant')
    await page.waitForSelector('[data-testid=ticket-plan] li:has-text("Add the route")')
    assert((await page.$$('[data-testid=ticket-goal]')).length === 2, 'goals written by the model')
    await page.waitForSelector('[data-testid=ticket-chat]:has-text("Plan")', { timeout: 5000 })
    assert(true, 'conversation listed in the ticket')

    // Development session: goals checked, the model cannot close, moves to review.
    // Not a git repository: after confirmation, development in the project folder itself.
    const before2 = requests.length
    await page.click('[data-testid=ticket-start]')
    await page.waitForSelector('[data-testid=ticket-status]:has-text("To test")', { timeout: 15000 })
    const rd = requests[before2]
    assert(rd.messages[0].content.includes('You **develop** this ticket') && toolNames(rd).includes('edit_file'), 'dev role in Build mode')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Development finished.")', { timeout: 10000 })
    const rr = requests[requests.length - 1].messages.filter((m) => m.role === 'tool')
    const d3 = text(rr.find((m) => m.tool_call_id === 'd3'))
    assert(d3.includes('the model cannot move'), 'the model cannot close the ticket: ' + d3)
    assert((await page.$$('[data-testid=ticket-goal].done')).length === 2, 'goals checked by the model')
    assert(await page.isVisible('[data-testid=ticket-test] strong:has-text("/export")'), 'test summary shown')
    assert((await page.$$('[data-testid=ticket-chat]')).length === 2, 'two linked conversations')
    await page.screenshot({ path: OUT + '/kanban-ai.png' })

    // Reopen the plan conversation from the ticket.
    await page.click('[data-testid=ticket-chat]:has-text("Plan")')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Plan saved.")', { timeout: 5000 })
    assert(true, 'plan conversation reopened from the ticket')
  } finally {
    fake.close()
  }
})
