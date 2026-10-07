// Shots of the real UI for the promo video, on a fresh pod with a scripted model (run through
// e2e/run.sh by `make promo`): editor, kanban, the Orchestrator and its sub-agents, a question
// card, an app preview, the home page, and the phone layout. PNGs in promo/out/shots.
const fs = require('fs')
const path = require('path')
const http = require('http')
const net = require('net')
const { start, openProject, open, WS } = require('../e2e/common.cjs')

const OUT = path.join(__dirname, 'out/shots')
fs.mkdirSync(OUT, { recursive: true })
const shot = (page, name, opts = {}) => page.screenshot({ path: path.join(OUT, name + '.png'), ...opts })

// A project worth looking at.
fs.writeFileSync(
  WS + '/demo/src/server.go',
  `package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Order is what the shop sells, one line per article.
type Order struct {
	ID      string    \`json:"id"\`
	Lines   []Line    \`json:"lines"\`
	Created time.Time \`json:"created"\`
}

type Line struct {
	SKU      string \`json:"sku"\`
	Quantity int    \`json:"quantity"\`
	Cents    int    \`json:"cents"\`
}

// Total of the order, in cents.
func (o Order) Total() int {
	total := 0
	for _, l := range o.Lines {
		total += l.Quantity * l.Cents
	}
	return total
}

func main() {
	http.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		var o Order
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		o.Created = time.Now()
		json.NewEncoder(w).Encode(map[string]any{"id": o.ID, "total": o.Total()})
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
`,
)

// ---------- the scripted model ----------

const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
const end = (res, reason = 'stop') => {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 1800, completion_tokens: 120 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))
const calls = (res, list) => {
  sse(res, { tool_calls: list.map(([id, name, args], index) => ({ index, id, type: 'function', function: { name, arguments: JSON.stringify(args) } })) })
  end(res, 'tool_calls')
}
const say = (res, s) => (sse(res, { content: s }), end(res))

let appPort = 0
const model = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'qwen3.8-27b', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 65536 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const sys = text(r.messages[0])
  const last = r.messages[r.messages.length - 1]
  const content = text(last)
  if (sys.includes('# You are a sub-agent')) {
    // Children stay busy for the shot.
    await new Promise((ok) => setTimeout(ok, 60_000))
    return say(res, 'done')
  }
  if (content === 'What do we work on today?') return calls(res, [['n', 'kanban_next', {}]])
  if (content.startsWith('Can start now'))
    return calls(res, [['c', 'action_card', { kind: 'start_dev', ticket: 2, label: 'Start #2 Checkout page', reason: 'High priority, unblocked, small.' }]])
  if (last.role === 'tool' && last.tool_call_id === 'c')
    return say(res, '**#2 Checkout page** is next: high priority, nothing blocks it, size S.\n\nAfter it, **#3 Order history** is ready too.')
  if (content === 'Split the review') {
    return calls(res, [
      ['s1', 'spawn_agent', { title: 'Review the API', task: 'Review src/server.go', files: ['src/server.go'] }],
      ['s2', 'spawn_agent', { title: 'Check the tests', task: 'Run the tests and report' }],
      ['s3', 'spawn_agent', { title: 'Audit the SQL', task: 'Look for N+1 queries' }],
    ])
  }
  if (content === 'Ask me') {
    return calls(res, [
      ['q', 'ask_user', { questions: [{ question: 'Where should the cart live?', type: 'compare', options: [{ label: 'Server session', pros: ['shared between devices', 'safe'], cons: ['one more table'] }, { label: 'Local storage', pros: ['instant', 'no backend'], cons: ['lost on logout'] }] }] }],
    ])
  }
  if (content === 'Let me try it') return calls(res, [['p', 'share_preview', { title: 'Shop, dev server', command: `node app.js ${appPort}`, port: appPort }]])
  if (last.role === 'tool' && last.tool_call_id === 'p') return say(res, 'Tap the card: it starts and opens on your tailnet.')
  if (last.role === 'tool') return say(res, 'Three sub-agents are on it; I will merge their reports.')
  return say(res, 'Done.')
})

const listen = (srv) => new Promise((ok) => srv.listen(0, '127.0.0.1', () => ok(srv.address().port)))
const freePort = async () => {
  const s = net.createServer()
  const p = await listen(s)
  await new Promise((ok) => s.close(ok))
  return p
}

async function addServer(page) {
  await page.click('.rail-right .rail-btn[title="AI assistant"]')
  await page.click('.ai-empty button:has-text("Add a model server")')
  await page.fill('.ai-servers input[name=url]', `127.0.0.1:${model.address().port}`)
  // The sub-agents stay busy: they must not hold the only place of the server.
  await page.fill('.ai-servers input[name=parallel]', '8')
  await page.click('.ai-servers form button.primary')
  await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
  await page.click('.ai-servers .modal-head button')
  await page.waitForSelector('[data-testid=model-pill]:has-text("qwen")')
}

async function ask(page, message, until) {
  await page.click('.ai-panel button[title="New conversation"]')
  await page.fill('.ai-composer textarea', message)
  await page.keyboard.press('Enter')
  await page.waitForSelector(until, { timeout: 20000 })
  await page.waitForTimeout(400)
}

