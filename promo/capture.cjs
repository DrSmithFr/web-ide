// Shots of the real UI for the promo video, on a fresh pod with a scripted model (run through
// e2e/run.sh by `make promo`). Whole screens, each one in two themes (High contrast and Day:
// the edit alternates them as its flashes): the editor and its completion, the board and the
// roadmap, the Orchestrator and its sub-agents, a question, the AI editing a file the user is
// typing in, a worktree, the cloud server for planning, an app preview, the phone layout.
// PNGs in promo/out/shots: <name>-hc.png and <name>-day.png.
const fs = require('fs')
const path = require('path')
const http = require('http')
const net = require('net')
const { execSync } = require('child_process')
const { start, openProject, open, WS } = require('../e2e/common.cjs')

const OUT = path.join(__dirname, 'out/shots')
fs.rmSync(OUT, { recursive: true, force: true })
fs.mkdirSync(OUT, { recursive: true })
const SCALE = 2

const SERVER_GO = `package main

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
`
fs.writeFileSync(WS + '/demo/src/server.go', SERVER_GO)
// A repository: the development of a ticket gets its worktree.
const env = { ...process.env, GIT_AUTHOR_NAME: 'dev', GIT_AUTHOR_EMAIL: 'dev@shop', GIT_COMMITTER_NAME: 'dev', GIT_COMMITTER_EMAIL: 'dev@shop' }
execSync('git init -q -b main && git add -A && git commit -q -m "The shop"', { cwd: WS + '/demo', env })

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
const DISCOUNT = `

// Discount of a coupon, in percent.
func (o Order) Discount(percent int) int {
	return o.Total() * percent / 100
}`

