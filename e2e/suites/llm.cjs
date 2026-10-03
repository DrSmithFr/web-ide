// AI assistant: server setup, model list, streamed answer with tool calls (read, language
// server, confirmed edit), Markdown + Mermaid rendering, saved conversations, image
// attachment and stop. The model is a scripted fake OpenAI server.
const fs = require('fs')
const http = require('http')
const { run, openProject, open, assert, text, WS, OUT } = require('../common.cjs')

const requests = []
let slowClosed = false

function chunk(res, delta, extra = {}) {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }], ...extra })}\n\n`)
}
function finish(res, reason, usage) {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  if (usage) res.write(`data: ${JSON.stringify({ choices: [], usage, timings: { predicted_per_second: 42.5, predicted_ms: 300, prompt_ms: 100 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const call = (id, name, args) => ({ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } })

/** Smallest valid PDF with one line of text. */
function tinyPdf(text) {
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 100] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    null,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]
  const stream = `BT /F1 18 Tf 20 50 Td (${text}) Tj ET`
  objs[3] = `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`
  let out = '%PDF-1.4\n'
  const offsets = []
  objs.forEach((o, i) => {
    offsets.push(out.length)
    out += `${i + 1} 0 obj\n${o}\nendobj\n`
  })
  const xref = out.length
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n` + offsets.map((o) => String(o).padStart(10, '0') + ' 00000 n \n').join('')
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(out, 'latin1')
}

const answer = [
  'J’ai renommé le message.\n\n',
  '```go\nfunc (g Greeter) Hello() string {\n\treturn fmt.Sprintf("Salut %s", g.Name)\n}\n```\n\n',
  '```mermaid\ngraph TD\n  main --> Hello\n```\n',
]

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) {
    if (req.url === '/props') return res.end(JSON.stringify({ role: 'router' }))
    return res.end(JSON.stringify({ modalities: { vision: true }, chat_template_caps: { supports_tools: true }, default_generation_settings: { n_ctx: 8192 } }))
  }
  if (req.url !== '/v1/chat/completions') return res.writeHead(404).end()
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  requests.push(r)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const userText = (m) => (typeof m.content === 'string' ? m.content : m.content.map((p) => p.text ?? '').join(' '))
  const lastUser = [...msgs].reverse().find((m) => m.role === 'user')
  const toolResults = msgs.filter((m) => m.role === 'tool').length

  if (userText(lastUser).includes('lent')) {
    res.on('close', () => (slowClosed = true))
    chunk(res, { content: 'Je commence\n\n```mermaid\ngraph TD\n  A-->B\n```\n\n' })
    for (let i = 0; i < 100 && !res.destroyed; i++) {
      await sleep(100)
      chunk(res, { content: '.' }, { timings: { predicted_n: i + 5, predicted_per_second: 10 } })
    }
    return finish(res, 'stop')
  }
  if (Array.isArray(lastUser.content) && lastUser === last && lastUser.content.some((p) => p.type === 'text' && p.text.includes('PDF « doc.pdf »'))) {
    const pdfText = lastUser.content.find((p) => p.text?.includes('PDF « doc.pdf »')).text
    chunk(res, { content: pdfText.includes('Bonjour du PDF') ? 'Le PDF dit bonjour.' : 'PDF vide.' })
    return finish(res, 'stop')
  }
  if (Array.isArray(lastUser.content) && lastUser === last) {
    const hasImage = lastUser.content.some((p) => p.type === 'image_url' && p.image_url.url.startsWith('data:image/'))
    chunk(res, { content: hasImage ? 'Je vois une image.' : 'Pas d’image.' })
    return finish(res, 'stop', { prompt_tokens: 50, completion_tokens: 5 })
  }
  if (last.role === 'user') {
    chunk(res, { reasoning_content: 'Je dois lire ' })
    await sleep(50)
    chunk(res, { reasoning_content: 'le fichier.' })
    chunk(res, { tool_calls: [call('c1', 'read_file', { path: 'src/main.go' })] })
    chunk(res, { tool_calls: [{ ...call('c2', 'lsp_symbols', { path: 'src/main.go' }), index: 1 }] })
    return finish(res, 'tool_calls', { prompt_tokens: 100, completion_tokens: 20 })
  }
  if (last.role === 'tool' && toolResults === 2) {
    chunk(res, { tool_calls: [call('c3', 'edit_file', { path: 'src/main.go', old_string: '"Bonjour %s"', new_string: '"Salut %s"' })] })
    return finish(res, 'tool_calls')
  }
  for (const part of answer) {
    chunk(res, { content: part })
    await sleep(60)
  }
  finish(res, 'stop', { prompt_tokens: 300, completion_tokens: 40, prompt_tokens_details: { cached_tokens: 100 } })
})

run(async ({ page }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  const port = fake.address().port
  page.on('dialog', (d) => d.accept())
  try {
    await openProject(page)
    await open(page, 'main.go')
    await page.click('.rail-right .rail-btn[title="Assistant IA"]')
    await page.waitForSelector('.ai-panel .ai-empty')

    // Server setup.
    await page.click('.ai-empty button:has-text("Ajouter un serveur")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${port}`)
    await page.click('.ai-servers button:has-text("Ajouter")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")', { timeout: 5000 })
    assert(true, 'serveur ajouté, modèle choisi automatiquement')
    await page.click('[data-testid=model-pill]')
    assert(await page.isVisible('.ai-model-item.active .badge:has-text("image")'), 'capacités du modèle affichées dans le choix du modèle')
    await page.keyboard.press('Escape')
    const stored = JSON.parse(fs.readFileSync(process.env.E2E_WS + '/../data/llm.json', 'utf8'))
    assert(stored.servers[0].url === `http://127.0.0.1:${port}` && stored.model === 'fake-model', 'configuration enregistrée dans llm.json')

    // Question → reading tools → confirmed edit → answer.
    await page.fill('.ai-composer textarea', 'Remplace Bonjour par Salut dans main.go')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-approval]', { timeout: 20000 })
    const diff = await page.textContent('[data-testid=ai-approval] .ai-diff')
    assert(diff.includes('- \treturn fmt.Sprintf("Bonjour %s", g.Name)') && diff.includes('+ \treturn fmt.Sprintf("Salut %s", g.Name)'), 'aperçu du diff avant modification')
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('Bonjour'), 'rien n’est écrit avant confirmation')
    await page.screenshot({ path: OUT + '/llm-approval.png' })
    await page.click('[data-testid=ai-approval] button:has-text("Appliquer")')
    // The final answer (not the one being written) with its diagram.
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md-codeblock', { timeout: 15000 })
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md-mermaid-svg svg', { timeout: 30000 }).catch(() => {})
    if (!(await page.isVisible('.md-mermaid-svg svg'))) {
      await page.screenshot({ path: OUT + '/llm-mermaid-fail.png' })
      console.log('    DOM :', await page.$$eval('.ai-msg.assistant', (e) => e.map((x) => x.className + ' ' + x.querySelector('.md')?.innerHTML.slice(0, 300)).join('\n    ')))
    }
    assert(await page.isVisible('.md-mermaid-svg svg'), 'diagramme Mermaid rendu')
    await page.hover('.ai-msg.assistant:not(.live) .md-mermaid-svg')
    await page.click('.ai-msg.assistant:not(.live) .md-mermaid-open')
    await page.waitForSelector('[data-testid=diagram-viewer] .ai-diagram-content svg')
    const z0 = await page.textContent('.ai-diagram-zoom')
    await page.click('[data-testid=diagram-viewer] button[title="Zoomer (+)"]')
    const z1 = await page.textContent('.ai-diagram-zoom')
    assert(parseInt(z1) > parseInt(z0), `diagramme en plein écran avec zoom (${z0} → ${z1})`)
    await page.screenshot({ path: OUT + '/llm-diagram.png' })
    await page.keyboard.press('Escape')
    await page.waitForSelector('[data-testid=diagram-viewer]', { state: 'detached' })
    const labels = await page.$$eval('.md-mermaid-svg svg text, .md-mermaid-svg svg tspan', (e) => e.map((x) => x.textContent).join(' '))
    assert(labels.includes('main') && labels.includes('Hello'), 'libellés du diagramme visibles : ' + labels)
    assert((await page.$$('.md-codeblock .tok-keyword')).length > 0, 'bloc de code coloré par la grammaire de l’éditeur')
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('"Salut %s"'), 'fichier modifié sur le disque')
    await page.waitForFunction(() => document.querySelector('.pane.active .ed-content')?.textContent.includes('Salut %s'), null, { timeout: 5000 }).catch(() => {})
    assert((await text(page)).includes('"Salut %s"'), 'l’éditeur ouvert suit la modification')

    const tools = await page.$$eval('.ai-tool', (els) => els.map((e) => ({ cls: e.className, text: e.textContent })))
    assert(tools.length === 3 && tools.every((t) => t.cls.includes('ok')), 'trois appels d’outils réussis : ' + JSON.stringify(tools.map((t) => t.text)))
    assert(tools[0].text.includes('Lit') && tools[0].text.includes('src/main.go') && tools[0].text.includes('lignes 1-'), 'read_file résumé : ' + tools[0].text)
    const second = requests[1]
    const readResult = second.messages.find((m) => m.role === 'tool' && m.tool_call_id === 'c1')
    const symResult = second.messages.find((m) => m.role === 'tool' && m.tool_call_id === 'c2')
    assert(readResult?.content.includes('9\t\treturn fmt.Sprintf("Bonjour %s", g.Name)'), 'read_file renvoie les lignes numérotées')
    assert(/Hello/.test(symResult?.content ?? '') && /Greeter/.test(symResult?.content ?? ''), 'lsp_symbols renvoie la structure (gopls) : ' + (symResult?.content ?? '').slice(0, 60).replace(/\n/g, ' | '))
    assert(requests[0].messages[0].role === 'system' && requests[0].tools.length >= 10 && requests[0].stream === true, 'prompt système et outils envoyés')
    assert(!requests[0].messages.some((m) => 'usage' in m || 'attachments' in m), 'champs de la page retirés des messages')
    assert(await page.isVisible('.ai-reasoning:has-text("Je dois lire le fichier.")'), 'réflexion affichée')
    assert((await page.$$eval('.ai-usage', (e) => e[e.length - 1].textContent)).includes('42.5 jetons/s'), 'usage et vitesse affichés')
    await page.screenshot({ path: OUT + '/llm-answer.png' })

    // Saved conversation: history, new chat, reopen.
    await page.waitForTimeout(300)
    await page.click('.ai-panel button[title="Conversations du projet"]')
    await page.waitForSelector('.ai-chat-item.active:has-text("Remplace Bonjour")')
    assert(await page.isVisible('.ai-side-wrap.overlay'), 'historique en panneau latéral (par-dessus quand le panneau est étroit)')
    await page.click('.ai-new-chat')
    await page.waitForSelector('.ai-empty')
    assert(!(await page.isVisible('.ai-side-wrap')), 'le panneau se replie après le choix')
    await page.click('.ai-panel button[title="Conversations du projet"]')
    await page.click('.ai-chat-open:has-text("Remplace Bonjour")')
    await page.waitForSelector('.ai-msg.assistant .md-codeblock')
    assert((await page.$$('.ai-tool')).length === 3, 'conversation rouverte avec ses appels d’outils')

    // Image attachment (new conversation).
    await page.click('.ai-panel button[title="Nouvelle conversation"]')
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGP8z8DwnwEIGBkZGRgYAAAiBgID0ZfYpQAAAABJRU5ErkJggg==', 'base64')
    await page.setInputFiles('.ai-composer input[type=file]', { name: 'pixel.png', mimeType: 'image/png', buffer: png })
    await page.waitForSelector('.ai-composer .ai-att:has-text("pixel.png")')
    await page.fill('.ai-composer textarea', 'Que vois-tu ?')
    await page.click('[data-testid=send]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Je vois une image.")', { timeout: 10000 })
    assert(await page.isVisible('.ai-msg.user .ai-att img'), 'vignette de la pièce jointe dans le message')
    assert(true, 'image envoyée en image_url au modèle')

    // PDF attachment: its text is extracted by pdf.js (worker loaded on demand).
    await page.setInputFiles('.ai-composer input[type=file]', { name: 'doc.pdf', mimeType: 'application/pdf', buffer: tinyPdf('Bonjour du PDF') })
    await page.waitForSelector('.ai-composer .ai-att:has-text("doc.pdf")', { timeout: 10000 })
    await page.click('[data-testid=send]')
    await page.waitForFunction(() => document.querySelectorAll('.ai-msg.assistant .md').length >= 2, null, { timeout: 10000 }).catch(() => {})
    const pdfAnswer = await page.$$eval('.ai-msg.assistant .md, .ai-error', (e) => e[e.length - 1]?.textContent)
    assert(pdfAnswer?.trim() === 'Le PDF dit bonjour.', 'texte du PDF extrait et envoyé : ' + pdfAnswer)

    // Stop a slow answer.
    await page.fill('.ai-composer textarea', 'réponds lentement')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.live .md:has-text("Je commence")', { timeout: 5000 })
    const drawn = await page.waitForSelector('.ai-msg.live .md-mermaid-svg svg', { timeout: 8000 }).then(() => true, () => false)
    assert(drawn, 'diagramme Mermaid dessiné pendant le stream')
    const stats = await page.waitForFunction(() => /10\.0 jetons\/s · \d+ jetons · [\d.]+ s/.test(document.querySelector('[data-testid=ai-live-stats]')?.textContent ?? ''), null, { timeout: 5000 }).then(() => true, () => false)
    assert(stats, 'vitesse, jetons et temps écoulé pendant la réponse : ' + (await page.textContent('[data-testid=ai-live-stats]').catch(() => '')))
    await page.screenshot({ path: OUT + '/llm-live.png' })
    await page.click('[data-testid=stop]')
    await page.waitForSelector('.ai-error:has-text("Arrêté")', { timeout: 5000 })
    await page.waitForTimeout(300)
    assert(slowClosed, 'la requête au serveur de modèles est annulée')
    assert((await page.$$eval('.ai-msg.assistant .md', (e) => e[e.length - 1].textContent)).startsWith('Je commence'), 'le début de la réponse est gardé')
    assert(await page.isVisible('.ai-act[title="Régénérer la réponse"]'), 'bouton Régénérer proposé')
  } finally {
    fake.close()
  }
})
