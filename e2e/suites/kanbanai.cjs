// Kanban and the assistant: ask_user (questions one at a time, recap, answers sent back),
// kanban_create / kanban_list from any conversation, writing tools only when linked.
const http = require('http')
const { run, openProject, assert, OUT } = require('../common.cjs')

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
  if (last.role === 'user')
    return (
      sse(res, {
        content: 'Quelques questions.',
        tool_calls: calls([
          [
            'q1',
            'ask_user',
            {
              questions: [
                { question: 'Quel format ?', header: 'Format', options: [{ label: 'CSV (recommandé)', description: 'simple' }, { label: 'JSON' }] },
                { question: 'Quelles colonnes ?', options: [{ label: 'id' }, { label: 'total' }, { label: 'date' }], multiple: true },
                { question: 'Priorité ?', options: [{ label: 'Haute' }, { label: 'Normale' }] },
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
          ['k1', 'kanban_create', { title: 'Export JSON', description: 'Colonnes : id, date', type: 'feature', priority: 'high', files: ['src/main.go'] }],
          ['k2', 'kanban_list', {}],
          ['k3', 'kanban_set_plan', { plan: 'x', goals: ['y'] }],
        ]),
      }),
      end(res, 'tool_calls')
    )
  sse(res, { content: 'Ticket créé.' })
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
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    await page.fill('.ai-composer textarea', 'Note un ticket pour l’export')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-question]', { timeout: 10000 })
    const names = toolNames(requests[0])
    assert(['kanban_list', 'kanban_get', 'kanban_create', 'ask_user'].every((n) => names.includes(n)), 'outils kanban de lecture et ask_user proposés')
    assert(!names.includes('kanban_set_plan') && !names.includes('kanban_move'), 'pas d’outil de modification sans ticket lié')
    assert(requests[0].messages[0].content.includes('ask_user'), 'le prompt présente ask_user')
    await page.waitForSelector('[data-testid=send]')
    assert(requests.length === 1, 'le tour s’arrête sur les questions')

    // Question 1: a single choice moves on.
    assert((await page.textContent('[data-testid=ai-ask-question]')).includes('Quel format'), 'première question affichée')
    await page.click('.ai-ask-option:has-text("JSON")')
    await page.waitForSelector('[data-testid=ai-ask-question]:has-text("Quelles colonnes")')
    assert(true, 'choix unique : question suivante')
    // Question 2: several choices.
    await page.click('.ai-ask-option:has-text("id")')
    await page.click('.ai-ask-option:has-text("date")')
    await page.click('[data-testid=ai-ask-next]')
    // Question 3: free answer.
    await page.fill('[data-testid=ai-ask-free]', 'Très haute')
    await page.click('[data-testid=ai-ask-next]')
    await page.waitForSelector('.ai-ask-recap')
    const recap = await page.textContent('.ai-ask-recap')
    assert(recap.includes('JSON') && recap.includes('id ; date') && recap.includes('Très haute'), 'récapitulatif : ' + recap)
    await page.screenshot({ path: OUT + '/ask-recap.png' })
    await page.click('[data-testid=ai-ask-send]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Ticket créé.")', { timeout: 15000 })
    const answer = text(requests[1].messages.find((m) => m.role === 'tool' && m.tool_call_id === 'q1'))
    assert(answer.includes('Quel format ?') && answer.includes('→ JSON') && answer.includes('id ; date') && answer.includes('Très haute'), 'réponses renvoyées au modèle : ' + answer)
    assert(await page.isVisible('[data-testid=ai-ask] .badge:has-text("répondu")'), 'carte marquée répondue')

    const res = requests[2].messages.filter((m) => m.role === 'tool')
    const by = (id) => text(res.find((m) => m.tool_call_id === id))
    assert(by('k1').includes('Ticket #1 créé'), 'kanban_create : ' + by('k1'))
    assert(by('k2').includes('#1 [Nouveau]') && by('k2').includes('Export JSON'), 'kanban_list : ' + by('k2'))
    assert(by('k3').includes('aucun ticket'), 'kanban_set_plan refusé sans ticket lié')

    await page.click('.rail-left .rail-btn[title="Kanban"]')
    await page.waitForSelector('[data-testid=kanban-panel] [data-testid=ticket-card-1]:has-text("Export JSON")')
    await page.click('[data-testid=ticket-card-1]')
    await page.waitForSelector('[data-testid=ticket-view]')
    assert((await page.textContent('.tk-side')).includes('src/main.go'), 'fichier lié par le modèle')
    await page.click('.tk-section-toggle:has-text("Historique")')
    assert(await page.isVisible('.tk-events .badge:has-text("assistant")'), "historique : créé par l'assistant")
  } finally {
    fake.close()
  }
})