let appPort = 0
const model = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url.endsWith('/models')) return res.end(JSON.stringify({ data: [{ id: 'qwen3.8-27b', status: { value: 'loaded' } }, { id: 'claude-opus-5-5' }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 65536 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const sys = text(r.messages[0])
  const last = r.messages[r.messages.length - 1]
  const content = text(last)
  const after = (id) => last.role === 'tool' && last.tool_call_id === id
  if (sys.includes('# You are a sub-agent')) {
    await new Promise((ok) => setTimeout(ok, 120_000)) // busy for the shots
    return say(res, 'done')
  }
  if (sys.includes('You **develop** this ticket')) return say(res, 'Reading the plan of #5, then the theme variables…')
  if (content === 'Plan the shop')
    return calls(res, [
      ['k1', 'kanban_create', { title: 'Shop backend', priority: 'high', description: 'Orders, payments, stock.' }],
      ['k2', 'kanban_create', { title: 'Checkout page', priority: 'high', parent: 1 }],
      ['k3', 'kanban_create', { title: 'Order history', parent: 1 }],
      ['k4', 'kanban_create', { title: 'Coupons', depends_on: [1] }],
      ['k5', 'kanban_create', { title: 'Dark theme', priority: 'low' }],
    ])
  if (after('k5')) return say(res, 'Five tickets: the backend first, its steps after it, coupons once it is merged.')
  if (content === 'What do we work on today?') return calls(res, [['n', 'kanban_next', {}]])
  if (content.startsWith('Can start now')) return calls(res, [['c', 'action_card', { kind: 'start_dev', ticket: 5, label: 'Start #5 Dark theme', reason: 'Unblocked and small: done before lunch.' }]])
  if (after('c')) return say(res, '**#5 Dark theme** can start now. **#1 Shop backend** is next: high priority, its steps follow it.')
  if (content === 'Split the review')
    return calls(res, [
      ['s1', 'spawn_agent', { title: 'Review the API', task: 'Review src/server.go' }],
      ['s2', 'spawn_agent', { title: 'Check the tests', task: 'Run the tests' }],
      ['s3', 'spawn_agent', { title: 'Audit the SQL', task: 'Look for N+1 queries' }],
    ])
  if (after('s3')) return say(res, 'Three sub-agents are on it; I will merge their reports.')
  if (content === 'Ask me')
    return calls(res, [['q', 'ask_user', { questions: [{ question: 'Where should the cart live?', type: 'compare', options: [{ label: 'Server session', pros: ['shared between devices', 'safe'], cons: ['one more table'] }, { label: 'Local storage', pros: ['instant', 'no backend'], cons: ['lost on logout'] }] }] }]])
  if (content === 'Add a discount')
    return calls(res, [['e', 'edit_file', { path: 'src/server.go', old_string: '\treturn total\n}', new_string: '\treturn total\n}' + DISCOUNT }]])
  if (after('e')) return say(res, 'Added `Discount`, next to `Total`.')
  if (content === 'Let me try it') return calls(res, [['p', 'share_preview', { title: 'Shop, dev server', command: `node app.js ${appPort}`, port: appPort }]])
  if (after('p')) return say(res, 'Tap the card: it starts and opens on your tailnet.')
  return say(res, 'Done.')
})

const listen = (srv) => new Promise((ok) => srv.listen(0, '127.0.0.1', () => ok(srv.address().port)))
const freePort = async () => {
  const s = net.createServer()
  const p = await listen(s)
  await new Promise((ok) => s.close(ok))
  return p
}

// ---------- helpers ----------

async function setTheme(page, name) {
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+Alt+s')
  await page.waitForSelector('.theme-card')
  await page.click(`.theme-card:has-text("${name}")`)
  await page.keyboard.press('Escape')
  await page.waitForSelector('.theme-card', { state: 'detached' })
  await page.waitForTimeout(300)
}

/** The page in both themes; again() puts back what the theme switch closed (a popup). */
async function both(page, name, again) {
  await page.screenshot({ path: path.join(OUT, `${name}-hc.png`) })
  await setTheme(page, 'Day')
  if (again) await again()
  await page.screenshot({ path: path.join(OUT, `${name}-day.png`) })
  await setTheme(page, 'High contrast')
  if (again) await again()
}

async function newChat(page, message, until, build = false) {
  await page.click('.ai-panel button[title="New conversation"]')
  if (build) {
    await page.focus('.ai-composer textarea')
    await page.keyboard.press('Shift+Tab') // Orchestrator → Build
  }
  await page.fill('.ai-composer textarea', message)
  await page.keyboard.press('Enter')
  await page.waitForSelector(until, { timeout: 20000 })
  await page.waitForTimeout(500)
}

async function addServers(page) {
  await page.click('.rail-right .rail-btn[title="AI assistant"]')
  await page.click('.ai-empty button:has-text("Add a model server")')
  await page.fill('.ai-servers input[name=url]', `127.0.0.1:${model.address().port}`)
  await page.fill('.ai-servers input[name=name]', 'llama.cpp · this machine')
  await page.fill('.ai-servers input[name=parallel]', '8') // the sub-agents stay busy
  await page.click('.ai-servers form button.primary')
  await page.waitForSelector('.ai-server-row:has-text("this machine")')
  // A cloud provider for the planning and the sub-agents.
  await page.fill('.ai-servers input[name=url]', `127.0.0.1:${model.address().port}/v1`)
  await page.selectOption('.ai-servers select[name=kind]', 'openai')
  await page.fill('.ai-servers input[name=name]', 'Claude · cloud')
  await page.fill('.ai-servers input[name=apiKey]', 'sk-ant-promo')
  await page.check('.ai-servers input[name=children]')
  await page.fill('.ai-servers input[name=note]', 'the strongest: plans and reviews, paid')
  await page.click('.ai-servers form button.primary')
  await page.waitForSelector('.ai-server-row:has-text("Claude")')
  // The settings stay open over the theme switch.
  await both(page, 'cloud')
  await page.click('.ai-servers .modal-head button').catch(() => {})
  await page.waitForSelector('[data-testid=model-pill]:has-text("qwen")')
}

;(async () => {
  await listen(model)
  appPort = await freePort()
  fs.writeFileSync(
    WS + '/demo/app.js',
    `require('http').createServer((q, s) => { s.setHeader('Content-Type', 'text/html; charset=utf-8'); s.end('<body style="margin:0;font:600 28px system-ui;background:#fff;color:#111;display:grid;place-items:center;height:100vh"><div style="text-align:center"><div style="font-size:64px;font-weight:900">SHOP</div><p>3 items · 42,00 €</p><button style="font:inherit;padding:14px 36px;border-radius:999px;border:0;background:#111;color:#fff">Checkout</button></div></body>') }).listen(process.argv[2], '127.0.0.1')\n`,
  )
  const desktop = await start({ orchestrator: true, scale: SCALE })
  const page = desktop.page
  page.on('dialog', (d) => d.accept())
  try {
    await openProject(page)
    await setTheme(page, 'High contrast')
    await addServers(page)

    // Tickets made by the assistant: the board and the roadmap.
    await newChat(page, 'Plan the shop', '.ai-msg.assistant .md:has-text("Five tickets")', true)
    await both(page, 'plan-chat')
    // Plans for all but the last one: they are To do (the Orchestrator can start them).
    await page.click('.rail-left .rail-btn[title="Kanban"]')
    for (let id = 1; id <= 5; id++) {
      if (id === 4) continue
      await page.click('[data-testid=kanban-open-board]')
      await page.click('[data-testid=kanban-view-board]').catch(() => {})
      await page.click(`[data-testid=ticket-card-${id}]`)
      await page.waitForSelector(`[data-testid=ticket-view]:has-text("#${id}")`)
      await page.click('[data-testid=ticket-plan-edit]')
      await page.fill('.tk-md-input', '## Approach\n- The handler and its page\n- Tests')
      await page.click('[data-testid=ticket-plan-save]')
      await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")')
    }
    await page.click('[data-testid=kanban-open-board]')
    await page.click('[data-testid=kanban-view-board]').catch(() => {})
    await page.waitForTimeout(500)
    await both(page, 'kanban')
    await page.click('[data-testid=kanban-view-roadmap]')
    await page.waitForTimeout(500)
    await both(page, 'roadmap')

    // The editor and its completion (the language server).
    await open(page, 'server.go')
    await page.click('.rail-left .rail-btn[title="Explorer"]').catch(() => {})
    await page.waitForTimeout(800)
    const complete = async () => {
      await page.click('.pane.active .ed-content')
      await page.keyboard.press('Control+End')
      await page.keyboard.type('\nfunc receipt(o Order) int {\n\treturn o.')
      await page.waitForSelector('.completion-item', { timeout: 15000 }).catch(() => {})
      await page.waitForTimeout(400)
    }
    await complete()
    await both(page, 'editor', async () => {
      for (let i = 0; i < 6; i++) await page.keyboard.press('Control+z')
      await complete()
    })
    for (let i = 0; i < 6; i++) await page.keyboard.press('Control+z')
    await page.keyboard.press('Escape')

    // The user types while the assistant edits the same file: both changes stay.
    await page.evaluate(() => {
      const pre = document.querySelector('.pane.active .ed-content')
      pre.focus()
      for (const block of pre.children) {
        const node = block.firstChild
        const i = node?.data?.indexOf('total := 0') ?? -1
        if (i >= 0) {
          getSelection().setBaseAndExtent(node, i + 10, node, i + 10)
          break
        }
      }
    })
    await page.keyboard.type(' // the user types here, unsaved')
    await newChat(page, 'Add a discount', '[data-testid=ai-approval]', true)
    await page.click('[data-testid=ai-approval] button:has-text("Apply")')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Added")', { timeout: 15000 })
    // Both changes in sight: the line typed by the user and the function added by the AI.
    await page.$eval('.pane.active .ed-scroll', (e) => (e.scrollTop = 17 * parseFloat(getComputedStyle(e.querySelector('.ed-content')).lineHeight)))
    await page.waitForTimeout(600)
    await both(page, 'buffer')

    // The Orchestrator, a question, the sub-agents.
    await newChat(page, 'What do we work on today?', '[data-testid=ai-action]')
    await both(page, 'orchestrator')
    await newChat(page, 'Ask me', '[data-testid=ai-ask-compare]')
    await both(page, 'ask')
    await newChat(page, 'Split the review', '[data-testid=ai-child] >> nth=2')
    await page.waitForTimeout(800)
    await both(page, 'subagents')

    // The preview of the app, opened from its card.
    await newChat(page, 'Let me try it', '[data-testid=ai-preview]')
    const [tab] = await Promise.all([desktop.ctx.waitForEvent('page'), page.click('[data-testid=ai-preview-open]')])
    await tab.waitForFunction(() => document.body?.textContent.includes('SHOP'), null, { timeout: 30000 })
    await tab.setViewportSize({ width: 390, height: 844 })
    await tab.waitForTimeout(300)
    await tab.screenshot({ path: path.join(OUT, 'app.png') })
    await tab.close()
    await page.waitForSelector('[data-testid=ai-preview-url]')
    await both(page, 'preview')

    // A ticket started from the card of the Orchestrator: its worktree opens in a window.
    await newChat(page, 'What do we work on today?', '[data-testid=ai-action]')
    const [win] = await Promise.all([desktop.ctx.waitForEvent('page'), page.click('[data-testid=ai-action-run]')])
    await win.waitForSelector('[data-testid=worktree-banner]', { timeout: 30000 })
    await win.waitForSelector('.ai-msg.assistant .md:has-text("Reading the plan")', { timeout: 30000 }).catch(() => {})
    await win.waitForTimeout(800)
    await both(win, 'worktree')
  } catch (e) {
    await page.screenshot({ path: path.join(OUT, 'failure.png') }).catch(() => {})
    throw e
  } finally {
    await desktop.browser.close()
  }

  // The phone layout.
  const phone = await start({ mobile: true, viewport: { width: 390, height: 844 }, scale: 3 })
  try {
    const p = phone.page
    await openProject(p)
    await p.tap('[data-testid=mobile-rail] [data-id=explorer]')
    await p.waitForTimeout(400)
    if (!(await p.isVisible('.tree-row:has-text("server.go")'))) await p.tap('.tree-row:has-text("src")')
    await p.tap('.tree-row:has-text("server.go")')
    await p.waitForSelector('.phone-body > .center:not(.hidden) .tab.active:has-text("server.go")')
    await p.$eval('.pane.active .ed-scroll', (e) => ((e.scrollLeft = 0), (e.scrollTop = 0)))
    await p.waitForTimeout(300)
    await both(p, 'phone-editor')
    // The conversation of the Orchestrator, from the history.
    await p.tap('[data-testid=mobile-rail] [data-id=assistant]')
    await p.waitForTimeout(400)
    if (!(await p.isVisible('[data-testid=ai-sidebar]'))) await p.tap('.ai-panel button[title="Conversations of the project"]')
    await p.tap('.ai-chat-open:has-text("What do we work on today?") >> nth=0')
    await p.waitForSelector('[data-testid=ai-action]')
    await p.waitForTimeout(600)
    await both(p, 'phone-assistant')
    await p.tap('[data-testid=mobile-rail] [data-id=kanban]')
    await p.waitForTimeout(600)
    await both(p, 'phone-kanban')
  } finally {
    await phone.browser.close()
  }
  model.close()
  console.log('  ok   shots in ' + OUT + ': ' + fs.readdirSync(OUT).join(', '))
})().catch((e) => {
  console.log('  FAIL ' + e.message.split('\n').slice(0, 3).join(' | '))
  process.exit(1)
})
