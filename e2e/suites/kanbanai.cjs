// Kanban and the assistant: ask_user (questions one at a time, recap, answers sent back),
// kanban_create / kanban_list from any conversation, writing tools only when linked,
// Briefing mode (tickets created and linked to the conversation).
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
            ['s1', 'kanban_set_plan', { plan: '## Approach\n1. Add the route\n2. Test', goals: [{ title: 'The route answers', description: 'curl /export gives 200' }, 'The tests pass'], size: 'l' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Plan saved.' })
    return end(res)
  }
  if (firstUser.startsWith('Brief:')) {
    if (last.role === 'user')
      return sse(res, { tool_calls: calls([['b0', 'ask_user', { questions: [{ question: 'Who uses the export?', options: [{ label: 'Accounting' }, { label: 'Everyone' }] }] }]]) }), end(res, 'tool_calls')
    if (last.tool_call_id === 'b0')
      return (
        sse(res, {
          tool_calls: calls([
            ['b1', 'kanban_create', { title: 'Accounting export', description: '**Need**: monthly export' }],
            ['b2', 'kanban_create', { title: 'Export settings' }],
            ['b3', 'kanban_add_note', { text: 'Used by accounting only.' }],
            ['b4', 'edit_file', { path: 'src/main.go', old_string: 'Bonjour', new_string: 'Salut' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Tickets written.' })
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
            ['d5', 'kanban_add_note', { text: 'Erratum: '.repeat(120) }],
            ['d6', 'kanban_add_note', { text: 'The CSV uses `;` as separator.' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Development finished.' })
    return end(res)
  }
  if (firstUser.startsWith('Handle this test feedback')) {
    const id = Number(/\(id (\d+), Bug/.exec(msgs[0].content)?.[1])
    if (last.role === 'user') return sse(res, { tool_calls: calls([['f1', 'kanban_feedback', { action: 'done', id }]]) }), end(res, 'tool_calls')
    sse(res, { content: 'Feedback handled.' })
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
          ['k1', 'kanban_create', { title: 'Export JSON', description: 'Columns: id, date', priority: 'high', files: ['src/main.go'] }],
          ['k2', 'kanban_list', {}],
          ['k3', 'kanban_set_plan', { plan: 'x', goals: ['y'] }],
          ['k4', 'kanban_create', { title: 'Verbose', description: 'blah '.repeat(400) }],
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
    assert(by('k4').includes('description too long (1999 characters, 1500 max)'), 'a description too long is refused: ' + by('k4').slice(0, 60))

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
    await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")', { timeout: 15000 })
    assert((await page.$eval('[data-testid=ticket-size]', (e) => e.value)) === 'l', 'the plan of the model sets the size of the ticket')
    const rp = requests[before]
    const sys = rp.messages[0].content
    assert(sys.includes('Ticket linked to this conversation') && sys.includes('implementation plan') && sys.includes('# Ticket #1 · Export JSON'), 'prompt of the Plan role with the ticket')
    const pn = toolNames(rp)
    assert(pn.includes('kanban_set_plan') && pn.includes('kanban_feedback') && !pn.includes('edit_file') && !pn.includes('exit_plan_mode'), 'tools of the ticket plan: ' + pn.join(','))
    assert(await page.isVisible('[data-testid=ai-ticket-bar]:has-text("#1")'), 'ticket bar in the assistant')
    await page.waitForSelector('[data-testid=ticket-plan] li:has-text("Add the route")')
    assert((await page.$$('[data-testid=ticket-goal]')).length === 2, 'goals written by the model')
    assert((await page.textContent('[data-testid=ticket-goal-desc]')) === 'curl /export gives 200', 'goal with its description')
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
    const d5 = text(rr.find((m) => m.tool_call_id === 'd5'))
    assert(d5.includes('note too long') && !(await page.isVisible('[data-testid=ticket-note]:has-text("Erratum")')), 'a long note of the model is refused: ' + d5.slice(0, 60))
    await page.waitForSelector('[data-testid=ticket-note]:has-text("separator")')
    assert(await page.isVisible('[data-testid=ticket-note]:has-text("separator") [data-testid=ticket-chat-link]:has-text("Development")'), 'the note of the model links its conversation')
    assert((await page.$$('[data-testid=ticket-goal].done')).length === 2, 'goals checked by the model')
    assert(await page.isVisible('[data-testid=ticket-test] strong:has-text("/export")'), 'test summary shown')
    assert((await page.$$('[data-testid=ticket-chat]')).length === 2, 'two linked conversations')
    await page.screenshot({ path: OUT + '/kanban-ai.png' })

    // Test feedback handled by a fix session: the model marks it done.
    await page.selectOption('[data-testid=ticket-feedback-kind]', 'bug')
    await page.fill('[data-testid=ticket-feedback-input]', 'The header is missing')
    await page.click('[data-testid=ticket-feedback-add]')
    await page.waitForSelector('[data-testid=ticket-feedback-item]:has-text("header is missing")')
    const before4 = requests.length
    await page.click('[data-testid=ticket-feedback-session]')
    await page.waitForSelector('[data-testid=ticket-feedback-item].done', { timeout: 15000 })
    const rf = requests[before4]
    assert(rf.messages[0].content.includes('**test feedback**') && text(rf.messages[1]).includes('The header is missing'), 'correction role on the feedback')
    await page.waitForSelector('[data-testid=ticket-feedback-item] [data-testid=ticket-chat-link]', { timeout: 5000 })
    assert((await page.textContent('[data-testid=ticket-status]')).includes('To test'), 'the ticket stays in To test')
    assert(true, 'feedback marked done by the model, linked to its conversation')

    // Reopen the plan conversation from the ticket.
    await page.click('[data-testid=ticket-chat]:has-text("Plan")')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Plan saved.")', { timeout: 5000 })
    assert(true, 'plan conversation reopened from the ticket')

    // Briefing mode: questions, then tickets created and linked to the conversation.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.click('.ai-composer textarea')
    await page.keyboard.press('Shift+Tab')
    await page.keyboard.press('Shift+Tab')
    await page.waitForSelector('[data-testid=ai-mode].briefing:has-text("Briefing")')
    assert(true, 'Shift+Tab twice: Briefing mode')
    const before3 = requests.length
    await page.fill('.ai-composer textarea', 'Brief: an export for accounting')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Who uses the export")', { timeout: 10000 })
    const rb = requests[before3]
    const bn = toolNames(rb)
    assert(rb.messages[0].content.includes('**Briefing mode**') && rb.messages[0].content.includes('Acceptance criteria'), 'system prompt of the Briefing mode')
    assert(bn.includes('ask_user') && bn.includes('kanban_create') && !bn.includes('edit_file') && !bn.includes('exit_plan_mode') && !bn.includes('kanban_add_note'), 'tools of the Briefing mode: ' + bn.join(','))
    await page.click('.ai-ask-option:has-text("Accounting")')
    await page.click('[data-testid=ai-ask-send]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Tickets written.")', { timeout: 15000 })
    const rbr = requests[requests.length - 1].messages.filter((m) => m.role === 'tool')
    const bt = (id) => text(rbr.find((m) => m.tool_call_id === id))
    assert(bt('b1').includes('Ticket #2 created') && bt('b1').includes('now linked'), 'first ticket created, conversation linked: ' + bt('b1'))
    assert(bt('b2').includes('Ticket #3 created') && bt('b2').includes('stays linked to ticket #2'), 'second ticket created: ' + bt('b2'))
    assert(bt('b3') === 'Note added.', 'note added to the linked ticket: ' + bt('b3'))
    assert(bt('b4').includes('Briefing mode'), 'no file change in Briefing mode')
    assert(await page.isVisible('[data-testid=ai-ticket-bar]:has-text("#2")'), 'ticket bar of the created ticket')
    assert(await page.isVisible('.ai-briefing-badge'), 'answers marked Briefing')
    if (!(await page.isVisible('[data-testid=kanban-panel]'))) await page.click('.rail-left .rail-btn[title="Kanban"]')
    await page.click('[data-testid=ticket-card-3]')
    await page.waitForSelector('[data-testid=ticket-view] [data-testid=ticket-title]:has-text("Export settings")')
    await page.waitForSelector('.tk-row:has(.kb-role.r-briefing) [data-testid=ticket-chat]:has-text("Brief:")', { timeout: 5000 })
    assert(true, 'the second ticket lists the briefing conversation')
    await page.click('[data-testid=ticket-card-2]')
    await page.waitForSelector('[data-testid=ticket-note]:has-text("accounting only")', { timeout: 5000 })
    assert(await page.isVisible('.tk-row:has(.kb-role.r-briefing) [data-testid=ticket-chat]:has-text("Brief:")'), 'the first ticket has the note and the conversation')
  } finally {
    fake.close()
  }
})
