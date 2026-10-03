// AI assistant, conversation features: side bar order, Ctrl+click on @file, queue of
// messages (after an answer and between tool steps), message edited in place (attachments
// kept), scroll at the end after a reload, reload during an answer (the page attaches to
// the completion still running in the pod) and during a tool (interrupted, then goes on).
const http = require('http')
const { run, openProject, assert, OUT } = require('../common.cjs')

const requests = []
const byScenario = {}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))
const call = (id, name, args) => ({ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } })

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 }, modalities: { vision: true } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  requests.push(r)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const firstUser = msgs.find((m, i) => i > 0 && m.role === 'user')
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user')
  const scenario = text(lastUser).split(' ')[0]
  byScenario[scenario] = (byScenario[scenario] ?? 0) + 1
  if (scenario === 'lent') {
    sse(res, { content: 'Partie 1.' })
    for (let i = 0; i < 25 && !res.destroyed; i++) {
      await sleep(100)
      sse(res, { content: ' x' })
    }
    sse(res, { content: ' Fin.' })
    return end(res)
  }
  if (scenario === 'outil-lent') {
    if (last.role === 'user') return sse(res, { tool_calls: [call('b1', 'bash', { command: 'sleep 3; echo fini' })] }), end(res, 'tool_calls')
    sse(res, { content: `Reprise : ${text(last)}` })
    return end(res)
  }
  if (scenario === 'outil') {
    if (last.role === 'user' && text(last).startsWith('outil')) return sse(res, { tool_calls: [call('q1', 'bash', { command: 'sleep 1; echo ok' })] }), end(res, 'tool_calls')
    sse(res, { content: `Vu : ${text(last)}` })
    return end(res)
  }
  if (scenario === 'attends') {
    await sleep(1200)
    sse(res, { content: 'Fini.' })
    return end(res)
  }
  if (scenario === 'long') {
    for (let i = 1; i <= 40; i++) sse(res, { content: `Paragraphe ${i} : du texte pour remplir la conversation et la faire défiler.\n\n` })
    sse(res, { content: '```mermaid\ngraph TD\n  A-->B\n  B-->C\n  C-->D\n  D-->E\n```\n\nDernière ligne.' })
    return end(res)
  }
  // A message injected after a tool result.
  if (last.role === 'user' && msgs[msgs.length - 2]?.role === 'tool') {
    sse(res, { content: `Vu : ${text(last)}` })
    return end(res)
  }
  const images = Array.isArray(lastUser.content) ? lastUser.content.filter((p) => p.type === 'image_url').length : 0
  sse(res, { content: `Réponse à ${text(lastUser)}${images ? ` (+${images} image)` : ''}` })
  end(res)
})

async function ask(page, message, expect) {
  await page.click('.ai-composer textarea')
  await page.fill('.ai-composer textarea', message)
  await page.keyboard.press('Enter')
  await page.waitForSelector(`.ai-msg.assistant:not(.live) .md:has-text("${expect}")`, { timeout: 20000 })
  await page.waitForSelector('[data-testid=send]', { timeout: 10000 })
}

const atEnd = (page) => page.$eval('.ai-messages', (el) => el.scrollHeight - el.scrollTop - el.clientHeight)

