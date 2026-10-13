// Agents tool: ACP sessions with the fake agent of e2e/bin (claude-agent-acp): opened from
// the launcher, a prompt streams thought, plan, message and an edit with its diff; a
// permission card; a config option; Stop; the agent killed is restarted and its session
// loaded; closing the tab ends the session.
const { run, openProject, assert, OUT } = require('../common.cjs')

const tab = '[data-testid=agent-tab]'
const state = (page) => page.getAttribute(tab, 'data-state')
const waitState = (page, s) => page.waitForSelector(`${tab}[data-state=${s}]`, { timeout: 15000 })

async function prompt(page, text) {
  await page.fill('[data-testid=agent-input]', text)
  await page.keyboard.press('Enter')
}

run(async ({ page }) => {
  await openProject(page)
  await page.click('.rail-btn[data-id=agents]')
  await page.waitForSelector('[data-testid=agents-launcher] [data-testid=agent-card-claude]')
  const card = await page.textContent('[data-testid=agent-card-claude]')
  assert(card.includes('Claude Code') && card.includes('Installed'), 'the fake Claude adapter is found in the PATH: ' + card)

  // Open a session: a tab, starting then ready.
  await page.click('[data-testid=agent-card-claude] button:has-text("Open")')
  await page.waitForSelector(tab)
  await waitState(page, 'ready')
  assert((await page.textContent(`${tab} .tab-title`)) === 'Claude Code', 'tab named after the agent')

  // A prompt: the turn streams, then the tab is ready again.
  await prompt(page, 'hello')
  await page.waitForSelector('[data-testid=agent-user]:has-text("hello")')
  await waitState(page, 'ready')
  await page.waitForSelector('[data-testid=agent-message] strong:has-text("fake agent")')
  assert((await page.textContent('[data-testid=agent-message]')).includes('Hello from the fake agent.'), 'chunks joined in one message')
  assert(await page.isVisible('[data-testid=agent-thought]'), 'thought shown')
  const plan = await page.textContent('[data-testid=agent-plan]')
  assert(plan.includes('Say hello') && plan.includes('Edit the file'), 'plan shown: ' + plan)
  const diff = await page.$$eval('[data-testid=agent-diff] .ai-diff > div', (l) => l.map((d) => d.className + ':' + d.textContent))
  assert(diff.includes('del:- hi') && diff.includes('add:+ hello') && diff.includes(':  there'), 'edit with its diff: ' + JSON.stringify(diff))
  await page.waitForFunction(() => document.querySelector('[data-testid=agent-tab] .tab-title')?.textContent === 'Fake session')
  assert((await page.textContent('[data-testid=agent-usage]')).replace(/\s/g, '') === '1.2k/200k', 'context usage shown')
  await page.screenshot({ path: OUT + '/agents-turn.png' })

  // A permission: a card in the thread, the tab waits for the user.
  await prompt(page, 'permission please')
  await page.waitForSelector('[data-testid=agent-permission][data-outcome=""]')
  assert((await state(page)) === 'asking', 'tab asking')
  assert((await page.textContent('[data-testid=agent-permission]')).includes('Allow: Edit hello.txt?'), 'permission titled by its tool call')
  await page.click('[data-testid=agent-option-allow-once]')
  await page.waitForSelector('[data-testid=agent-permission][data-outcome=allow-once]')
  await page.waitForSelector('[data-testid=agent-message]:has-text("Edited.")')
  await waitState(page, 'ready')

  // A config option of the agent.
  await page.selectOption('[data-testid=agent-config-mode]', 'bypass')
  await page.waitForFunction(() => document.querySelector('[data-testid=agent-config-mode]')?.value === 'bypass')

  // Stop during a turn.
  await prompt(page, 'slow')
  await page.waitForSelector('[data-testid=agent-message]:has-text("tick 3")')
  assert((await state(page)) === 'busy', 'tab busy')
  await page.click('[data-testid=agent-stop]')
  await page.waitForSelector('[data-testid=agent-notice]:has-text("Stopped.")')
  await waitState(page, 'ready')

  // The agent dies: restarted with its session loaded; the thread stays, without the replay.
  const users = await page.$$eval('[data-testid=agent-user]', (l) => l.length)
  await prompt(page, 'crash')
  await page.waitForSelector('[data-testid=agent-notice]:has-text("it is restarted and its session loaded")')
  await waitState(page, 'ready')
  assert(!(await page.isVisible('text=replayed answer')), 'the replay of session/load is not shown')
  await prompt(page, 'hello again')
  await page.waitForSelector('[data-testid=agent-user]:has-text("hello again")')
  await waitState(page, 'ready')
  assert((await page.$$eval('[data-testid=agent-user]', (l) => l.length)) === users + 2, 'thread kept across the restart')

  // A reload gets the thread back from the pod.
  await page.reload()
  await page.waitForSelector('.menubar')
  if (!(await page.isVisible('[data-testid=agent-session]'))) await page.click('.rail-btn[data-id=agents]')
  await page.waitForSelector('[data-testid=agent-user]:has-text("hello again")')

  // Closing the tab ends the session.
  await page.click(`${tab} .tab-close`)
  await page.waitForSelector('[data-testid=agents-launcher]')
  assert((await page.$$(tab)).length === 0, 'session closed')
})