;(async () => {
  await listen(model)
  appPort = await freePort()
  // The app of the preview: a small page of the shop.
  fs.writeFileSync(
    WS + '/demo/app.js',
    `require('http').createServer((q, s) => { s.setHeader('Content-Type', 'text/html; charset=utf-8'); s.end('<body style="margin:0;font:600 28px system-ui;background:#fff;color:#111;display:grid;place-items:center;height:100vh"><div style="text-align:center"><div style="font-size:64px;font-weight:900">SHOP</div><p>3 items · 42,00 €</p><button style="font:inherit;padding:14px 36px;border-radius:999px;border:0;background:#111;color:#fff">Checkout</button></div></body>') }).listen(process.argv[2], '127.0.0.1')\n`,
  )
  const desktop = await start({ orchestrator: true, scale: 3 })
  const page = desktop.page
  page.on('dialog', (d) => d.accept())
  try {
    await page.waitForSelector('.home .home-bar')
    await page.waitForTimeout(500)
    await shot(page, 'home')
    await openProject(page)
    // Tickets for the board and the Orchestrator.
    await page.click('.rail-left .rail-btn[title="Kanban"]')
    for (const [title, prio] of [
      ['Login with passkeys', 'normal'],
      ['Checkout page', 'high'],
      ['Order history', 'normal'],
      ['Dark theme', 'low'],
    ]) {
      await page.click('[data-testid=kanban-open-board]')
      await page.click('[data-testid=kanban-new]')
      await page.fill('[data-testid=kanban-title]', title)
      await page.click('[data-testid=kanban-create]')
      await page.waitForSelector(`[data-testid=ticket-view]:has-text("${title}")`)
      if (prio !== 'normal') await page.selectOption('[data-testid=ticket-priority]', prio).catch(() => {})
      if (title !== 'Dark theme') {
        await page.click('[data-testid=ticket-plan-edit]')
        await page.fill('.tk-md-input', '## Approach\n- Endpoint and page\n- Tests')
        await page.click('[data-testid=ticket-plan-save]')
        await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")')
      }
    }
    await page.click('[data-testid=kanban-open-board]')
    await page.waitForTimeout(500)
    await shot(page, 'kanban')

    await open(page, 'server.go')
    await page.click('.rail-left .rail-btn[title="Explorer"]').catch(() => {})
    await page.waitForTimeout(400)
    await shot(page, 'editor')

    await addServer(page)
    await ask(page, 'What do we work on today?', '[data-testid=ai-action]')
    await shot(page, 'orchestrator')
    await shot(page, 'orchestrator-panel', { clip: await page.$eval('.ai-panel', (e) => e.getBoundingClientRect().toJSON()) })
    await ask(page, 'Split the review', '[data-testid=ai-child] >> nth=2')
    await page.waitForTimeout(800)
    await shot(page, 'subagents-panel', { clip: await page.$eval('.ai-panel', (e) => e.getBoundingClientRect().toJSON()) })
    await ask(page, 'Ask me', '[data-testid=ai-ask-compare]')
    await shot(page, 'ask-panel', { clip: await page.$eval('.ai-panel', (e) => e.getBoundingClientRect().toJSON()) })
    await ask(page, 'Let me try it', '[data-testid=ai-preview]')
    const [tab] = await Promise.all([desktop.ctx.waitForEvent('page'), page.click('[data-testid=ai-preview-open]')])
    await tab.waitForFunction(() => document.body?.textContent.includes('SHOP'), null, { timeout: 30000 })
    await tab.setViewportSize({ width: 390, height: 844 })
    await tab.waitForTimeout(300)
    await tab.screenshot({ path: path.join(OUT, 'app.png') })
    await tab.close()
    await page.waitForSelector('[data-testid=ai-preview-url]')
    await shot(page, 'preview-panel', { clip: await page.$eval('.ai-panel', (e) => e.getBoundingClientRect().toJSON()) })
  } finally {
    await desktop.browser.close()
  }

  // The phone layout.
  const phone = await start({ mobile: true, viewport: { width: 390, height: 844 }, scale: 3 })
  try {
    const p = phone.page
    await openProject(p)
    await p.tap('[data-testid=mobile-rail] [data-id=explorer]')
    await p.tap('.tree-row:has-text("src")')
    await p.waitForTimeout(400)
    await shot(p, 'phone-explorer')
    await p.tap('.tree-row:has-text("server.go")')
    await p.waitForSelector('.phone-body > .center:not(.hidden) .tab.active:has-text("server.go")')
    await p.waitForTimeout(300)
    await shot(p, 'phone-editor')
    await p.tap('.pane.active .ed-content')
    await p.waitForSelector('[data-testid=ed-lock-hint]')
    await p.waitForTimeout(350)
    await shot(p, 'phone-hint')
    await p.tap('[data-testid=mobile-rail] [data-id=kanban]')
    await p.waitForTimeout(500)
    await shot(p, 'phone-kanban')
  } finally {
    await phone.browser.close()
  }
  model.close()
  console.log('  ok   shots in ' + OUT + ': ' + fs.readdirSync(OUT).join(', '))
})().catch((e) => {
  console.log('  FAIL ' + e.message.split('\n').slice(0, 3).join(' | '))
  process.exit(1)
})
