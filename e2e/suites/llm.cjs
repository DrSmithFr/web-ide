// AI assistant: server setup, model list, streamed answer with tool calls (read, language
// server, confirmed edit), Markdown + Mermaid rendering, saved conversations, image
// attachment, stop, and resume after a crash (with the same or another model). The model is a scripted fake OpenAI server.
const fs = require('fs')
const http = require('http')
const { run, openProject, open, assert, text, WS, OUT } = require('../common.cjs')

const requests = []
let slowClosed = false
let crashed = false
let crashedOther = false
let cut = false

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
  'I renamed the message.\n\n',
  '```go\nfunc (g Greeter) Hello() string {\n\treturn fmt.Sprintf("Salut %s", g.Name)\n}\n```\n\n',
  '```mermaid\ngraph TD\n  main --> Hello\n```\n',
]

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }, { id: 'other-model', status: { value: 'loaded' } }] }))
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

  if (userText(lastUser).includes('slowly')) {
    res.on('close', () => (slowClosed = true))
    chunk(res, {}, { prompt_progress: { total: 200, cache: 50, processed: 120, time_ms: 100 } })
    await sleep(800)
    chunk(res, { content: 'Starting\n\n```mermaid\ngraph TD\n  A-->B\n```\n\n' })
    for (let i = 0; i < 100 && !res.destroyed; i++) {
      await sleep(100)
      chunk(res, { content: '.' }, { timings: { cache_n: 50, prompt_n: 150, prompt_per_second: 820.4, predicted_n: i + 5, predicted_per_second: 10 } })
    }
    return finish(res, 'stop')
  }
  if (userText(lastUser).includes('crash after a step')) {
    if (last.role === 'user') {
      chunk(res, { tool_calls: [call('k1', 'read_file', { path: 'src/main.go' })] })
      return finish(res, 'tool_calls')
    }
    if (!crashed) {
      crashed = true
      res.write(`data: ${JSON.stringify({ error: { message: 'model server crashed' } })}\n\n`)
      return res.end()
    }
    chunk(res, { content: 'Resumed after the step.' })
    return finish(res, 'stop')
  }
  if (userText(lastUser).includes('cut after a step')) {
    if (last.role === 'user') {
      chunk(res, { tool_calls: [call('k3', 'read_file', { path: 'src/main.go' })] })
      return finish(res, 'tool_calls')
    }
    if (!cut) {
      // the model server stopped in the middle (llama-swap unloading it): no error, no [DONE]
      cut = true
      chunk(res, { reasoning_content: 'Let me plan the' })
      return res.end()
    }
    chunk(res, { content: 'Resumed after the cut.' })
    return finish(res, 'stop')
  }
  if (userText(lastUser).includes('crash until another model')) {
    if (last.role === 'user') {
      chunk(res, { tool_calls: [call('k2', 'read_file', { path: 'src/main.go' })] })
      return finish(res, 'tool_calls')
    }
    if (r.model !== 'other-model') {
      crashedOther = true
      res.write(`data: ${JSON.stringify({ error: { message: 'model server crashed' } })}\n\n`)
      return res.end()
    }
    chunk(res, { content: 'Resumed by the other model.' })
    return finish(res, 'stop')
  }
  if (userText(lastUser).includes('think long')) {
    for (let i = 1; i <= 60 && !res.destroyed; i++) {
      chunk(res, { reasoning_content: `step ${i} of the reasoning\n` })
      await sleep(40)
    }
    chunk(res, { content: 'Thought enough.' })
    return finish(res, 'stop')
  }
  if (Array.isArray(lastUser.content) && lastUser === last && lastUser.content.some((p) => p.type === 'text' && p.text.includes('PDF "doc.pdf"'))) {
    const pdfText = lastUser.content.find((p) => p.text?.includes('PDF "doc.pdf"')).text
    chunk(res, { content: pdfText.includes('Hello from the PDF') ? 'The PDF says hello.' : 'Empty PDF.' })
    return finish(res, 'stop')
  }
  if (Array.isArray(lastUser.content) && lastUser === last) {
    const hasImage = lastUser.content.some((p) => p.type === 'image_url' && p.image_url.url.startsWith('data:image/'))
    chunk(res, { content: hasImage ? 'I see an image.' : 'No image.' })
    return finish(res, 'stop', { prompt_tokens: 50, completion_tokens: 5 })
  }
  if (last.role === 'user') {
    chunk(res, { reasoning_content: 'I need to read ' })
    await sleep(50)
    chunk(res, { reasoning_content: 'the file.' })
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

run(async ({ page, ctx }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  const port = fake.address().port
  page.on('dialog', (d) => d.accept())
  try {
    await openProject(page)
    await open(page, 'main.go')
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.waitForSelector('.ai-panel .ai-empty')

    // Server setup.
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")', { timeout: 5000 })
    assert(true, 'server added, model chosen automatically')
    await page.click('[data-testid=model-pill]')
    assert(await page.isVisible('.ai-model-item.active .badge:has-text("image")'), 'capabilities of the model shown in the model picker')
    await page.keyboard.press('Escape')
    const stored = JSON.parse(fs.readFileSync(process.env.E2E_WS + '/../data/llm.json', 'utf8'))
    assert(stored.servers[0].url === `http://127.0.0.1:${port}` && stored.model === 'fake-model', 'configuration saved in llm.json')

    // A second window showing another conversation is told when this one waits for the user.
    const other = await ctx.newPage()
    await other.goto(new URL(new URL(page.url()).pathname + '/tool/assistant', page.url()).href)
    await other.waitForSelector('.ai-panel.detached .ai-composer')

    // Question → reading tools → confirmed edit → answer.
    await page.fill('.ai-composer textarea', 'Replace Bonjour with Salut in main.go')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-approval]', { timeout: 20000 })
    const diff = await page.textContent('[data-testid=ai-approval] .ai-diff')
    assert(diff.includes('- \treturn fmt.Sprintf("Bonjour %s", g.Name)') && diff.includes('+ \treturn fmt.Sprintf("Salut %s", g.Name)'), 'diff preview before the change')
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('Bonjour'), 'nothing is written before confirmation')
    await page.screenshot({ path: OUT + '/llm-approval.png' })
    // The other window opens it from its toast and confirms the change there.
    await other.waitForSelector('.toast:has-text("asks to change a file")', { timeout: 5000 })
    await other.click('.toast:has-text("asks to change a file") button:has-text("Open")')
    await other.waitForSelector('[data-testid=ai-approval] .ai-diff')
    await other.click('[data-testid=ai-approval] button:has-text("Apply")')
    assert(true, 'a change waiting for the user is announced to the other windows and confirmed from one of them')
    await page.waitForSelector('[data-testid=ai-approval]', { state: 'detached', timeout: 5000 })
    await other.close()
    // The final answer (not the one being written) with its diagram.
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md-codeblock', { timeout: 15000 })
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md-mermaid-svg svg', { timeout: 30000 }).catch(() => {})
    if (!(await page.isVisible('.md-mermaid-svg svg'))) {
      await page.screenshot({ path: OUT + '/llm-mermaid-fail.png' })
      console.log('    DOM :', await page.$$eval('.ai-msg.assistant', (e) => e.map((x) => x.className + ' ' + x.querySelector('.md')?.innerHTML.slice(0, 300)).join('\n    ')))
    }
    assert(await page.isVisible('.md-mermaid-svg svg'), 'Mermaid diagram rendered')
    await page.hover('.ai-msg.assistant:not(.live) .md-mermaid-svg')
    await page.click('.ai-msg.assistant:not(.live) .md-mermaid-open')
    await page.waitForSelector('[data-testid=diagram-viewer] .ai-diagram-content svg')
    const z0 = await page.textContent('.ai-diagram-zoom')
    await page.click('[data-testid=diagram-viewer] button[title="Zoom in (+)"]')
    const z1 = await page.textContent('.ai-diagram-zoom')
    assert(parseInt(z1) > parseInt(z0), `full-screen diagram with zoom (${z0} → ${z1})`)
    await page.screenshot({ path: OUT + '/llm-diagram.png' })
    await page.keyboard.press('Escape')
    await page.waitForSelector('[data-testid=diagram-viewer]', { state: 'detached' })
    const labels = await page.$$eval('.md-mermaid-svg svg text, .md-mermaid-svg svg tspan', (e) => e.map((x) => x.textContent).join(' '))
    assert(labels.includes('main') && labels.includes('Hello'), 'labels of the diagram visible: ' + labels)
    assert((await page.$$('.md-codeblock .tok-keyword')).length > 0, 'code block highlighted by the grammar of the editor')
    assert(fs.readFileSync(WS + '/demo/src/main.go', 'utf8').includes('"Salut %s"'), 'file changed on disk')
    await page.waitForFunction(() => document.querySelector('.pane.active .ed-content')?.textContent.includes('Salut %s'), null, { timeout: 5000 }).catch(() => {})
    assert((await text(page)).includes('"Salut %s"'), 'the open editor follows the change')

    const tools = await page.$$eval('.ai-tool', (els) => els.map((e) => ({ cls: e.className, text: e.textContent })))
    assert(tools.length === 3 && tools.every((t) => t.cls.includes('ok')), 'three successful tool calls: ' + JSON.stringify(tools.map((t) => t.text)))
    assert(tools[0].text.includes('Reads') && tools[0].text.includes('src/main.go') && tools[0].text.includes('lines 1-'), 'read_file summary: ' + tools[0].text)
    const second = requests[1]
    const readResult = second.messages.find((m) => m.role === 'tool' && m.tool_call_id === 'c1')
    const symResult = second.messages.find((m) => m.role === 'tool' && m.tool_call_id === 'c2')
    assert(readResult?.content.includes('9\t\treturn fmt.Sprintf("Bonjour %s", g.Name)'), 'read_file returns numbered lines')
    assert(/Hello/.test(symResult?.content ?? '') && /Greeter/.test(symResult?.content ?? ''), 'lsp_symbols returns the structure (gopls): ' + (symResult?.content ?? '').slice(0, 60).replace(/\n/g, ' | '))
    assert(requests[0].messages[0].role === 'system' && requests[0].tools.length >= 10 && requests[0].stream === true, 'system prompt and tools sent')
    assert(!requests[0].messages.some((m) => 'usage' in m || 'attachments' in m), 'fields of the page removed from the messages')
    assert(await page.isVisible('.ai-reasoning .ai-tool-head:has-text("Thinking")'), 'reasoning shown as a block')
    assert(!(await page.isVisible('.ai-reasoning-text')) && !(await page.isVisible('.ai-tool-detail')), 'blocks folded once done')
    assert((await page.$$eval('.ai-tool .ai-dur', (e) => e.length)) === 3, 'each tool block shows its duration')
    await page.click('.ai-reasoning .ai-tool-head')
    assert(await page.isVisible('.ai-reasoning-text:has-text("I need to read the file.")'), 'reasoning unfolded by a click')
    // Dynamic effort: the most after the message, medium after reading, low after an edit.
    const efforts = requests.slice(0, 3).map((r) => r.chat_template_kwargs?.reasoning_effort)
    assert(efforts.join() === 'xhigh,medium,low', 'reasoning effort of each step: ' + efforts)
    assert(!requests[0].tools.some((d) => d.function.name === 'set_effort'), 'no set_effort unless turned on')
    assert((await page.textContent('.ai-reasoning .ai-effort').catch(() => '')) === 'Max', 'the effort of the answer is shown')
    await page.click('[data-testid=ai-options]')
    await page.waitForSelector('[data-testid=opt-effort]', { timeout: 3000 }).catch(() => {})
    await page.waitForTimeout(200) // the popover fades in
    await page.screenshot({ path: OUT + '/llm-effort.png' })
    assert(await page.isVisible('[data-testid=opt-effort] button.on:has-text("Dynamic")') && (await page.isVisible('[data-testid=opt-effort-tool]')), 'effort selector, dynamic by default, with its tool toggle')
    const box = await page.$eval('.ai-options-pop', (e) => e.getBoundingClientRect().toJSON())
    assert(box.left >= 0 && box.right <= page.viewportSize().width, 'the options stay on the screen: ' + JSON.stringify(box))
    await page.click('[data-testid=opt-effort] button:has-text("Medium")')
    assert(!(await page.isVisible('[data-testid=opt-effort-tool]')), 'the tool toggle only in the dynamic mode')
    assert((await page.evaluate(() => JSON.parse(localStorage.getItem('webide.llm.prefs')).effort)) === 'medium', 'the effort is kept with the preferences')
    await page.click('[data-testid=opt-effort] button:has-text("Dynamic")')
    await page.keyboard.press('Escape')

    // Statistics of the conversation, next to the board: two of the three calls in one answer.
    await page.click('[data-testid=ai-stats-toggle]')
    await page.waitForSelector('[data-testid=st-tools] td:has-text("read_file")', { timeout: 5000 })
    await page.screenshot({ path: OUT + '/llm-stats.png' })
    const tile = (id) => page.textContent(`[data-testid=${id}] .st-tile-value`)
    assert((await tile('st-multi')) === '50 %' && (await tile('st-failures')) === '0 %', 'calls at once and failures: ' + (await tile('st-multi')) + ' / ' + (await tile('st-failures')))
    assert(/tokens\/s/.test(await tile('st-write')), 'writing speed: ' + (await tile('st-write')))
    const toolRows = await page.$$eval('[data-testid=st-tools] tbody tr', (r) => r.map((x) => x.firstElementChild.textContent))
    assert(['read_file', 'lsp_symbols', 'edit_file'].every((n) => toolRows.includes(n)), 'time by tool: ' + toolRows)
    await page.click('[data-testid=st-scope-project]')
    await page.waitForSelector('.st-note:has-text("conversations")', { timeout: 5000 })
    assert(await page.isVisible('[data-testid=st-effort]'), 'the project is filtered by effort')
    await page.click('[data-testid=ai-side-board]')
    assert((await page.isVisible('[data-testid=bd-board]')) && !(await page.isVisible('[data-testid=st-view]')), 'the column switches to the board')
    await page.click('[data-testid=ai-side-stats]')
    await page.click('[data-testid=ai-stats-toggle]')
    assert(!(await page.isVisible('[data-testid=st-view]')), 'the statistics close')
    assert((await page.$$eval('.ai-usage', (e) => e[e.length - 1].textContent)).includes('42.5 tokens/s'), 'usage and speed shown')
    await page.screenshot({ path: OUT + '/llm-answer.png' })

    // Saved conversation: history, new chat, reopen.
    await page.waitForTimeout(300)
    await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.waitForSelector('.ai-chat-item.active:has-text("Replace Bonjour")')
    assert(await page.isVisible('.ai-side-wrap.overlay'), 'history in a side panel (over the conversation when the panel is narrow)')
    await page.click('.ai-new-chat')
    await page.waitForSelector('.ai-empty')
    assert(!(await page.isVisible('.ai-side-wrap')), 'the panel folds after the choice')
    await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.click('.ai-chat-open:has-text("Replace Bonjour")')
    await page.waitForSelector('.ai-msg.assistant .md-codeblock')
    assert((await page.$$('.ai-tool')).length === 3, 'conversation reopened with its tool calls')

    // Image attachment (new conversation).
    await page.click('.ai-panel button[title="New conversation"]')
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGP8z8DwnwEIGBkZGRgYAAAiBgID0ZfYpQAAAABJRU5ErkJggg==', 'base64')
    await page.setInputFiles('.ai-composer input[type=file]', { name: 'pixel.png', mimeType: 'image/png', buffer: png })
    await page.waitForSelector('.ai-composer .ai-att:has-text("pixel.png")')
    await page.fill('.ai-composer textarea', 'What do you see?')
    await page.click('[data-testid=send]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("I see an image.")', { timeout: 10000 })
    assert(await page.isVisible('.ai-msg.user .ai-att img'), 'thumbnail of the attachment in the message')
    assert(true, 'image sent as image_url to the model')

    // PDF attachment: its text is extracted by pdf.js (worker loaded on demand).
    await page.setInputFiles('.ai-composer input[type=file]', { name: 'doc.pdf', mimeType: 'application/pdf', buffer: tinyPdf('Hello from the PDF') })
    await page.waitForSelector('.ai-composer .ai-att:has-text("doc.pdf")', { timeout: 10000 })
    await page.click('[data-testid=send]')
    await page.waitForFunction(() => document.querySelectorAll('.ai-msg.assistant .md').length >= 2, null, { timeout: 10000 }).catch(() => {})
    const pdfAnswer = await page.$$eval('.ai-msg.assistant .md, .ai-error', (e) => e[e.length - 1]?.textContent)
    assert(pdfAnswer?.trim() === 'The PDF says hello.', 'text of the PDF extracted and sent: ' + pdfAnswer)

    // The live reasoning box follows the end of the text.
    await page.fill('.ai-composer textarea', 'think long')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.live [data-testid=ai-reasoning-text]:has-text("step 40 ")', { timeout: 10000 })
    const follows = await page.$eval('.ai-msg.live [data-testid=ai-reasoning-text]', (b) => b.scrollHeight > b.clientHeight && b.scrollHeight - b.scrollTop - b.clientHeight < 30)
    assert(follows, 'the live reasoning scrolls to its end')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Thought enough.")', { timeout: 10000 })

    // Stop a slow answer.
    await page.fill('.ai-composer textarea', 'answer slowly')
    await page.keyboard.press('Enter')
    const reading = await page.waitForFunction(() => /Reading the prompt · 60 % .* · prompt 700 tokens\/s · cache 25 %/.test(document.querySelector('[data-testid=ai-live-stats]')?.textContent ?? ''), null, { timeout: 5000 }).then(() => true, () => false)
    assert(reading, 'prompt reading speed and cache ratio while the prompt is read: ' + (await page.textContent('[data-testid=ai-live-stats]').catch(() => '')))
    await page.waitForSelector('.ai-msg.live .md:has-text("Starting")', { timeout: 5000 })
    const drawn = await page.waitForSelector('.ai-msg.live .md-mermaid-svg svg', { timeout: 8000 }).then(() => true, () => false)
    assert(drawn, 'Mermaid diagram drawn during the stream')
    const stats = await page.waitForFunction(() => /10\.0 tokens\/s · \d+ tokens · [\d.]+ s/.test(document.querySelector('[data-testid=ai-live-stats]')?.textContent ?? ''), null, { timeout: 5000 }).then(() => true, () => false)
    assert(stats, 'speed, tokens and elapsed time during the answer: ' + (await page.textContent('[data-testid=ai-live-stats]').catch(() => '')))
    const promptStats = await page.textContent('[data-testid=ai-live-stats]').catch(() => '')
    assert(!promptStats.includes('prompt') && !promptStats.includes('cache'), 'only the generation once the prompt is read: ' + promptStats)
    await page.screenshot({ path: OUT + '/llm-live.png' })
    await page.click('[data-testid=stop]')
    await page.waitForSelector('.ai-error:has-text("Stopped")', { timeout: 5000 })
    await page.waitForTimeout(300)
    assert(slowClosed, 'the request to the model server is canceled')
    assert((await page.$$eval('.ai-msg.assistant .md', (e) => e[e.length - 1].textContent)).startsWith('Starting'), 'the start of the answer is kept')
    assert(await page.isVisible('.ai-act[title="Generate the answer again"]'), 'regenerate button offered')

    // A crash after a step: Resume keeps the step and asks only the failed answer again.
    const before = requests.length
    await page.fill('.ai-composer textarea', 'crash after a step')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-resume]', { timeout: 10000 })
    assert(await page.isVisible('.ai-error:has-text("model server crashed")'), 'error of the model server shown')
    await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.waitForSelector('.ai-chat-item.active [data-testid=ai-dot-failed]', { timeout: 5000 })
    assert(true, 'a red dot in the list for the failed conversation')
    await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.click('[data-testid=ai-resume]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Resumed after the step.")', { timeout: 10000 })
    const sent = requests.slice(before)
    const resumed = sent[2]?.messages ?? []
    assert(sent.length === 3 && resumed.filter((m) => m.role === 'tool' && m.tool_call_id === 'k1').length === 1 && resumed[resumed.length - 1].role === 'tool', 'resume sends the step already done, without running it again: ' + sent.length + ' requests')
    assert(!(await page.isVisible('[data-testid=ai-resume]')), 'no resume button once the answer is complete')

    // A stream closed without its end (the model server stopped): an error, with Resume.
    await page.fill('.ai-composer textarea', 'cut after a step')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-resume]', { timeout: 10000 })
    assert(cut && (await page.isVisible('.ai-error:has-text("closed the answer before its end")')), 'a cut stream shown as an error')
    await page.click('[data-testid=ai-resume]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Resumed after the cut.")', { timeout: 10000 })

    // A crash, then another model chosen: offered to resume with it, under the two buttons.
    const beforeOther = requests.length
    await page.fill('.ai-composer textarea', 'crash until another model')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-resume]', { timeout: 10000 })
    assert(crashedOther && !(await page.isVisible('[data-testid=ai-resume-other]')), 'no resume with another model while the model chosen is the one of the conversation')
    await page.click('[data-testid=model-pill]')
    await page.click('.ai-model-item:has-text("other-model")')
    await page.waitForSelector('[data-testid=ai-resume-other]:has-text("Resume with other-model")', { timeout: 5000 })
    const below = await page.evaluate(() => document.querySelector('[data-testid=ai-resume-other]').getBoundingClientRect().top > document.querySelector('[data-testid=ai-resume]').getBoundingClientRect().bottom)
    assert(below, 'resume with the model chosen shown under Resume and Retry')
    await page.click('[data-testid=ai-resume-other]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Resumed by the other model.")', { timeout: 10000 })
    const otherSent = requests.slice(beforeOther)
    const last = otherSent[otherSent.length - 1]
    assert(last.model === 'other-model' && last.messages.filter((m) => m.role === 'tool' && m.tool_call_id === 'k2').length === 1, 'the other model goes on from the step already done: ' + otherSent.map((q) => q.model).join(', '))

    // Abandon a failed conversation: no more Resume, no more red dot, in the history.
    await page.click('[data-testid=model-pill]')
    await page.click('.ai-model-item:has-text("fake-model")')
    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer textarea', 'crash until another model')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-dismiss]', { timeout: 10000 })
    await page.click('[data-testid=ai-dismiss]')
    await page.waitForSelector('[data-testid=ai-resume]', { state: 'detached', timeout: 5000 })
    await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.waitForFunction(() => document.querySelector('.ai-chat-item.active') && !document.querySelector('.ai-chat-item.active [data-testid=ai-dot-failed]'), null, { timeout: 5000 })
    assert(!(await page.isVisible('[data-testid=ai-side-active] .ai-chat-item.active')), 'the abandoned conversation leaves the active ones for the history, without its red dot')

    // A wide window: the statistics next to a conversation follow it while it runs (the
    // reading step is counted while the edit waits for its approval).
    const w = await page.context().newPage()
    await w.goto(new URL(new URL(page.url()).pathname.replace(/\/$/, '') + '/tool/assistant', page.url()).href)
    await w.waitForSelector('.ai-panel.detached')
    await w.click('.ai-panel button[title="New conversation"]')
    await w.click('[data-testid=ai-stats-toggle]')
    await w.waitForSelector('[data-testid=st-view]')
    await w.fill('.ai-composer textarea', 'Explain the greeter')
    await w.keyboard.press('Enter')
    const followed = await w.waitForSelector('[data-testid=st-tools] td:has-text("lsp_symbols")', { timeout: 8000 }).then(() => true, () => false)
    assert(followed && (await w.isVisible('.ai-composer')) && !!(await w.$('[data-testid=stop]')), 'the statistics follow the answer running next to them')
    await w.screenshot({ path: OUT + '/llm-stats-wide.png' })
    await w.click('[data-testid=stop]')
    await w.close()
  } finally {
    fake.close()
  }
})
