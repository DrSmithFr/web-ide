// AI assistant, agent features: system prompt built from the editable template, the
// instruction files (global and project) and the skills; IDE tools (skill, open_file,
// focus, run_command); model switch; manual and automatic compaction with another model;
// conversations in .ide/chats.db. The model is a scripted fake OpenAI server.
const fs = require('fs')
const http = require('http')
const { execFileSync } = require('child_process')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

const demo = WS + '/demo'
fs.writeFileSync(demo + '/CLAUDE.md', 'Règle du projet : tests avec make test. Voir @docs/regles.md\n')
fs.mkdirSync(demo + '/docs', { recursive: true })
fs.writeFileSync(demo + '/docs/regles.md', 'Règle importée : indentation par tabulations.\n')
fs.mkdirSync(demo + '/.claude/skills/deploy', { recursive: true })
fs.writeFileSync(demo + '/.claude/skills/deploy/SKILL.md', '---\nname: deploy\ndescription: Déployer le projet en production\n---\nLancer make deploy.\n')

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
  if (text(r.messages[0]).startsWith('Tu résumes')) {
    summaries.push(r)
    sse(res, { content: `RÉSUMÉ-${summaries.length} : l’utilisateur teste l’agent.` })
    return end(res, 'stop')
  }
  requests.push(r)
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user' && !text(m).startsWith('Résumé de la conversation'))
  const ask = text(lastUser ?? { content: '' })
  const toolsDone = msgs.filter((m) => m.role === 'tool').length
  if (ask.includes('outils')) {
    if (last.role === 'user')
      return sse(res, { tool_calls: calls([['s1', 'load_skill', { name: 'greet' }], ['s2', 'open_file', { path: 'src/main.go', line: 8, end_line: 10 }]]) }), end(res, 'tool_calls')
    if (toolsDone === 2)
      return (
        sse(res, { tool_calls: calls([['s3', 'run_command', { command: 'echo bonjour-console && exit 3' }], ['s4', 'focus', { target: 'panel', panel: 'git' }]]) }),
        end(res, 'tool_calls')
      )
    sse(res, { content: 'Outils essayés.' })
    return end(res, 'stop', { prompt_tokens: 900, completion_tokens: 10 })
  }
  if (ask.includes('longue')) {
    // Big usage: the next step must be preceded by an automatic compaction.
    if (last.role === 'user') return sse(res, { tool_calls: calls([['l1', 'list_dir', { path: '.' }]]) }), end(res, 'tool_calls', { prompt_tokens: 7000, completion_tokens: 20 })
    sse(res, { content: 'Après compaction.' })
    return end(res, 'stop', { prompt_tokens: 1200, completion_tokens: 5 })
  }
  sse(res, { content: `Réponse de ${r.model}.` })
  end(res, 'stop', { prompt_tokens: 500, completion_tokens: 5 })
})

async function ask(page, message, expect) {
  await page.fill('.ai-composer textarea', message)
  await page.keyboard.press('Enter')
  await page.waitForSelector(`.ai-msg.assistant .md:has-text("${expect}")`, { timeout: 20000 })
  await page.waitForSelector('.ai-composer .btn:has-text("Envoyer")', { timeout: 10000 })
}

