// AI assistant, agent features: system prompt built from the editable template, the
// instruction files (global and project) and the skills; IDE tools (skill, open_file,
// focus, run_command); model switch; manual and automatic compaction with another model;
// conversations in .ide/chats.db. The model is a scripted fake OpenAI server.
const fs = require('fs')
const http = require('http')
const { execFileSync } = require('child_process')
const { run, openProject, assert, WS, OUT, composerText } = require('../common.cjs')

const demo = WS + '/demo'
const data = WS + '/../data'
// Global instructions and skill of the IDE data folder (~/.web-ide).
fs.writeFileSync(data + '/AGENTS.md', 'Instruction of the IDE: commit messages in English.\n')
fs.mkdirSync(data + '/skills/release', { recursive: true })
fs.writeFileSync(data + '/skills/release/SKILL.md', '---\nname: release\ndescription: Publish a release\n---\nTag, then push.\n')
fs.writeFileSync(demo + '/CLAUDE.md', 'Project rule: tests with make test. See @docs/rules.md\n')
fs.mkdirSync(demo + '/docs', { recursive: true })
fs.writeFileSync(demo + '/docs/rules.md', 'Imported rule: indent with tabs.\n')
fs.mkdirSync(demo + '/.claude/skills/deploy', { recursive: true })
fs.writeFileSync(demo + '/.claude/skills/deploy/SKILL.md', '---\nname: deploy\ndescription: Deploy the project to production\n---\nRun make deploy.\n')

const requests = []
const summaries = []

