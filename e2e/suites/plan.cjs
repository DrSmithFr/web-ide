// AI assistant, Plan / Build modes: Shift+Tab, Plan prompt and tools (no file change,
// exit_plan_mode), dedicated Plan model, bash read-only guess (reading runs, a change asks),
// plan card and its execution in Build, compaction asked by the model.
const fs = require('fs')
const http = require('http')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

const requests = []
const summaries = []
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
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: ['fake-model', 'fake-plan', 'fake-small'].map((id) => ({ id, status: { value: 'loaded' } })) }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  if (text(r.messages[0]).startsWith('Tu résumes')) {
    summaries.push(r)
    sse(res, { content: 'RÉSUMÉ du travail.' })
    return end(res)
  }
  requests.push(r)
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user' && !text(m).startsWith('Résumé'))
  const ask = text(lastUser)
  const tools = msgs.filter((m) => m.role === 'tool')
  if (ask.startsWith('Planifie')) {
    if (last.role === 'user')
      return (
        sse(res, {
          tool_calls: calls([
            ['p1', 'bash', { command: 'git status; ls src' }],
            ['p2', 'bash', { command: 'rm -rf src' }],
            ['p3', 'edit_file', { path: 'src/main.go', old_string: 'Bonjour', new_string: 'Salut' }],
          ]),
        }),
        end(res, 'tool_calls')
      )
    return sse(res, { tool_calls: calls([['p4', 'exit_plan_mode', { plan: '## Plan\n1. Remplacer Bonjour par Salut dans `src/main.go`\n2. Lancer les tests' }]]) }), end(res, 'tool_calls')
  }
  if (ask.startsWith('Le plan est accepté')) {
    if (last.role === 'user') return sse(res, { tool_calls: calls([['e1', 'edit_file', { path: 'src/main.go', old_string: '"Bonjour %s"', new_string: '"Salut %s"' }]]) }), end(res, 'tool_calls')
    if (last.role === 'tool' && last.tool_call_id === 'e1') return sse(res, { tool_calls: calls([['c1', 'compact_conversation', { instructions: 'garder le plan' }]]) }), end(res, 'tool_calls')
    sse(res, { content: 'Plan exécuté.' })
    return end(res)
  }
  sse(res, { content: `Réponse (${r.model}).` })
  end(res)
})

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
    // Dedicated Plan model.
    await page.click('.ai-tab:has-text("Mode Plan")')
    await page.selectOption('[data-testid=plan-settings] select[name=planServer]', { index: 1 })
    await page.waitForSelector('[data-testid=plan-settings] select[name=planModel] option[value="fake-plan"]', { state: 'attached' })
    await page.selectOption('[data-testid=plan-settings] select[name=planModel]', 'fake-plan')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    // Shift+Tab: Plan mode.
    await page.click('.ai-composer textarea')
    await page.keyboard.press('Shift+Tab')
    await page.waitForSelector('[data-testid=ai-mode].plan:has-text("fake-plan")')
    assert(true, 'Maj+Tab passe en mode Plan (modèle dédié affiché)')

    await page.fill('.ai-composer textarea', 'Planifie le renommage')
    await page.keyboard.press('Enter')
    // The command that changes something waits for the user.
    await page.waitForSelector('[data-testid=ai-approval]:has-text("rm -rf src")', { timeout: 10000 })
    const r0 = requests[0]
    assert(r0.model === 'fake-plan', 'requête envoyée au modèle du mode Plan')
    assert(r0.messages[0].content.includes('mode Plan') && r0.messages[0].content.includes('exit_plan_mode'), 'prompt système du mode Plan')
    const names = toolNames(r0)
    assert(!names.includes('edit_file') && !names.includes('write_file') && names.includes('exit_plan_mode') && names.includes('compact_conversation'), 'outils du mode Plan : sans écriture, avec exit_plan_mode : ' + names.join(','))
    await page.click('[data-testid=ai-approval] button:has-text("Refuser")')
    await page.waitForSelector('[data-testid=ai-plan]', { timeout: 10000 })
    const res1 = requests[1].messages.filter((m) => m.role === 'tool')
    const byId = (id) => text(res1.find((m) => m.tool_call_id === id) ?? { content: '' })
    assert(byId('p1').startsWith('Code de sortie 0'), 'commande de lecture exécutée sans demander')
    assert(byId('p2').includes('refusé') && fs.existsSync(WS + '/demo/src/main.go'), 'commande de modification refusée, rien supprimé')
    assert(byId('p3').includes('mode Plan') && fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('Bonjour'), 'edit_file refusé en mode Plan')
    const card = await page.waitForSelector('[data-testid=ai-plan] .md:has-text("Remplacer Bonjour par Salut")', { timeout: 5000 }).then(() => true, () => false)
    assert(card, 'carte du plan affichée')
    await page.waitForSelector('[data-testid=send]')
    assert(requests.length === 2, 'le tour s’arrête après la présentation du plan')
    assert(await page.isVisible('.ai-plan-badge'), 'réponse marquée Plan')
    await page.screenshot({ path: OUT + '/plan-card.png' })

    // Execute the plan: Build mode, model of the conversation, edit then compaction by the model.
    await page.click('[data-testid=ai-plan] button:has-text("Exécuter ce plan")')
    await page.waitForSelector('[data-testid=ai-approval]:has-text("src/main.go")', { timeout: 10000 })
    await page.click('[data-testid=ai-approval] button:has-text("Appliquer")')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Plan exécuté.")', { timeout: 15000 })
    const r2 = requests[2]
    assert(r2.model === 'fake-model' && toolNames(r2).includes('edit_file') && !toolNames(r2).includes('exit_plan_mode'), 'exécution en mode Build avec le modèle de la conversation')
    assert(!(await page.isVisible('[data-testid=ai-mode].plan')), 'le mode repasse en Build')
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('"Salut %s"'), 'le plan est exécuté (fichier modifié)')
    assert(summaries.length === 1 && summaries[0].messages[0].content.includes('garder le plan'), 'compaction demandée par le modèle, avec ses consignes')
    assert(await page.isVisible('[data-testid=ai-summary]'), 'résumé affiché dans la conversation')
    await page.click('.ai-compacted-toggle')
    assert((await page.textContent('[data-testid=ai-plan]')).includes('exécuté'), 'plan marqué exécuté (dans les messages compactés)')
    await page.screenshot({ path: OUT + '/plan-done.png' })
  } finally {
    fake.close()
  }
})
