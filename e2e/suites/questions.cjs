// Questions tool: a form of an ACP session (the fake agent's elicitation/create) opens by
// itself in a modal with the last message of the agent, Later and the list, the answer
// reaches the agent; a question withdrawn leaves; the setting off and the override of a
// session keep the modal closed; a question of the session shown stays in its thread; an
// MCP ask_user is answered in the modal and returned to its caller.
const { run, openProject, assert, mcp, OUT } = require('../common.cjs')

const tab = '[data-testid=agent-tab]'
const waitState = (page, s) => page.waitForSelector(`${tab}[data-state=${s}]`, { timeout: 15000 })
const box = '[data-testid=agent-input] .ed-content'
const modal = '[data-testid=question-modal]'
const items = '[data-testid=question-item]'

async function prompt(page, text) {
  await page.click(box)
  await page.keyboard.press('Control+a')
  await page.keyboard.press('Delete')
  await page.keyboard.type(text)
  await page.keyboard.press('Control+Enter')
}
const showAgents = (page) => page.click('.rail-btn[data-id=agents]')
const showQuestions = (page) => page.click('.rail-btn[data-id=questions]')
const lastMessage = (page) => page.$$eval('[data-testid=agent-message]', (l) => l[l.length - 1]?.textContent ?? '')

run(async ({ page }) => {
  await openProject(page)
  await showAgents(page)
  await page.click('[data-testid=agent-card-claude] button:has-text("Open")')
  await waitState(page, 'ready')

  // The agent asks while the Questions tool is shown: the modal opens by itself.
  await prompt(page, 'ask me later')
  await showQuestions(page)
  await page.waitForSelector(`${modal}[data-kind=form]`, { timeout: 15000 })
  const last = await page.textContent('[data-testid=question-last]')
  assert(last.includes('favourite color'), 'the last message of the agent above the question: ' + last)
  assert((await page.textContent('.q-modal .modal-head')).includes('Claude Code asks'), 'the asker in the title')
  assert(await page.isVisible('.rail-btn[data-id=questions] .rail-badge'), 'a badge on the rail')
  await page.screenshot({ path: OUT + '/questions-modal.png' })
  // Later: closed, still listed; a click opens it again.
  await page.click('[data-testid=question-later]')
  await page.waitForSelector(modal, { state: 'detached' })
  assert((await page.$$(items)).length === 1, 'still listed')
  await page.click(items)
  await page.waitForSelector(modal)
  await page.click(`${modal} .q-choice:has-text("Blue") input`)
  await page.fill(`${modal} [data-field=count] input`, '3')
  await page.click(`${modal} [data-testid=question-submit]`)
  await page.waitForSelector(modal, { state: 'detached' })
  await page.waitForSelector('[data-testid=questions-tool] .empty-state')
  await showAgents(page)
  await waitState(page, 'ready')
  await page.waitForSelector('[data-testid=agent-message]:has-text("You chose")')
  const said = await lastMessage(page)
  assert(said.includes('You chose') && said.includes('"question_0":"Blue"') && said.includes('"count":3'), 'the answer reaches the agent: ' + said)
  assert(await page.isVisible('[data-testid=agent-question][data-outcome=accept]'), 'the card of the thread shows the answer')

  // A question of the session shown is answered in its thread: no modal.
  await prompt(page, 'ask me')
  await page.waitForSelector('[data-testid=agent-question][data-outcome=""]')
  await page.waitForTimeout(400)
  assert(!(await page.$(modal)), 'no modal over the thread that shows the question')
  await page.click('[data-testid=agent-question][data-outcome=""] [data-testid=question-decline]')
  await page.waitForSelector('[data-testid=agent-message]:has-text("Skipped.")')
  await waitState(page, 'ready')

  // The agent withdraws its question: the modal and the item go.
  await prompt(page, 'ask then withdraw later')
  await showQuestions(page)
  await page.waitForSelector(modal, { timeout: 15000 })
  await page.waitForSelector(modal, { state: 'detached', timeout: 15000 })
  await page.waitForSelector('[data-testid=questions-tool] .empty-state')

  // The setting off: listed, no modal; opened from the list.
  await page.click('[data-testid=questions-auto] input')
  await showAgents(page)
  await waitState(page, 'ready')
  await prompt(page, 'permission later')
  await showQuestions(page)
  await page.waitForSelector(`${items}[data-source=acp]`, { timeout: 15000 })
  await page.waitForTimeout(400)
  assert(!(await page.$(modal)), 'setting off: no modal')
  await page.click(items)
  await page.waitForSelector(`${modal}[data-kind=permission]`)
  await page.click(`${modal} [data-testid=question-option-allow-once]`)
  await page.waitForSelector(modal, { state: 'detached' })
  await showAgents(page)
  await page.waitForSelector('[data-testid=agent-message]:has-text("Edited.")')
  await waitState(page, 'ready')

  // Setting on, but this session never opens its questions by itself (its tab menu).
  await showQuestions(page)
  await page.click('[data-testid=questions-auto] input')
  await showAgents(page)
  await page.click(tab, { button: 'right' })
  await page.click('.ctx-menu >> text=Its questions never open by themselves')
  await prompt(page, 'ask me later')
  await showQuestions(page)
  await page.waitForSelector(items, { timeout: 15000 })
  await page.waitForTimeout(400)
  assert(!(await page.$(modal)), 'override of the session: no modal')
  await page.click(items)
  await page.click(`${modal} [data-testid=question-cancel]`)
  await page.waitForSelector(modal, { state: 'detached' })
  await showAgents(page)
  await page.waitForSelector('[data-testid=agent-message]:has-text("Withdrawn.")')

  // ask_user of a CLI agent through MCP: the modal opens, the answers return to the call.
  await showQuestions(page)
  const call = mcp('ask_user', { questions: [{ question: 'Which database?', options: ['SQLite', 'Postgres'] }], context: 'The tests need a database.' })
  await page.waitForSelector(`${modal}[data-kind=ask]`, { timeout: 15000 })
  assert((await page.textContent('[data-testid=question-last]')).includes('The tests need a database.'), 'the context of the MCP call')
  await page.click(`${modal} .ai-ask-option:has-text("Postgres")`)
  if (await page.isVisible(`${modal} [data-testid=ai-ask-next]`)) await page.click(`${modal} [data-testid=ai-ask-next]`)
  await page.click(`${modal} [data-testid=ai-ask-send]`)
  await page.waitForSelector(modal, { state: 'detached' })
  const r = await call
  const text = r.result?.content?.[0]?.text ?? JSON.stringify(r)
  assert(text.includes('Postgres'), 'the answers returned to the MCP call: ' + text)
})
