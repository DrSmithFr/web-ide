// Orchestrator mode, the default of a new conversation: "what do we work on today?" lists the
// tickets that can start and offers a card (nothing runs before the click; the click starts
// the development); "what did we do yesterday?" reads the history of the kanban; "I have an
// idea" moves the user into a new Briefing conversation where the idea is sent, with a link
// back in the orchestrator thread; Shift+Tab cycles through the four modes.
const http = require('http')
const { run, openProject, assert, OUT } = require('../common.cjs')

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
const today = () => new Date().toISOString().slice(0, 10)

const outputs = {}
const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const sys = text(r.messages[0])
  const last = r.messages[r.messages.length - 1]
  const content = text(last)
  if (sys.includes('**Briefing mode**')) return say(res, "Let's clarify the dark mode.")
  if (!sys.includes('**Orchestrator mode**')) return say(res, 'Developing.')
  outputs.tools = r.tools.map((t) => t.function.name)
  if (content === 'What do we work on today?') return call(res, 'n', 'kanban_next', {})
  if (content.startsWith('Can start now')) {
    outputs.next = content
    return call(res, 'c', 'action_card', { kind: 'start_dev', ticket: 1, label: 'Start #1', reason: 'First in line' })
  }
  if (content === 'What did we do yesterday?') return call(res, 'h', 'kanban_history', { from: today() })
  if (content.startsWith('Tickets that moved')) {
    outputs.history = content
    return say(res, 'Two tickets were planned.')
  }
  if (content.startsWith('I have an idea')) return call(res, 'o', 'open_conversation', { mode: 'briefing', message: 'Idea: a dark mode', send: true })
  if (last.role === 'tool' && last.tool_call_id === 'c') return say(res, 'Click the card to start it.')
  return say(res, 'Done.')
})

run(
  async ({ page }) => {
    page.on('dialog', (d) => d.accept())
    await new Promise((r) => fake.listen(0, '127.0.0.1', r))
    try {
      await openProject(page)
      // Two planned tickets.
      await page.click('.rail-left .rail-btn[title="Kanban"]')
      for (const title of ['Login page', 'Dark theme']) {
        await page.click('[data-testid=kanban-open-board]')
        await page.click('[data-testid=kanban-new]')
        await page.fill('[data-testid=kanban-title]', title)
        await page.click('[data-testid=kanban-create]')
        await page.waitForSelector(`[data-testid=ticket-view]:has-text("${title}")`)
        await page.click('[data-testid=ticket-plan-edit]')
        await page.fill('.tk-md-input', 'The plan')
        await page.click('[data-testid=ticket-plan-save]')
        await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")')
      }

      await page.click('.rail-right .rail-btn[title="AI assistant"]')
      await page.click('.ai-empty button:has-text("Add a model server")')
      await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
      await page.click('.ai-servers button:has-text("Add")')
      await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
      await page.click('.ai-servers .modal-head button')
      await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')
      assert((await page.textContent('.ai-mode')).includes('Orchestrator'), 'a new conversation starts in Orchestrator mode')
      assert(await page.isVisible('.ai-suggestion:has-text("What do we work on today?")'), 'the welcome offers the questions of the day')
      await page.focus('.ai-composer textarea')
      const modes = []
      for (let i = 0; i < 4; i++) {
        await page.keyboard.press('Shift+Tab')
        modes.push((await page.textContent('.ai-mode-label')).trim())
      }
      assert(modes.join(',') === 'Build,Plan,Briefing,Orchestrator', 'Shift+Tab cycles through the modes: ' + modes)

      // 1. What next: a card; nothing runs before the click.
      await page.fill('.ai-composer textarea', 'What do we work on today?')
      await page.keyboard.press('Enter')
      await page.waitForSelector('.ai-msg.assistant .md:has-text("Click the card to start it.")', { timeout: 20000 })
      assert(outputs.next.includes('#1 [To do] (Normal) Login page') && outputs.next.includes('#2 [To do] (Normal) Dark theme'), 'kanban_next lists the tickets that can start: ' + outputs.next)
      assert(!outputs.tools.includes('edit_file') && outputs.tools.includes('open_conversation'), 'the Orchestrator has its tools and changes no file')
      assert((await page.textContent('[data-testid=ai-action]')).includes('First in line'), 'the action card with its reason')
      await page.screenshot({ path: OUT + '/orchestrator-card.png' })
      assert(!(await page.isVisible('.ai-msg.assistant .md:has-text("Developing.")')), 'nothing started before the click')
      await page.click('[data-testid=ai-action-run]')
      // Without git, the development goes on in the project folder (confirmed), here.
      await page.waitForSelector('.ai-msg.assistant .md:has-text("Developing.")', { timeout: 20000 })
      assert(true, 'the click started the development of #1')

      // 2. What did we do: the history of the kanban.
      await page.click('.ai-panel button[title="New conversation"]')
      await page.fill('.ai-composer textarea', 'What did we do yesterday?')
      await page.keyboard.press('Enter')
      await page.waitForSelector('.ai-msg.assistant .md:has-text("Two tickets were planned.")', { timeout: 20000 })
      assert(outputs.history.includes('Login page') && outputs.history.includes('In progress'), 'kanban_history lists the moves of the period: ' + outputs.history)

      // 3. An idea: a Briefing conversation opens for the user, the idea sent.
      await page.click('.ai-panel button[title="New conversation"]')
      await page.fill('.ai-composer textarea', 'I have an idea: a dark mode')
      await page.keyboard.press('Enter')
      await page.waitForSelector('.ai-msg.assistant .md:has-text("Let\'s clarify the dark mode.")', { timeout: 20000 })
      assert((await page.textContent('.ai-mode')).includes('Briefing'), 'the view moved to a Briefing conversation')
      assert(await page.isVisible('.ai-msg.user:has-text("Idea: a dark mode")'), 'the idea was sent in it')
      await page.screenshot({ path: OUT + '/orchestrator-briefing.png' })
      if (!(await page.isVisible('[data-testid=ai-sidebar]'))) await page.click('.ai-panel button[title="Conversations of the project"]')
      await page.click('.ai-chat-open:has-text("I have an idea")')
      await page.waitForSelector('[data-testid=ai-opened]')
      assert(true, 'the orchestrator thread keeps a link to the conversation it opened')
    } finally {
      fake.close()
    }
  },
  { orchestrator: true },
)
