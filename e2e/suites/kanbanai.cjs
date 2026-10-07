// Kanban and the assistant: ask_user (questions one at a time, recap, answers sent back),
// kanban_create / kanban_list from any conversation, writing tools only when linked,
// Briefing mode (tickets created and linked to the conversation).
const http = require('http')
const { run, openProject, assert, OUT, WS } = require('../common.cjs')

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
    // Claude Code writes in the conversation (MCP kanban_reply), then answers its question.
    if (last.role === 'user' && String(last.content).includes('Claude Code here'))
      return sse(res, { tool_calls: calls([['c0', 'ask_user', { questions: [{ question: 'CSV or JSON?', options: [{ label: 'CSV' }, { label: 'JSON' }] }] }]]) }), end(res, 'tool_calls')
    if (last.tool_call_id === 'c0') {
      sse(res, { content: 'Thanks, Claude: ' + text(last) })
      return end(res)
    }
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
  if (firstUser.startsWith('Types:')) {
    // One question of each type (choice, idea, compare, rank, scenario) plus a legacy one,
    // then an invalid call (compare with three options) that must be refused.
    if (last.role === 'user')
      return (
        sse(res, {
          tool_calls: calls([
            [
              't1',
              'ask_user',
              {
                questions: [
                  { question: 'Which color?', type: 'choice', options: [{ label: 'Blue', pros: ['calm'] }, { label: 'Red', cons: ['loud'] }] },
                  { question: 'Should the export include a header row?', type: 'idea' },
                  { question: 'CSV or JSON as the default format?', type: 'compare', options: [{ label: 'CSV', pros: ['simple'] }, { label: 'JSON', pros: ['structured'] }] },
                  { question: 'Rank the fields by importance.', type: 'rank', top: 2, options: [{ label: 'id' }, { label: 'total' }, { label: 'date' }, { label: 'email' }] },
                  { question: 'When the file is empty, which file is written?', type: 'scenario', situation: 'The export runs at midnight and no rows are produced.', options: [{ label: 'None' }, { label: 'An empty file' }] },
                  { question: 'Which priority?', options: [{ label: 'High' }, { label: 'Normal' }] },
                  { question: 'Anything else to add?', options: [{ label: 'Yes' }, { label: 'No' }] },
                ],
              },
            ],
            [
              't1bad',
              'ask_user',
              {
                questions: [
                  { question: 'How many formats?', type: 'compare', options: [{ label: 'CSV' }, { label: 'JSON' }, { label: 'XML' }] },
                ],
              },
            ],
          ]),
        }),
        end(res, 'tool_calls')
      )
    if (msgs.some((m) => m.role === 'tool' && m.tool_call_id === 't1')) return sse(res, { content: 'Types noted.' }), end(res)
    sse(res, { content: 'Types done.' })
    return end(res)
  }
  if (firstUser.startsWith('Graph:') || firstUser.startsWith('GraphOff:')) {
    // A small graph: Mode (Solo → Difficulty → Hard → Permadeath; Coop → Players), then
    // Platform; plus two invalid graphs (unknown next, a cycle) refused by the pod.
    const graph = [
      { question: 'Mode?', id: 'a', options: [{ label: 'Solo', next: 'c' }, { label: 'Coop', next: 'd' }] },
      { question: 'Platform?', options: [{ label: 'PC' }, { label: 'Phone' }] },
      { question: 'Difficulty?', id: 'c', options: [{ label: 'Hard', next: 'e' }, { label: 'Easy' }] },
      { question: 'How many players?', id: 'd', options: [{ label: '2' }, { label: '4' }] },
      { question: 'Permadeath?', id: 'e', type: 'idea' },
    ]
    if (last.role === 'user')
      return (
        sse(res, {
          tool_calls: calls([
            ['g1', 'ask_user', { questions: graph }],
            ['g2', 'ask_user', { questions: [{ question: 'Lost?', options: [{ label: 'x', next: 'zz' }, { label: 'y' }] }] }],
            [
              'g3',
              'ask_user',
              {
                questions: [
                  { question: 'Start?', options: [{ label: 'x', next: 'p' }, { label: 'y' }] },
                  { question: 'P?', id: 'p', options: [{ label: 'x', next: 'q' }, { label: 'y' }] },
                  { question: 'Q?', id: 'q', options: [{ label: 'x', next: 'p' }, { label: 'y' }] },
                ],
              },
            ],
          ]),
        }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Graph noted.' })
    return end(res)
  }
  if (firstUser.startsWith('TypesM:')) {
    // One idea (answered by swiping the card) and one compare (stacked on a mobile viewport).
    if (last.role === 'user')
      return (
        sse(res, {
          tool_calls: calls([
            [
              'mm',
              'ask_user',
              {
                questions: [
                  { question: 'Ship it right away?', type: 'idea' },
                  { question: 'Drawer on the left or the right?', type: 'compare', options: [{ label: 'Left' }, { label: 'Right' }] },
                ],
              },
            ],
          ]),
        }),
        end(res, 'tool_calls')
      )
    if (msgs.some((m) => m.role === 'tool' && m.tool_call_id === 'mm')) return sse(res, { content: 'Mobile types noted.' }), end(res)
    sse(res, { content: 'Mobile types noted.' })
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

    // The question types: one of each (choice, idea, compare, rank, scenario), a legacy one,
    // a note, "I don't know", and an invalid call (compare with three options) refused.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer textarea', 'Types: which widgets')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Which color?")', { timeout: 10000 })
    // Q1 choice: a note, then the pick (a single choice moves on by itself).
    await page.click('[data-testid=ai-ask-note]')
    await page.fill('[data-testid=ai-ask-note-input]', 'prefers it calm')
    await page.click('.ai-ask-option:has-text("Blue")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("header row")')
    // Q2 idea: "Yes, but…" opens the text and keeps the user (no auto-advance).
    await page.click('[data-testid=ai-ask-idea-but]')
    await page.fill('[data-testid=ai-ask-idea-text]', 'and a footer too')
    assert(await page.isVisible('[data-testid=ai-ask-question]:has-text("header row")'), 'the idea stays while the "Yes, but…" text is typed')
    await page.click('[data-testid=ai-ask-next]')
    // Q3 compare: a pick of the two.
    await page.waitForSelector('[data-testid=ai-ask-compare]')
    await page.click('.ai-ask-compare .ai-ask-option:has-text("JSON")')
    await page.waitForSelector('[data-testid=ai-ask-rank]')
    // Q4 rank: move "email" to the top, then next.
    const email = page.locator('[data-testid=ai-ask-rank-item]').filter({ hasText: 'email' })
    await email.locator('[data-testid=ai-ask-rank-up]').click()
    await email.locator('[data-testid=ai-ask-rank-up]').click()
    await email.locator('[data-testid=ai-ask-rank-up]').click()
    await page.click('[data-testid=ai-ask-next]')
    // Q5 scenario: the situation is shown, then a pick.
    await page.waitForSelector('[data-testid=ai-ask-situation]')
    assert((await page.textContent('[data-testid=ai-ask-situation]')).includes('at midnight'), 'the scenario situation is shown')
    await page.click('.ai-ask-scenario .ai-ask-option:has-text("None")')
    // Q6 legacy (no type): "I don't know".
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Which priority?")')
    await page.click('[data-testid=ai-ask-dontknow]')
    await page.click('[data-testid=ai-ask-next]')
    // Q7: "Up to you" (the other special answer).
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Anything else")')
    await page.click('[data-testid=ai-ask-uptoyou]')
    await page.click('[data-testid=ai-ask-next]')
    await page.waitForSelector('.ai-ask-recap')
    const typesRecap = await page.textContent('.ai-ask-recap')
    assert(typesRecap.includes('Blue') && typesRecap.includes('and a footer too') && typesRecap.includes('JSON') && typesRecap.includes('None') && typesRecap.includes('1. email, 2. id') && typesRecap.includes("I don't know") && typesRecap.includes('Up to you'), 'recap: ' + typesRecap)
    await page.screenshot({ path: OUT + '/ask-types.png' })
    await page.click('[data-testid=ai-ask-send]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Types noted.")', { timeout: 15000 })
    const t1res = requests[requests.length - 1].messages.filter((m) => m.role === 'tool')
    const t1 = text(t1res.find((m) => m.tool_call_id === 't1'))
    assert(t1.includes('[choice]') && t1.includes('Blue') && t1.includes('and a footer too') && t1.includes('[compare]') && t1.includes('JSON') && t1.includes('[rank]') && t1.includes('1. email, 2. id (the top 2 only)') && t1.includes('[scenario]') && t1.includes('None') && t1.includes('I don\'t know (the user does not know: offer concrete examples or options)') && t1.includes('Up to you (left to you: decide and say what you chose)') && t1.includes('note: prefers it calm'), 'answers by type sent back: ' + t1)
    const t1bad = t1res.find((m) => m.tool_call_id === 't1bad')
    assert(t1bad && text(t1bad).includes('compare') && text(t1bad).includes('exactly 2 options') && text(t1bad).includes('it has 3'), 'invalid compare refused, naming the rule: ' + (t1bad ? text(t1bad) : '(missing)'))
    assert(await page.isVisible('[data-testid=ai-ask] .badge:has-text("answered")'), 'types card marked answered')
    const folded = await page.textContent('[data-testid=ai-ask]:has(.badge:has-text("answered")):has-text("Which color?")')
    assert(folded.includes('Blue') && folded.includes('prefers it calm') && folded.includes('1. email, 2. id'), 'the folded card shows the answers and the note: ' + folded)

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

    // Claude Code replies in the briefing conversation, then answers the question of the assistant.
    const mcpTool = (name, args) =>
      fetch(process.env.E2E_URL + '/mcp', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + process.env.E2E_TOKEN },
        body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: { cwd: WS + '/demo', id: 2, ...args } } }),
      })
        .then((r) => r.json())
        .then((r) => ({ text: r.result.content[0].text, failed: !!r.result.isError }))
    const chatId = (await mcpTool('kanban_get', {})).text.match(/\(chat ([a-z0-9]+)\)/)[1]
    const sent = await mcpTool('kanban_reply', { chat: chatId, message: 'Claude Code here: which format?' })
    assert(!sent.failed && sent.text.includes('Message sent'), 'kanban_reply: ' + sent.text)
    await page.waitForSelector('[data-testid=ai-author-claude]', { timeout: 10000 })
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("CSV or JSON?")', { timeout: 10000 })
    assert((await page.textContent('.ai-msg.user:has([data-testid=ai-author-claude])')).includes('which format?'), 'the message of Claude is shown with its badge')
    const pendingText = (await mcpTool('kanban_conversation', { chat: chatId })).text
    assert(pendingText.includes('Waiting for the answers') && pendingText.includes('[options: CSV | JSON]'), 'kanban_conversation lists the waiting questions: ' + pendingText)
    const answered = await mcpTool('kanban_answer', { chat: chatId, answers: [['JSON']] })
    assert(!answered.failed, 'kanban_answer: ' + answered.text)
    await page.waitForSelector('[data-testid=ai-ask-by-claude]', { timeout: 10000 })
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Thanks, Claude")', { timeout: 10000 })
    assert((await page.textContent('.ai-msg.assistant:not(.live) .md:has-text("Thanks, Claude")')).includes('JSON'), 'the assistant gets the answers of Claude')

    // Mobile: the idea is answered by swiping the card (right = Yes), and the compare stacks.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer textarea', 'TypesM: swipe')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-idea]', { timeout: 10000 })
    await page.$eval('[data-testid=ai-ask-idea]', (el) => {
      const target = el.querySelector('.ai-ask-idea-proposal')
      const r = target.getBoundingClientRect()
      const x = r.x + r.width / 2
      const y = r.y + r.height / 2
      const fire = (type, cx, cy) =>
        target.dispatchEvent(
          new PointerEvent(type, { bubbles: true, cancelable: true, pointerId: 1, pointerType: 'touch', isPrimary: true, clientX: cx, clientY: cy, button: 0 }),
        )
      fire('pointerdown', x, y)
      fire('pointermove', x + 50, y)
      fire('pointermove', x + 130, y)
      fire('pointerup', x + 150, y)
    })
    await page.waitForSelector('[data-testid=ai-ask-compare]', { state: 'visible', timeout: 10000 })
    assert(true, 'swiping the idea card right answers "Yes" and moves on to the compare')
    // Narrow viewport (the phone layout, the assistant in use in front): the card goes on
    // where it was, the two cards stack (a single grid column).
    await page.setViewportSize({ width: 390, height: 844 })
    await page.waitForSelector('[data-testid=ai-ask-compare]', { state: 'visible', timeout: 10000 })
    const tracks = await page.$eval('[data-testid=ai-ask-compare]', (el) => getComputedStyle(el).gridTemplateColumns.split(' ').length)
    await page.screenshot({ path: OUT + '/ask-types-mobile.png' })
    assert(tracks === 1, 'compare stacked on a mobile viewport (tracks: ' + tracks + ')')
    await page.click('.ai-ask-compare .ai-ask-option:has-text("Left")')
    await page.click('[data-testid=ai-ask-next]')
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.click('[data-testid=ai-ask-send]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Mobile types noted.")', { timeout: 15000 })
    const mm = text(requests[requests.length - 1].messages.filter((m) => m.role === 'tool').find((m) => m.tool_call_id === 'mm'))
    assert(mm.includes('[idea]') && mm.includes('→ Yes') && mm.includes('[compare]') && mm.includes('Left'), 'swipe (Yes) and compare (Left) sent back: ' + mm)
    assert(true, 'mobile: swipe and compare answered')

    // A graph of questions: the branch of the answer only, a breadcrumb, Previous + another
    // answer changing the path, invalid graphs refused.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer textarea', 'Graph: a game')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Mode?")', { timeout: 10000 })
    await page.click('.ai-ask-option:has-text("Solo")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Difficulty?")')
    await page.click('.ai-ask-option:has-text("Hard")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Permadeath?")')
    assert((await page.textContent('[data-testid=ai-ask-question] [data-testid=ai-ask-crumbs]')) === 'Solo › Hard', 'breadcrumb of the branch')
    await page.click('[data-testid=ai-ask-idea-yes]')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Platform?")')
    for (let i = 0; i < 3; i++) await page.click('.ai-ask .btn:has-text("Previous")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Mode?")')
    await page.click('.ai-ask-option:has-text("Coop")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("How many players?")')
    await page.click('.ai-ask-option:has-text("4")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Platform?")')
    await page.click('.ai-ask-option:has-text("PC")')
    await page.click('[data-testid=ai-ask-next]')
    await page.waitForSelector('.ai-ask-recap')
    const graphRecap = await page.textContent('.ai-ask-recap')
    assert(graphRecap.includes('How many players?') && !graphRecap.includes('Difficulty?') && graphRecap.includes('Coop'), 'recap follows the new branch: ' + graphRecap)
    await page.click('[data-testid=ai-ask-send]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Graph noted.")', { timeout: 15000 })
    let gres = requests[requests.length - 1].messages.filter((m) => m.role === 'tool')
    const g1 = text(gres.find((m) => m.tool_call_id === 'g1'))
    assert(g1.includes('2. [choice] Coop › How many players?\n   → 4') && g1.includes('3. [choice] Platform?') && g1.includes('Not asked (branch not taken): "Difficulty?", "Permadeath?".'), 'path sent back: ' + g1)
    assert(text(gres.find((m) => m.tool_call_id === 'g2')).includes('next "zz" names no question'), 'unknown next refused')
    assert(text(gres.find((m) => m.tool_call_id === 'g3')).includes('p → q → p form a cycle'), 'cycle refused')

    // Leaving the path: a free answer on a question with branches sends the round at once.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer textarea', 'GraphOff: a game')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Mode?")', { timeout: 10000 })
    await page.click('.ai-ask-option:has-text("Solo")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Difficulty?")')
    await page.fill('[data-testid=ai-ask-free]', 'Medium')
    assert((await page.textContent('[data-testid=ai-ask-next]')).includes('Send the answers'), 'Next becomes Send when leaving the path')
    await page.click('[data-testid=ai-ask-next]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Graph noted.")', { timeout: 15000 })
    gres = requests[requests.length - 1].messages.filter((m) => m.role === 'tool')
    const off = text(gres.find((m) => m.tool_call_id === 'g1'))
    assert(off.includes('left the anticipated path at "Difficulty?" with: Medium') && off.includes('Not asked: "Platform?", "How many players?", "Permadeath?".'), 'off path sent back: ' + off)
    await page.waitForSelector('[data-testid=ai-ask-offpath]')
    const offCard = await page.textContent('[data-testid=ai-ask]:has([data-testid=ai-ask-offpath])')
    assert(offCard.includes('Solo') && offCard.includes('Medium') && !offCard.includes('Platform?'), 'folded card: the path and the badge: ' + offCard)
  } finally {
    fake.close()
  }
})