run(async ({ page, ctx }) => {
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

    // Side bar: search first, then the new conversation button.
    await page.click('.ai-panel button[title="Conversations du projet"]')
    const order = await page.$$eval('.ai-sidebar > *', (e) => e.map((x) => x.className))
    assert(order[0].includes('ai-side-search') && order[1].includes('ai-new-chat'), 'barre latérale : recherche puis nouvelle conversation')
    await page.click('.ai-panel button[title="Conversations du projet"]')

    // Ctrl+click on an @file opens it.
    await ask(page, 'Regarde @src/main.go', 'Réponse à Regarde @src/main.go')
    await page.click('.ai-msg.user .ai-mention:has-text("@src/main.go")', { modifiers: ['Control'] })
    await page.waitForFunction(() => document.querySelector('.pane.active .tab.active')?.textContent.includes('main.go'), null, { timeout: 5000 }).catch(() => {})
    assert((await page.textContent('.pane.active .tab.active').catch(() => '')).includes('main.go'), 'Ctrl+clic sur @fichier ouvre le fichier')
    // The editor takes the focus once open: come back to the message box.
    await page.waitForTimeout(400)
    await page.click('.ai-composer textarea')

    // Queue: a message written during an answer is sent after it.
    await page.fill('.ai-composer textarea', 'attends un peu')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=stop]')
    await page.fill('.ai-composer textarea', 'suivant')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-queue]:has-text("suivant")')
    assert(true, 'message mis en file d’attente pendant la réponse')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Réponse à suivant")', { timeout: 15000 })
    assert(!(await page.isVisible('[data-testid=ai-queue]')), 'file vidée et message envoyé après la réponse')

    // Queue between tool steps: the message joins the conversation after the tool result.
    await page.fill('.ai-composer textarea', 'outil puis')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-tool.running', { timeout: 10000 })
    await page.fill('.ai-composer textarea', 'et aussi ça')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Vu : et aussi ça")', { timeout: 15000 })
    const inj = requests[requests.length - 1].messages.slice(-2)
    assert(inj[0].role === 'tool' && inj[1].role === 'user' && text(inj[1]) === 'et aussi ça', 'message injecté entre deux étapes d’outils')
    await page.waitForSelector('[data-testid=send]')

    // Edit in place, attachment kept, the answer regenerated from there.
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGP8z8DwnwEIGBkZGRgYAAAiBgID0ZfYpQAAAABJRU5ErkJggg==', 'base64')
    await page.setInputFiles('.ai-composer input[type=file]', { name: 'p.png', mimeType: 'image/png', buffer: png })
    await page.waitForSelector('.ai-composer .ai-att:has-text("p.png")')
    await ask(page, 'Décris', 'Réponse à Décris (+1 image)')
    const before = await page.$$eval('.ai-msg.user', (e) => e.length)
    await page.hover('.ai-msg.user:has-text("Décris")')
    await page.click('.ai-msg.user:has-text("Décris") .ai-act[title="Modifier"]')
    await page.waitForSelector('[data-testid=ai-edit] textarea')
    await page.fill('[data-testid=ai-edit] textarea', 'Décris en détail')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Réponse à Décris en détail (+1 image)")', { timeout: 10000 })
    assert((await page.$$eval('.ai-msg.user', (e) => e.length)) === before && !(await page.isVisible('.md:has-text("Réponse à Décris (+1 image)")')), 'message modifié sur place, réponse régénérée, image gardée')

    // Long answer, reload: the view ends at the bottom.
    await ask(page, 'long texte', 'Dernière ligne.')
    await page.reload()
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Dernière ligne.")', { timeout: 10000 })
    await page.waitForSelector('.md-mermaid-svg svg', { timeout: 15000 }).catch(() => {})
    await page.waitForTimeout(800)
    const gap = await atEnd(page)
    assert(gap < 40, `après un rechargement, la conversation est affichée jusqu’en bas (écart ${Math.round(gap)} px)`)

    // Reload during an answer: the page attaches to the completion running in the pod.
    await page.waitForSelector('[data-testid=send]')
    await page.fill('.ai-composer textarea', 'lent stream')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.live .md:has-text("Partie 1. x x")', { timeout: 10000 })
    await page.reload()
    await page.waitForSelector('.ai-msg.live .md:has-text("Partie 1.")', { timeout: 10000 })
    assert(true, 'après le rechargement, la réponse en cours est affichée avec ce qui était écrit')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Fin.")', { timeout: 15000 })
    const full = await page.$$eval('.ai-msg.assistant:not(.live) .md', (e) => e[e.length - 1].textContent)
    assert(full.startsWith('Partie 1.') && (full.match(/x/g) ?? []).length === 25 && byScenario.lent === 1, `réponse complète, sans nouvelle requête (${byScenario.lent} requête)`)
    await page.screenshot({ path: OUT + '/chat-resume.png' })

    // Reload during a tool: marked interrupted, the agent goes on.
    await page.waitForSelector('[data-testid=send]')
    await page.fill('.ai-composer textarea', 'outil-lent maintenant')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-tool.running', { timeout: 10000 })
    await page.reload()
    const resumed = await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Reprise : Interrompu par le rechargement")', { timeout: 15000 }).then(() => true, () => false)
    const rows = await page.$$eval('.ai-tool', (e) => e.filter((x) => x.textContent.includes('sleep 3')).map((x) => x.className))
    assert(resumed && rows.length === 1 && rows[0].includes('error') && byScenario['outil-lent'] === 2, `outil interrompu par le rechargement (sans relance), puis l’agent continue (${rows.length} outil, ${byScenario['outil-lent']} requêtes)`)

    // Another window opened during an answer follows its stream.
    await page.waitForSelector('[data-testid=send]')
    const toolUrl = new URL(new URL(page.url()).pathname + '/tool/assistant', page.url()).href
    const other = await ctx.newPage()
    await other.goto(toolUrl)
    await other.waitForSelector('.ai-panel.detached .ai-composer')
    const lentBefore = byScenario.lent
    await page.click('.ai-composer textarea')
    await page.fill('.ai-composer textarea', 'lent un')
    await page.keyboard.press('Enter')
    await other.waitForSelector('.ai-msg.live .md:has-text("Partie 1. x")', { timeout: 10000 })
    assert(true, 'l’autre fenêtre suit la réponse en cours')
    assert((await other.getAttribute('.ai-composer textarea', 'placeholder')).includes('autre fenêtre'), 'zone de saisie de l’autre fenêtre en lecture seule')
    await other.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Fin.")', { timeout: 15000 })
    assert(byScenario.lent === lentBefore + 1, 'réponse reçue dans l’autre fenêtre sans nouvelle requête')

    // Stop from the window that follows.
    await page.waitForSelector('[data-testid=send]')
    await page.fill('.ai-composer textarea', 'lent deux')
    await page.keyboard.press('Enter')
    await other.waitForSelector('.ai-msg.live .md:has-text("Partie 1. x")', { timeout: 10000 })
    await other.click('[data-testid=stop]')
    await page.waitForSelector('.ai-error:has-text("Arrêté")', { timeout: 10000 })
    assert(true, 'Arrêter depuis l’autre fenêtre arrête la réponse')

    // The running window closes: the other one takes over and gets the end.
    await page.waitForSelector('[data-testid=send]')
    await other.waitForSelector('[data-testid=send]', { timeout: 10000 })
    await page.fill('.ai-composer textarea', 'lent trois')
    await page.keyboard.press('Enter')
    await other.waitForSelector('.ai-msg.live .md:has-text("Partie 1. x")', { timeout: 10000 })
    await page.close()
    await other.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Fin.")', { timeout: 15000 })
    const ends = await other.$$eval('.ai-msg.assistant:not(.live) .md', (e) => e.filter((x) => x.textContent.includes('Fin.')).length)
    assert(ends >= 2, 'la fenêtre restante reprend la conversation et reçoit la fin')
    await other.screenshot({ path: OUT + '/chat-other-window.png' })
  } finally {
    fake.close()
  }
})
