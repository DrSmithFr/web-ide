// Milestones: one created from a ticket, its user stories written by Claude (MCP), the
// reminder in How to test, the milestone page validating its ticket To test, a feedback made
// into a ticket, then the milestone validated by the user once everything is done.
const { run, openProject, assert, mcp } = require('../common.cjs')

run(async ({ page }) => {
  page.on('dialog', (d) => d.accept())
  await openProject(page)
  await page.click('.rail-left .rail-btn[title="Kanban"]')
  await page.waitForSelector('[data-testid=kanban-panel]')
  await page.click('[data-testid=kanban-open-board]')
  await page.waitForSelector('[data-testid=kanban-board]')
  await mcp('kanban_create', { title: 'CSV export' })
  await mcp('kanban_set_plan', { id: 1, plan: 'Write the CSV', goals: [], size: 's', complexity: 'low' })
  await page.click('[data-testid=ticket-card-1]')
  await page.waitForSelector('[data-testid=ticket-view] [data-testid=ticket-status]:has-text("To do")')

  // A new milestone from the ticket.
  await page.selectOption('select[data-testid=ticket-milestone]', '-1')
  await page.waitForSelector('.modal input')
  await page.fill('.modal input', 'Exports')
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => document.querySelector('select[data-testid=ticket-milestone]')?.value === '1')
  assert(true, 'a milestone created from the ticket, which joins it')
  await page.waitForSelector('[data-testid=card-milestone]:has-text("Exports")')
  assert(true, 'the card shows its milestone')

  // Claude writes a user story; the ticket To test recalls the milestone and its stories.
  const story = await mcp('kanban_milestone', { action: 'story_add', id: 1, text: 'As a user, I export my orders as CSV' })
  assert(story.result?.content?.[0]?.text.includes('User story added'), 'a user story written through the MCP endpoint')
  await page.click('.pane .tab:has-text("#1")')
  await page.click('[data-testid=ticket-start]')
  await page.waitForSelector('[data-testid=ticket-status]:has-text("In progress")')
  await page.click('[data-testid=ticket-to-test]')
  await page.waitForSelector('[data-testid=ticket-status]:has-text("To test")')
  await page.waitForSelector('[data-testid=ticket-milestone-reminder]:has-text("As a user, I export my orders as CSV")')
  assert((await page.textContent('[data-testid=ticket-milestone-reminder]')).includes('Exports'), 'How to test recalls the milestone and its user stories')

  // The milestone page: the ticket To test is validated there.
  await page.click('[data-testid=ticket-milestone-open]')
  await page.waitForSelector('[data-testid=milestone-view]')
  assert((await page.textContent('[data-testid=milestone-state]')).includes('Ongoing'), 'an open milestone is ongoing')
  assert(await page.isDisabled('[data-testid=milestone-validate]'), 'the milestone waits for its ticket and its story')
  await page.waitForSelector('[data-testid=milestone-totest]:has-text("CSV export")')
  await page.click('[data-testid=milestone-ticket-close]')
  await page.waitForSelector('[data-testid=milestone-progress]:has-text("1/1")')
  assert(true, 'the ticket To test validated from the milestone page')

  // A feedback of the milestone made into a ticket of it.
  await page.selectOption('[data-testid=milestone-feedback-kind]', 'bug')
  await page.fill('[data-testid=milestone-feedback-input]', 'Dates are not ISO')
  await page.click('[data-testid=milestone-feedback-add]')
  await page.click('[data-testid=milestone-feedback-ticket]')
  await page.waitForSelector('[data-testid=milestone-feedback-link]:has-text("#2")')
  assert((await page.textContent('[data-testid=milestone-progress]')).includes('1/2'), 'the feedback became a ticket of the milestone')
  assert((await page.getAttribute('[data-testid=milestone-validate]', 'title')).includes('ticket'), 'validation waits for that ticket')

  // Everything done: the user validates the milestone.
  await page.click('[data-testid=milestone-feedback-link]')
  await page.waitForSelector('[data-testid=ticket-view] .tk-num:has-text("#2")')
  await page.click('.pane.active [data-testid=ticket-more]')
  await page.click('.ctx-menu button:has-text("Delete the ticket")')
  await page.click('.pane .tab:has-text("Exports")')
  await page.waitForSelector('[data-testid=milestone-progress]:has-text("1/1")')
  await page.click('[data-testid=milestone-feedback-check]')
  await page.click('[data-testid=milestone-story-check]')
  await page.waitForSelector('[data-testid=milestone-validate]:not([disabled])')
  await page.click('[data-testid=milestone-validate]')
  await page.waitForSelector('[data-testid=milestone-state]:has-text("Validated")')
  assert(await page.isVisible('[data-testid=milestone-reopen]'), 'the milestone validated by the user, then reopenable')
  const refused = await mcp('kanban_milestone', { action: 'validate', id: 1 })
  assert(refused.result?.isError, 'a model cannot validate a milestone')
})