const sse = (res, delta, extra = {}) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }], ...extra })}\n\n`)
function end(res, reason, usage) {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  if (usage) res.write(`data: ${JSON.stringify({ choices: [], usage })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const calls = (list) => list.map(([id, name, args], index) => ({ index, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }))
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models')
    return res.end(JSON.stringify({ data: ['fake-model', 'fake-other', 'fake-small'].map((id) => ({ id, status: { value: 'loaded' } })) }))
  if (req.url === '/props') return res.end(JSON.stringify({ role: 'router' }))
  if (req.url.startsWith('/props?')) return res.end(JSON.stringify({ chat_template_caps: { supports_tools: true }, default_generation_settings: { n_ctx: 8192 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  if (text(r.messages[0]).startsWith('You summarize')) {
    summaries.push(r)
    sse(res, { content: `SUMMARY-${summaries.length}: the user tests the agent.` })
    return end(res, 'stop')
  }
  requests.push(r)
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user' && !text(m).startsWith('Summary of the earlier conversation'))
  const ask = text(lastUser ?? { content: '' })
  const toolsDone = msgs.filter((m) => m.role === 'tool').length
  if (ask.includes('tools')) {
    if (last.role === 'user')
      return sse(res, { tool_calls: calls([['s1', 'load_skill', { name: 'greet' }], ['s2', 'open_file', { path: 'src/main.go', line: 8, end_line: 10 }]]) }), end(res, 'tool_calls')
    if (toolsDone === 2 && last.role === 'tool')
      return (
        sse(res, {
          tool_calls: calls([
            ['s3', 'run_command', { command: 'echo hello-console && exit 3' }],
            ['s4', 'focus', { target: 'panel', panel: 'git' }],
            ['s5', 'bash', { command: 'echo from-bash; ls src; exit 2' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Tools tried.' })
    return end(res, 'stop', { prompt_tokens: 900, completion_tokens: 10 })
  }
  if (ask.includes('long')) {
    // Big usage: the next step must be preceded by an automatic compaction.
    if (last.role === 'user') return sse(res, { tool_calls: calls([['l1', 'list_dir', { path: '.' }]]) }), end(res, 'tool_calls', { prompt_tokens: 7000, completion_tokens: 20 })
    sse(res, { content: 'After compaction.' })
    return end(res, 'stop', { prompt_tokens: 1200, completion_tokens: 5 })
  }
  sse(res, { content: `Answer of ${r.model}.` })
  end(res, 'stop', { prompt_tokens: 500, completion_tokens: 5 })
})

async function ask(page, message, expect) {
  await page.fill('.ai-composer .ed-content', message)
  await page.keyboard.press('Control+Enter')
  await page.waitForSelector(`.ai-msg.assistant .md:has-text("${expect}")`, { timeout: 20000 })
  await page.waitForSelector('[data-testid=send]', { timeout: 10000 })
}

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

    // Instructions and skills listed in the settings.
    await page.click('.ai-tab:has-text("Prompt")')
    await page.waitForSelector('[data-testid=instruction-file]')
    const files = await page.$$eval('[data-testid=instruction-file]', (e) => e.map((x) => x.textContent))
    assert(files.length === 4 && files[0].includes('global') && files[1].includes('data/AGENTS.md') && files[2].includes('CLAUDE.md') && files[3].includes('docs/rules.md'), 'instruction files listed (with ~/.web-ide/AGENTS.md): ' + JSON.stringify(files))
    const skills = await page.$$eval('[data-testid=skill]', (e) => e.map((x) => x.textContent))
    assert(skills.length === 3 && skills.some((s) => s.includes('greet')) && skills.some((s) => s.includes('release')) && skills.some((s) => s.includes('deploy') && s.includes('project')), 'skills listed (with ~/.web-ide/skills): ' + JSON.stringify(skills))
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    // First message: system prompt with template, instructions and skills.
    await ask(page, 'Hello', 'Answer of fake-model.')
    const sys = requests[0].messages[0].content
    assert(sys.includes('Open project: "demo"') && sys.includes('read_file'), 'default prompt with the variables replaced')
    assert(sys.includes('Global instruction: answer politely.') && sys.includes('Instruction of the IDE') && sys.includes('Project rule') && sys.includes('Imported rule'), 'global CLAUDE.md, AGENTS.md of ~/.web-ide, project file and @ import in the prompt')
    assert(sys.includes('- greet: Greet') && sys.includes('- deploy: Deploy') && !sys.includes('Good day to you'), 'skills listed without their content')
    assert(await page.isVisible('.ai-usage:has-text("fake-model")'), 'model shown under the answer')
    const gauge = (await page.getAttribute('[data-testid=ai-gauge]', 'title')) ?? ''
    assert(gauge.replace(/[^\d/]/g, '').includes('/8192'), 'context gauge shown: ' + gauge)

    // Project prompt edited in the settings.
    await page.click('.ai-panel button[title^="Settings"]')
    await page.click('.ai-tab:has-text("Prompt")')
    await page.fill('[data-testid=prompt-settings] textarea', 'You are the robot of the project {{project}}.\n{{tools}}')
    await page.click('[data-testid=prompt-settings] button:has-text("Save")')
    await page.waitForSelector('[data-testid=prompt-settings] strong:has-text("project prompt")')
    assert(fs.readFileSync(demo + '/.ide/system-prompt.md', 'utf8').includes('robot of the project'), 'project prompt saved in .ide/system-prompt.md')
    await page.click('.ai-servers .modal-head button')

    // Model switched in the middle of the conversation.
    await page.click('[data-testid=model-pill]')
    await page.click('.ai-model-item:has-text("fake-other")')
    await ask(page, 'And you?', 'Answer of fake-other.')
    const r2 = requests[requests.length - 1]
    assert(r2.model === 'fake-other' && r2.messages.length === 4, 'the conversation goes on with the other model')
    assert(r2.messages[0].content.startsWith('You are the robot of the project demo.') && r2.messages[0].content.includes('Project rule'), 'new prompt used, instructions still added')

    // IDE tools.
    await ask(page, 'Try the tools', 'Tools tried.')
    const tools = await page.$$eval('.ai-tool', (e) => e.map((x) => ({ cls: x.className, text: x.textContent })))
    assert(tools.length === 5, 'five tool calls: ' + JSON.stringify(tools.map((t) => t.text)))
    const results = requests.flatMap((r) => r.messages.filter((m) => m.role === 'tool'))
    const byId = (id) => results.find((m) => m.tool_call_id === id)?.content ?? ''
    assert(byId('s1').includes('Good day to you'), 'load_skill returns the content of the skill')
    assert(byId('s3').includes('Exit code: 3') && byId('s3').includes('hello-console'), 'run_command returns the output and the code: ' + JSON.stringify(byId('s3').slice(0, 80)))
    assert(tools[2].cls.includes('error'), 'failed command reported')
    assert(byId('s5').startsWith('Exit code 2') && byId('s5').includes('from-bash') && byId('s5').includes('main.go'), 'bash returns the output and the code: ' + JSON.stringify(byId('s5').slice(0, 60)))
    assert(!(await page.isVisible('.bottom .console-tabs .tab:has-text("from-bash")')), 'bash does not open a console')
    const pos = await page.textContent('[data-testid=status-cursor]')
    assert((await page.textContent('.pane.active .tab.active')).includes('main.go') && /^10:2 \(\d+ chars\)/.test(pos), 'open_file opens main.go and selects the lines 8 to 10: ' + pos)
    assert(await page.isVisible('.git-panel'), 'focus shows the Git panel')
    assert(await page.isVisible('.bottom .console-tabs .tab.active:has-text("echo hello-console")'), 'console of the command in front')
    await page.screenshot({ path: OUT + '/agent-tools.png' })

    // Manual compaction by a different model.
    await page.click('.ai-panel button[title^="Settings"]')
    await page.click('.ai-tab:has-text("Compaction")')
    await page.selectOption('[data-testid=compaction-settings] select[name=compactServer]', { index: 1 })
    await page.waitForSelector('[data-testid=compaction-settings] select[name=compactModel] option[value="fake-small"]', { state: 'attached' })
    await page.selectOption('[data-testid=compaction-settings] select[name=compactModel]', 'fake-small')
    await page.click('.ai-servers .modal-head button')
    await page.click('[data-testid=ai-gauge]')
    await page.waitForSelector('[data-testid=ai-context-menu]')
    assert((await page.textContent('[data-testid=ai-context-menu]')).includes('Messages sent to the model'), 'context menu open')
    await page.click('[data-testid=ai-context-menu] button:has-text("Compact now")')
    await page.waitForSelector('[data-testid=ai-summary]:has-text("fake-small")', { timeout: 10000 })
    assert(summaries.length === 1 && summaries[0].model === 'fake-small' && text(summaries[0].messages[1]).includes('And you?') && !text(summaries[0].messages[1]).includes('Try the tools'), 'summary asked to the compaction model (all but the last exchange)')
    assert(await page.isVisible('.ai-compacted-toggle'), 'compacted messages folded')
    await ask(page, 'Again', 'Answer of fake-other.')
    const r3 = requests[requests.length - 1]
    assert(text(r3.messages[1]).includes('SUMMARY-1') && text(r3.messages[2]).includes('Try the tools') && !r3.messages.some((m) => text(m).includes('And you')), 'after compaction: summary + last exchange only')

    // Automatic compaction when the usage passes 75 % of the context.
    await ask(page, 'A long task', 'After compaction.')
    assert(summaries.length === 2, 'automatic compaction past the threshold')
    const r4 = requests[requests.length - 1]
    assert(text(r4.messages[1]).includes('SUMMARY-2'), 'the task goes on after the automatic compaction')
    await page.screenshot({ path: OUT + '/agent-compaction.png' })

    // Commands: completion, /help, /model, a skill as a command, /compact with instructions.
    await page.click('.ai-composer .ed-content')
    await page.keyboard.type('/he')
    await page.waitForSelector('[data-testid=ai-complete] .ai-complete-item.active:has-text("/help")')
    await page.keyboard.press('Enter')
    assert((await composerText(page)) === '/help ', 'command completion')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=ai-help]')
    const helpText = await page.textContent('[data-testid=ai-help]')
    assert(helpText.includes('/compact') && helpText.includes('/greet'), '/help lists the commands and the skills')
    await page.fill('.ai-composer .ed-content', '/model fake-small')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-small")')
    assert(true, '/model changes the model')
    await page.fill('.ai-composer .ed-content', '/model fake-other')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-other")')
    await ask(page, '/greet Marie', 'Answer of fake-other.')
    const skillAsk = requests[requests.length - 1].messages.at(-1)
    assert(text(skillAsk).includes('load_skill') && text(skillAsk).includes('"greet"') && text(skillAsk).includes('Marie'), 'skill as a command: ' + text(skillAsk).slice(0, 80))
    assert(await page.isVisible('.ai-msg.user .ai-mention:has-text("/greet")'), 'the bubble shows the command')

    // @ mention with completion: only the path is sent.
    await page.click('.ai-composer .ed-content')
    await page.keyboard.type('Look at @mai')
    await page.waitForSelector('[data-testid=ai-complete] .ai-complete-item.active:has-text("src/main.go")')
    await page.keyboard.press('Tab')
    assert((await composerText(page)) === 'Look at @src/main.go ', 'path completion')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.user .ai-mention:has-text("@src/main.go")')
    await page.waitForSelector('[data-testid=send]')
    assert(text(requests[requests.length - 1].messages.at(-1)) === 'Look at @src/main.go', 'the message keeps the path only')

    await page.fill('.ai-composer .ed-content', '/compact keep the file names')
    await page.keyboard.press('Control+Enter')
    for (let t = 0; t < 100 && summaries.length < 3; t++) await page.waitForTimeout(100)
    await page.waitForSelector('[data-testid=send]')
    assert(summaries.length === 3 && summaries[2].messages[0].content.includes('keep the file names'), '/compact with instructions')

    // Conversations in the project SQLite base, ignored by git.
    await page.waitForTimeout(500)
    const db = demo + '/.ide/chats.db'
    assert(fs.existsSync(db), '.ide/chats.db base created')
    assert(fs.readFileSync(demo + '/.ide/.gitignore', 'utf8').includes('chats.db'), '.ide/.gitignore ignores the base')
    const count = execFileSync('python3', ['-c', `import sqlite3;c=sqlite3.connect('${db}');print(c.execute('select count(*) from chats').fetchone()[0], c.execute('select count(*) from messages').fetchone()[0])`], { encoding: 'utf8' }).trim().split(' ').map(Number)
    assert(count[0] === 1 && count[1] > 10, 'conversation and messages in SQLite: ' + count.join(' '))
    // Reload: the active conversation comes back by itself.
    await page.reload()
    await page.waitForSelector('.menubar')
    await page.waitForSelector('.ai-summary', { timeout: 10000 })
    const visible = (await page.$$('[data-testid=ai-summary]')).length
    await page.click('.ai-compacted-toggle')
    const all = (await page.$$('[data-testid=ai-summary]')).length
    assert(visible === 1 && all === 3, `active conversation reloaded from SQLite, older summaries folded (${visible}/${all})`)

    // Detached window, wide: the history is always shown.
    const projectPath = new URL(page.url()).pathname
    await page.goto(new URL(projectPath + '/tool/assistant', page.url()).href)
    await page.waitForSelector('.ai-panel.detached')
    await page.waitForSelector('[data-testid=ai-sidebar]', { timeout: 5000 }).catch(() => {})
    assert((await page.isVisible('[data-testid=ai-sidebar]')) && !(await page.isVisible('.ai-side-wrap.overlay')) && !(await page.isVisible('.ai-panel button[title="Conversations of the project"]')), 'wide detached window: history always shown')
    assert(await page.isVisible('.ai-chat-item.active:has-text("Hello")'), 'the active conversation is selected in the history')
    await page.screenshot({ path: OUT + '/agent-detached.png' })
    await page.fill('.ai-composer .ed-content', '/clear')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-empty')
    assert(true, '/clear opens a new conversation')
  } finally {
    fake.close()
  }
})