run(async ({ page }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  page.on('dialog', (d) => d.accept())
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="Assistant IA"]')
    await page.click('.ai-empty button:has-text("Ajouter un serveur")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Ajouter")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')

    // Instructions and skills listed in the settings.
    await page.click('.ai-tab:has-text("Prompt")')
    await page.waitForSelector('[data-testid=instruction-file]')
    const files = await page.$$eval('[data-testid=instruction-file]', (e) => e.map((x) => x.textContent))
    assert(files.length === 3 && files[0].includes('global') && files[1].includes('CLAUDE.md') && files[2].includes('docs/regles.md'), 'fichiers d’instructions listés : ' + JSON.stringify(files))
    const skills = await page.$$eval('[data-testid=skill]', (e) => e.map((x) => x.textContent))
    assert(skills.length === 2 && skills.some((s) => s.includes('greet')) && skills.some((s) => s.includes('deploy') && s.includes('projet')), 'skills listés : ' + JSON.stringify(skills))
    await page.click('.ai-servers .modal-head button')
    await page.waitForFunction(() => document.querySelector('.ai-modelbar select.grow')?.value === 'fake-model')

    // First message: system prompt with template, instructions and skills.
    await ask(page, 'Bonjour', 'Réponse de fake-model.')
    const sys = requests[0].messages[0].content
    assert(sys.includes('Projet ouvert : « demo »') && sys.includes('read_file'), 'prompt par défaut avec les variables remplacées')
    assert(sys.includes('Instruction globale : réponds poliment.') && sys.includes('Règle du projet') && sys.includes('Règle importée'), 'CLAUDE.md global, du projet et import @ dans le prompt')
    assert(sys.includes('- greet : Saluer') && sys.includes('- deploy : Déployer') && !sys.includes('Bien le bonjour'), 'skills listés sans leur contenu')
    assert(await page.isVisible('.ai-usage:has-text("fake-model")'), 'modèle affiché sous la réponse')
    assert(await page.isVisible('[data-testid=ai-gauge]:has-text("/ 8.2k")'), 'jauge de contexte affichée')

    // Project prompt edited in the settings.
    await page.click('.ai-panel button[title^="Réglages"]')
    await page.click('.ai-tab:has-text("Prompt")')
    await page.fill('[data-testid=prompt-settings] textarea', 'Tu es le robot du projet {{project}}.\n{{tools}}')
    await page.click('[data-testid=prompt-settings] button:has-text("Enregistrer")')
    await page.waitForSelector('[data-testid=prompt-settings] strong:has-text("prompt du projet")')
    assert(fs.readFileSync(demo + '/.ide/system-prompt.md', 'utf8').includes('robot du projet'), 'prompt du projet enregistré dans .ide/system-prompt.md')
    await page.click('.ai-servers .modal-head button')

    // Model switched in the middle of the conversation.
    await page.selectOption('.ai-modelbar select.grow', 'fake-other')
    await ask(page, 'Et toi ?', 'Réponse de fake-other.')
    const r2 = requests[requests.length - 1]
    assert(r2.model === 'fake-other' && r2.messages.length === 4, 'la conversation continue avec l’autre modèle')
    assert(r2.messages[0].content.startsWith('Tu es le robot du projet demo.') && r2.messages[0].content.includes('Règle du projet'), 'nouveau prompt utilisé, instructions toujours ajoutées')

    // IDE tools.
    await ask(page, 'Essaie les outils', 'Outils essayés.')
    const tools = await page.$$eval('.ai-tool', (e) => e.map((x) => ({ cls: x.className, text: x.textContent })))
    assert(tools.length === 4, 'quatre appels d’outils : ' + JSON.stringify(tools.map((t) => t.text)))
    const results = requests.flatMap((r) => r.messages.filter((m) => m.role === 'tool'))
    const byId = (id) => results.find((m) => m.tool_call_id === id)?.content ?? ''
    assert(byId('s1').includes('Bien le bonjour'), 'load_skill renvoie le contenu du skill')
    assert(byId('s3').includes('Code de sortie : 3') && byId('s3').includes('bonjour-console'), 'run_command renvoie la sortie et le code : ' + JSON.stringify(byId('s3').slice(0, 80)))
    assert(tools[2].cls.includes('error'), 'commande en échec signalée')
    const pos = await page.textContent('.cursor-info')
    assert((await page.textContent('.pane.active .tab.active')).includes('main.go') && /^10:2 \(\d+ car\.\)/.test(pos), 'open_file ouvre main.go et sélectionne les lignes 8 à 10 : ' + pos)
    assert(await page.isVisible('.git-panel'), 'focus affiche le panneau Git')
    assert(await page.isVisible('.bottom .btab.active:has-text("echo bonjour-console")'), 'console de la commande au premier plan')
    await page.screenshot({ path: OUT + '/agent-tools.png' })

    // Manual compaction by a different model.
    await page.click('.ai-panel button[title^="Réglages"]')
    await page.click('.ai-tab:has-text("Compaction")')
    await page.selectOption('[data-testid=compaction-settings] select[name=compactServer]', { index: 1 })
    await page.waitForSelector('[data-testid=compaction-settings] select[name=compactModel] option[value="fake-small"]', { state: 'attached' })
    await page.selectOption('[data-testid=compaction-settings] select[name=compactModel]', 'fake-small')
    await page.click('.ai-servers .modal-head button')
    await page.click('.ai-composer button:has-text("compacter")')
    await page.waitForSelector('[data-testid=ai-summary]:has-text("fake-small")', { timeout: 10000 })
    assert(summaries.length === 1 && summaries[0].model === 'fake-small' && text(summaries[0].messages[1]).includes('Et toi ?') && !text(summaries[0].messages[1]).includes('Essaie les outils'), 'résumé demandé au modèle de compaction (tout sauf le dernier échange)')
    assert(await page.isVisible('.ai-compacted-toggle'), 'messages compactés repliés')
    await ask(page, 'Encore', 'Réponse de fake-other.')
    const r3 = requests[requests.length - 1]
    assert(text(r3.messages[1]).includes('RÉSUMÉ-1') && text(r3.messages[2]).includes('Essaie les outils') && !r3.messages.some((m) => text(m).includes('Et toi')), 'après compaction : résumé + dernier échange seulement')

    // Automatic compaction when the usage passes 75 % of the context.
    await ask(page, 'Une tâche longue', 'Après compaction.')
    assert(summaries.length === 2, 'compaction automatique déclenchée au-delà du seuil')
    const r4 = requests[requests.length - 1]
    assert(text(r4.messages[1]).includes('RÉSUMÉ-2'), 'la tâche continue après la compaction automatique')
    await page.screenshot({ path: OUT + '/agent-compaction.png' })

    // Conversations in the project SQLite base, ignored by git.
    await page.waitForTimeout(500)
    const db = demo + '/.ide/chats.db'
    assert(fs.existsSync(db), 'base .ide/chats.db créée')
    assert(fs.readFileSync(demo + '/.ide/.gitignore', 'utf8').includes('chats.db'), '.ide/.gitignore ignore la base')
    const count = execFileSync('python3', ['-c', `import sqlite3;c=sqlite3.connect('${db}');print(c.execute('select count(*) from chats').fetchone()[0], c.execute('select count(*) from messages').fetchone()[0])`], { encoding: 'utf8' }).trim().split(' ').map(Number)
    assert(count[0] === 1 && count[1] > 10, 'conversation et messages dans SQLite : ' + count.join(' '))
    await page.reload()
    await page.waitForSelector('.menubar')
    await page.click('.ai-panel button[title="Conversations du projet"]').catch(async () => {
      await page.click('.rail-right .rail-btn[title="Assistant IA"]')
      await page.click('.ai-panel button[title="Conversations du projet"]')
    })
    await page.click('.ai-history-open:has-text("Bonjour")')
    await page.waitForSelector('.ai-summary')
    const visible = (await page.$$('[data-testid=ai-summary]')).length
    await page.click('.ai-compacted-toggle')
    const all = (await page.$$('[data-testid=ai-summary]')).length
    assert(visible === 1 && all === 2, `conversation relue depuis SQLite : le premier résumé est replié avec les messages compactés (${visible}/${all})`)
  } finally {
    fake.close()
  }
})
