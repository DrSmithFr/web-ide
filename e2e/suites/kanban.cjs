// Kanban: board, new ticket, ticket view by stage (description, notes, plan, goals, files,
// attachments), workflow buttons, test feedback, side panel, sync with a second window.
const fs = require('fs')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

run(async ({ page, ctx }) => {
  page.on('dialog', (d) => d.accept())
  await openProject(page)
  await page.click('.rail-left .rail-btn[title="Kanban"]')
  await page.waitForSelector('[data-testid=kanban-panel]')
  await page.click('[data-testid=kanban-open-board]')
  await page.waitForSelector('[data-testid=kanban-board]')
  assert((await page.$$('.kb-col')).length === 4, 'four active columns: New, To do, In progress, To test')

  // New ticket.
  await page.click('[data-testid=kanban-new]')
  await page.fill('[data-testid=kanban-title]', 'CSV export of the orders')
  await page.fill('[data-testid=kanban-description]', 'Add a CSV **export**.')
  await page.click('[data-testid=kanban-create]')
  await page.waitForSelector('[data-testid=ticket-view]')
  assert((await page.textContent('[data-testid=ticket-title]')).includes('CSV export'), 'ticket opened in a tab')
  assert((await page.textContent('[data-testid=ticket-status]')).includes('New'), 'status New')
  await page.waitForSelector('[data-testid=ticket-description] strong', { timeout: 5000 })
  assert(true, 'description in Markdown')
  assert(await page.isVisible('.pane.active .tab.active:has-text("#1")'), 'title of the tab')
  assert(fs.existsSync(WS + '/demo/.ide/kanban.db'), 'kanban.db base in .ide')
  assert(fs.readFileSync(WS + '/demo/.ide/.gitignore', 'utf8').includes('kanban.db'), 'kanban.db ignored by git')
  assert(await page.isVisible('.tk-stage.current[data-stage=new]'), 'the stage New is the current one')
  assert((await page.$$eval('.tk-stage', (l) => l.map((e) => e.dataset.stage))).join() === 'new,todo,in_progress,review', 'every stage shown')

  // Description limited to 1500 characters.
  await page.click('[data-testid=ticket-description-edit]')
  await page.fill('.tk-md-input', 'x'.repeat(1501))
  assert(await page.isDisabled('[data-testid=ticket-description-save]'), 'description over 1500 characters cannot be saved')
  assert((await page.textContent('[data-testid=ticket-description] .tk-count.over')).includes('1501/1500'), 'character counter')
  await page.click('[data-testid=ticket-description] .btn:has-text("Cancel")')

  // Plan and goals: the plan moves the ticket to To do.
  await page.click('[data-testid=ticket-plan-edit]')
  await page.fill('.tk-md-input', '1. Add the route\n2. Write the CSV')
  await page.click('[data-testid=ticket-plan-save]')
  await page.waitForSelector('[data-testid=ticket-plan] ol li')
  await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")')
  assert(await page.isVisible('.tk-stage.current[data-stage=todo]'), 'a plan moves the ticket to To do')
  for (const [g, d] of [['The /export route answers', 'curl /export'], ['The CSV has a header', '']]) {
    await page.fill('[data-testid=ticket-goal-input]', g)
    await page.fill('[data-testid=ticket-goal-description]', d)
    await page.keyboard.press('Enter')
    await page.waitForSelector(`[data-testid=ticket-goal]:has-text("${g}")`)
  }
  assert((await page.$$('[data-testid=ticket-goal]')).length === 2, 'two goals added')
  assert((await page.textContent('[data-testid=ticket-goal-desc]')) === 'curl /export', 'goal with a description')
  await page.click('[data-testid=ticket-goal] input[type=checkbox]')
  await page.waitForSelector('[data-testid=ticket-goal].done')
  assert(true, 'goal checked')

  // Note, linked file, attachment.
  await page.fill('[data-testid=ticket-note-input]', 'Mind the `;` separator')
  await page.click('[data-testid=ticket-note-add]')
  await page.waitForSelector('[data-testid=ticket-note]:has-text("separator")')
  assert(true, 'note added')

  // Claude Code writes through the MCP endpoint of the pod: the window follows, author Claude.
  const mcp = await fetch(process.env.E2E_URL + '/mcp', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + process.env.E2E_TOKEN },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'kanban_add_note', arguments: { cwd: WS + '/demo', id: 1, text: 'Checked by Claude' } } }),
  }).then((r) => r.json())
  assert(mcp.result && !mcp.result.isError, 'note through MCP')
  await page.waitForSelector('[data-testid=ticket-note]:has-text("Checked by Claude")')
  assert((await page.textContent('[data-testid=ticket-note]:has-text("Checked by Claude") .tk-note-head')).includes('Claude'), 'a note of Claude is shown as written by Claude')
  // A file link of Claude Code opens the file in the window of the project.
  const opened = await fetch(`${process.env.E2E_URL}/open?path=${encodeURIComponent(WS + '/demo/notes.txt')}&line=1`, {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + process.env.E2E_TOKEN },
  }).then((r) => r.json())
  assert(opened.opened, 'file link sent to the window')
  await page.waitForSelector('.pane .tab.active:has-text("notes.txt")')
  assert(true, 'file link opened in the editor')
  await page.click('.pane .tab:has-text("#1")')
  await page.waitForSelector('[data-testid=ticket-view]')
  // Claude Code menu: a terminal of the IDE runs claude with the prompt of the endpoint.
  await page.click('[data-testid=ticket-claude]')
  await page.click('.ctx-menu button:has-text("Redo the plan (Opus)")')
  await page.waitForFunction(() => document.querySelector('.xterm-rows')?.textContent.includes('fake claude: --model opus /mcp__web-ide__plan 1'))
  assert(true, 'Claude Code started on the ticket in a terminal')
  await page.click('[data-testid=ticket-file-add]')
  await page.waitForSelector('.pick-input')
  await page.keyboard.type('main.go')
  await page.waitForFunction(() => document.querySelector('.pick-item.selected')?.textContent.includes('main.go'))
  await page.keyboard.press('Enter')
  await page.waitForSelector('.tk-side .tk-row:has-text("src/main.go")')
  assert(true, 'file linked')
  await page.setInputFiles('[data-testid=ticket-attach]', { name: 'capture.txt', mimeType: 'text/plain', buffer: Buffer.from('bonjour') })
  await page.waitForSelector('.tk-side .tk-row:has-text("capture.txt")')
  assert(true, 'attachment added')

  // Two columns in a wide pane, one in a narrow one.
  const sideBelow = () =>
    page.evaluate(() => document.querySelector('.tk-side').getBoundingClientRect().top >= document.querySelector('.tk-main').getBoundingClientRect().bottom)
  assert(!(await sideBelow()), 'wide pane: side column beside the main one')
  await page.setViewportSize({ width: 800, height: 900 })
  await page.waitForFunction(() => document.querySelector('.tk-side').getBoundingClientRect().top >= document.querySelector('.tk-main').getBoundingClientRect().bottom)
  assert(await page.evaluate(() => document.querySelector('.tk-main').getBoundingClientRect().width > 300), 'narrow pane: one full-width column')
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.waitForFunction(() => document.querySelector('.tk-side').getBoundingClientRect().top < document.querySelector('.tk-main').getBoundingClientRect().bottom)

  // A second window follows the changes.
  const page2 = await ctx.newPage()
  await page2.goto(page.url())
  await page2.waitForSelector('[data-testid=ticket-view]')

  // Workflow.
  await page.click('[data-testid=ticket-start]')
  await page.waitForSelector('[data-testid=ticket-status]:has-text("In progress")')
  await page2.waitForSelector('[data-testid=ticket-status]:has-text("In progress")', { timeout: 5000 })
  assert(true, 'the other window follows the status change')
  await page.click('[data-testid=ticket-to-review]')
  await page.waitForSelector('[data-testid=ticket-status]:has-text("To test")')
  await page.click('[data-testid=ticket-feedback]')
  assert(await page.evaluate(() => document.activeElement?.dataset.testid === 'ticket-feedback-input'), 'Add feedback focuses the feedback box')
  await page.selectOption('[data-testid=ticket-feedback-kind]', 'bug')
  await page.fill('[data-testid=ticket-feedback-input]', 'The file is empty')
  await page.click('[data-testid=ticket-feedback-add]')
  await page.waitForSelector('[data-testid=ticket-feedback-item].k-bug:has-text("The file is empty")')
  assert((await page.textContent('[data-testid=ticket-status]')).includes('To test'), 'a feedback leaves the ticket in To test')
  assert(await page.isVisible('[data-testid=ticket-feedback-item] [data-testid=ticket-feedback-session]'), 'fix session offered for an open feedback')
  await page.click('[data-testid=ticket-feedback-item] input[type=checkbox]')
  await page.waitForSelector('[data-testid=ticket-feedback-item].done')
  assert(!(await page.isVisible('[data-testid=ticket-feedback-session]')), 'no fix session for a handled feedback')
  await page.click('[data-testid=ticket-close]')
  await page.waitForSelector('[data-testid=ticket-status]:has-text("Done")')
  assert(true, 'ticket closed')
  await page.click('.tk-section-toggle:has-text("History")')
  const events = await page.$$eval('.tk-events li', (l) => l.length)
  assert(events >= 5, `history of the changes (${events})`)

  // Board and panel.
  await page.click('.pane.active .tab:has-text("Kanban")')
  await page.waitForSelector('[data-testid=kanban-board]')
  assert(!(await page.isVisible('[data-testid=ticket-card-1]')), 'closed ticket hidden from the board')
  await page.click('.kb-toolbar button:has-text("Done and abandoned")')
  await page.waitForSelector('[data-testid=ticket-card-1]')
  assert(true, 'closed ticket in the done column')
  await page.screenshot({ path: OUT + '/kanban-board.png' })
  await page.click('[data-testid=kanban-settings]')
  await page.waitForFunction(() => document.querySelector('[data-testid=kanban-mcp-command]')?.value.includes('/mcp --header'))
  assert(true, 'the setup command of Claude Code is in the kanban settings')
  await page.click('.modal button:has-text("Cancel")')
  await page.click('.rail-left .rail-btn[title="Kanban"]')
  await page.click('.rail-left .rail-btn[title="Kanban"]')
  await page.click('[data-testid=kanban-panel] button[title="New ticket"]')
  await page.fill('[data-testid=kanban-title]', 'Second ticket')
  await page.click('[data-testid=kanban-create]')
  await page.waitForSelector('[data-testid=kanban-panel] [data-testid=ticket-card-2]')
  assert(true, 'new ticket from the panel')
  await page.waitForSelector('[data-testid=ticket-view] [data-testid=ticket-title]:has-text("Second ticket")')
  await page.screenshot({ path: OUT + '/kanban-ticket.png' })
  await page2.close()
})
