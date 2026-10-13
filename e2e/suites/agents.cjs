// Agents tool: ACP sessions with the fake agent of e2e/bin (claude-agent-acp): opened from
// the launcher, a prompt streams thought, plan, message and an edit with its diff; a
// permission card; a config option; Stop; the agent killed is restarted and its session
// loaded; closing the tab ends the session. Work time and cost: on the tab, the project, and
// the ticket of a session run in its worktree.
const { execSync } = require('child_process')
const { run, openProject, assert, mcp, OUT, WS } = require('../common.cjs')

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
  // Time and cost of the turn on the tab (the fake runs on a plan: an API equivalent).
  await page.waitForFunction(() => /Cost: \$0\.\d+ \(API equivalent\)/.test(document.querySelector('[data-testid=agent-tab]')?.title ?? ''))
  assert(/Work: \d+ s/.test(await page.getAttribute(tab, 'title')), 'work time on the tab')
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

  // The turns stay counted for the project once the session is closed.
  await page.waitForFunction(() => /\$0\.\d+/.test(document.querySelector('[data-testid=project-agent-stats-value]')?.textContent ?? ''))

  // A session in the worktree of a ticket: its time and cost on the ticket.
  const home = page.url().replace(/[?#].*$/, '')
  // A ticket branch needs a first commit.
  const env = { ...process.env, GIT_AUTHOR_NAME: 'E2E', GIT_AUTHOR_EMAIL: 'e2e@test', GIT_COMMITTER_NAME: 'E2E', GIT_COMMITTER_EMAIL: 'e2e@test' }
  execSync('git add -A && git commit -q -m "initial state"', { cwd: WS + '/demo', env })
  await mcp('kanban_create', { title: 'Timed by the agents' })
  const created = await mcp('kanban_list', {})
  const n = Math.max(...[...JSON.stringify(created).matchAll(/#(\d+)/g)].map((m) => +m[1]))
  await mcp('kanban_set_plan', { id: n, plan: 'Do it.', goals: [{ title: 'Done' }], size: 's', complexity: 'low' })
  const started = JSON.stringify(await mcp('kanban_start', { id: n }))
  const wt = started.match(/project\/([\w-]+-t\d+)/)?.[1]
  assert(wt, 'ticket started in a worktree: ' + started.slice(0, 300))
  await page.goto(home.replace(/\/project\/[^/]+$/, '/project/' + wt))
  await page.waitForSelector('.menubar')
  if (!(await page.isVisible('[data-testid=agents-launcher]'))) await page.click('.rail-btn[data-id=agents]')
  await page.click('[data-testid=agent-card-claude] button:has-text("Open")')
  await waitState(page, 'ready')
  await prompt(page, 'hello')
  await page.waitForSelector('[data-testid=agent-user]:has-text("hello")')
  await waitState(page, 'ready')
  await page.goto(home + '?ticket=' + n)
  await page.waitForSelector('[data-testid=ticket-agent-stats]')
  const badge = await page.textContent('[data-testid=ticket-agent-stats]')
  assert(/^Agents: \d+ s · \$0\.\d+$/.test(badge), 'time and cost on the ticket: ' + badge)
})
