// Scenes of the real UI for the site video, recorded on a fresh pod with a scripted model (run
// through e2e/run.sh by `make promo-site`). The model streams its text and holds its steps
// until the script releases them; the screencast of the browser keeps a frame at each change of
// the screen, with its time. Per scene: the frames, the clicks (for a drawn cursor) and the
// rects of the parts the camera aims at, in CSS pixels of the 1920×1080 viewport.
// Writes promo/out/site/<scene>/*.jpg and promo/out/site/manifest.json.
const fs = require('fs')
const path = require('path')
const http = require('http')
const net = require('net')
const { execSync } = require('child_process')
const { start, openProject, open, WS } = require('../../e2e/common.cjs')

const OUT = path.join(__dirname, '../out/site')
fs.rmSync(OUT, { recursive: true, force: true })
fs.mkdirSync(OUT, { recursive: true })
const SCALE = 2
const VIEW = { width: 1920, height: 1080 }

const SERVER_GO = `package main

import (
	"encoding/json"
	"log"
	"net/http"
)

// Item is a line of the cart.
type Item struct {
	SKU      string \`json:"sku"\`
	Quantity int    \`json:"quantity"\`
	Cents    int    \`json:"cents"\`
}

// Total of the items, in cents.
func Total(items []Item) int {
	total := 0
	for _, it := range items {
		total += it.Quantity * it.Cents
	}
	return total
}

func main() {
	http.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		var items []Item
		if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]int{"total": Total(items)})
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
`
const CART_GO = `package main

import "sync"

// Carts keeps the cart of each customer on the server: it follows them
// from one device to another and survives a logout.
type Carts struct {
	mu    sync.Mutex
	items map[string][]Item
}

func NewCarts() *Carts { return &Carts{items: map[string][]Item{}} }

// Get is the cart of a customer.
func (c *Carts) Get(customer string) []Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.items[customer]
}

// Put replaces the cart of a customer.
func (c *Carts) Put(customer string, items []Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[customer] = items
}
`
const CART_TEST = `package main

import "testing"

func TestCartFollowsTheCustomer(t *testing.T) {
	c := NewCarts()
	c.Put("ada", []Item{{SKU: "mug", Quantity: 2, Cents: 900}})
	if got := Total(c.Get("ada")); got != 1800 {
		t.Fatalf("total %d", got)
	}
}
`
fs.writeFileSync(WS + '/demo/src/server.go', SERVER_GO)
const env = { ...process.env, GIT_AUTHOR_NAME: 'dev', GIT_AUTHOR_EMAIL: 'dev@shop', GIT_COMMITTER_NAME: 'dev', GIT_COMMITTER_EMAIL: 'dev@shop' }

// ---------- the scripted model ----------

const wait = (ms) => new Promise((ok) => setTimeout(ok, ms))
const gates = {}
const gate = (k) => (gates[k] ??= (() => { let ok; const p = new Promise((r) => (ok = r)); p.open = ok; return p })())
const release = (k) => gate(k).open()

const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
const end = (res, reason = 'stop') => {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 2400, completion_tokens: 180 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))
/** Streams the text a few words at a time, then the tool calls (or the end of the answer). */
async function reply(res, s, list, ms = 45) {
  const words = (s ?? '').split(/(?<= )/)
  for (let i = 0; i < words.length; i += 2) {
    sse(res, { content: words.slice(i, i + 2).join('') })
    await wait(ms)
  }
  if (!list) return end(res)
  sse(res, { tool_calls: list.map(([id, name, args], index) => ({ index, id, type: 'function', function: { name, arguments: JSON.stringify(args) } })) })
  end(res, 'tool_calls')
}

let appPort = 0
const model = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url.endsWith('/models')) return res.end(JSON.stringify({ data: [{ id: 'qwen3.8-27b', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 65536 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const sys = text(r.messages[0])
  const last = r.messages[r.messages.length - 1]
  const content = text(last)
  const after = (id) => last.role === 'tool' && last.tool_call_id === id
  const tools = r.messages.filter((m) => m.role === 'tool')

  // The developer of the ticket, in its worktree: fast, many steps.
  if (sys.includes('You **develop** this ticket')) {
    await gate('dev')
    const goals = [...text(tools.find((m) => m.tool_call_id === 'd1') ?? {}).matchAll(/\(id (\d+)\)/g)].map((m) => Number(m[1]))
    const steps = [
      () => reply(res, 'On it: reading the plan of #6.', [['d1', 'kanban_get', { id: 6 }]], 20),
      () => reply(res, '', [['d2', 'read_file', { path: 'src/server.go' }]]),
      () => reply(res, 'The cart only lives in the browser. A store on the server first:', [['d3', 'write_file', { path: 'src/cart.go', content: CART_GO }]], 20),
      () => reply(res, '', [['o1', 'open_file', { path: 'src/cart.go', line: 5 }]]),
      () => reply(res, 'Then the routes, next to the checkout.', [['d4', 'edit_file', { path: 'src/server.go', old_string: '\tlog.Fatal(', new_string: '\tcarts := NewCarts()\n\thttp.HandleFunc("/cart", func(w http.ResponseWriter, r *http.Request) {\n\t\tjson.NewEncoder(w).Encode(carts.Get(r.URL.Query().Get("customer")))\n\t})\n\tlog.Fatal(' }]], 20),
      () => reply(res, 'A test of the store.', [['d5', 'write_file', { path: 'src/cart_test.go', content: CART_TEST }]], 20),
      () => reply(res, '', [['d6', 'run_command', { command: 'git status --short' }]]),
      () => reply(res, 'Two goals reached.', goals.slice(0, 2).map((id, i) => ['g' + i, 'kanban_goal', { action: 'check', id }]), 20),
      () => reply(res, '', [['d7', 'kanban_move', { status: 'review', test_summary: 'Add an item on a phone, open the shop on a laptop: the cart is there.' }]]),
      () => reply(res, 'A preview, to check it on two devices:', [['d8', 'share_preview', { title: 'Shop with the saved cart', command: `node app.js ${appPort}`, port: appPort }]], 20),
      () => reply(res, 'Done: the cart is **saved on the server** for each customer. Open the preview on your phone, then on your laptop: same cart.', null, 30),
    ]
    const n = new Set(tools.map((m) => m.tool_call_id.replace(/^g\d$/, 'g'))).size
    return steps[Math.min(n, steps.length - 1)]()
  }
  // The planner of the ticket: it fills the ticket view.
  if (sys.includes('Ticket linked to this conversation') && sys.includes('implementation plan')) {
    if (!tools.length) return reply(res, 'Reading the code of the cart first.', [['p1', 'read_file', { path: 'src/server.go' }]])
    if (after('p1')) return reply(res, 'No cart on the server yet: `Total` gets the items from the browser. The description first.', [['p2', 'kanban_update', { description: '**Context**: the cart lives in the local storage of the browser: it stays on one device and is lost on logout.\n\n**Need**: the cart of each customer is saved on the server and follows them from one device to another.' }]])
    if (after('p2'))
      return reply(res, 'Now the plan: a store, two routes, the page loads the cart from the server.', [
        ['p3', 'kanban_set_plan', {
          size: 'm',
          plan: '## Approach\n1. `Carts`: a store of the carts on the server, by customer\n2. Routes `GET /cart` and `PUT /cart` next to `/checkout`\n3. The page loads and saves its cart through them\n4. Tests of the store and of the routes',
          goals: [
            { title: 'The cart follows the customer', description: 'add an item on a phone, find it on a laptop' },
            { title: 'The cart survives a logout' },
            { title: 'Tests of the store and the routes pass' },
          ],
        }],
      ])
    return reply(res, 'Plan saved: **4 steps, 3 goals**, size M. Say go when you are ready.')
  }
  // The Orchestrator of the project.
  if (content.startsWith('Customers lose their cart'))
    return reply(res, 'Right: the cart lives in the local storage of the browser, so it stays on one device. One question before I write the ticket.', [
      ['q', 'ask_user', { questions: [{ question: 'Where should the cart live?', type: 'compare', options: [{ label: 'On the server', pros: ['follows the customer', 'survives a logout'], cons: ['one more store'] }, { label: 'Synced local storage', pros: ['instant', 'works offline'], cons: ['conflicts to merge'] }] }] }],
    ])
  if (after('q')) {
    await gate('create')
    return reply(res, 'On the server, then. Writing the ticket.', [['k', 'kanban_create', { title: 'Cart saved on the server', priority: 'high', description: 'The cart follows the customer from one device to another.' }]])
  }
  if (after('k')) return reply(res, 'Ticket **#6** is on the board. Open it and generate its plan: the planner keeps it small.')
  if (/^LET'?S GO/i.test(content)) return reply(res, 'Ready to go:', [['c', 'action_card', { kind: 'start_dev', ticket: 6, label: 'Start #6 Cart saved on the server', reason: 'Planned, unblocked, size M: a worktree and a developer agent.' }]], 25)
  if (after('c')) return reply(res, 'Tap **Start**: its branch, its worktree, its agent.', null, 25)
  // The board of the shop, made before the recording.
  if (content === 'Plan the shop')
    return reply(res, '', [
      ['s1', 'kanban_create', { title: 'Shop backend', priority: 'high', description: 'Orders, payments, stock.' }],
      ['s2', 'kanban_create', { title: 'Checkout page', priority: 'high', parent: 1 }],
      ['s3', 'kanban_create', { title: 'Order history', parent: 1 }],
      ['s4', 'kanban_create', { title: 'Coupons', depends_on: [1] }],
      ['s5', 'kanban_create', { title: 'Dark theme', priority: 'low' }],
    ])
  return reply(res, 'Five tickets: the backend first, its steps after it.')
})

const listen = (srv) => new Promise((ok) => srv.listen(0, '127.0.0.1', () => ok(srv.address().port)))
const freePort = async () => {
  const s = net.createServer()
  const p = await listen(s)
  await new Promise((ok) => s.close(ok))
  return p
}

// ---------- the recorder ----------

const manifest = { view: VIEW, scale: SCALE, scenes: {} }

/** Records a page: a frame at each change of the screen (screencast), the clicks and the marks. */
async function record(page, name) {
  const dir = path.join(OUT, name)
  fs.mkdirSync(dir)
  const scene = { frames: [], events: [] }
  manifest.scenes[name] = scene
  const cdp = await page.context().newCDPSession(page)
  const t0 = Date.now()
  const now = () => (Date.now() - t0) / 1000
  cdp.on('Page.screencastFrame', ({ data, sessionId }) => {
    const file = `${name}/${String(scene.frames.length).padStart(4, '0')}.jpg`
    fs.writeFileSync(path.join(OUT, file), Buffer.from(data, 'base64'))
    scene.frames.push({ f: file, t: now() })
    cdp.send('Page.screencastFrameAck', { sessionId }).catch(() => {})
  })
  await cdp.send('Page.startScreencast', { format: 'jpeg', quality: 90, maxWidth: VIEW.width * SCALE, maxHeight: VIEW.height * SCALE })
  const box = async (sel) => {
    const b = await page.locator(sel).first().boundingBox()
    return b && [b.x, b.y, b.width, b.height].map((v) => Math.round(v))
  }
  return {
    now,
    /** Rect of an element, named for the camera. */
    mark: async (key, sel) => scene.events.push({ t: now(), kind: 'mark', key, rect: await box(sel) }),
    /** A click, drawn by the timeline: the cursor moves there first. */
    click: async (sel, pause = 500) => {
      await page.waitForSelector(sel)
      const r = await box(sel)
      scene.events.push({ t: now(), kind: 'click', x: r[0] + r[2] / 2, y: r[1] + r[3] / 2 })
      await page.click(sel)
      await page.waitForTimeout(pause)
    },
    /** Typed in a field, a key at a time. */
    type: async (sel, s, delay = 55) => {
      await page.locator(sel).first().focus()
      await page.keyboard.type(s, { delay })
    },
    /** A moment without change of the screen still takes its time in the scene. */
    hold: (ms) => page.waitForTimeout(ms),
    stop: async () => {
      // The last state lasts until the end of the scene.
      await page.waitForTimeout(300)
      await cdp.send('Page.stopScreencast')
      scene.dur = now()
      await cdp.detach()
    },
  }
}

// ---------- helpers ----------

async function setTheme(page, name) {
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+Alt+s')
  await page.waitForSelector('.theme-card')
  await page.click(`.theme-card:has-text("${name}")`)
  await page.keyboard.press('Escape')
  await page.waitForSelector('.theme-card', { state: 'detached' })
}

async function addServer(page) {
  await page.click('.rail-right .rail-btn[title="AI assistant"]')
  await page.click('.ai-empty button:has-text("Add a model server")')
  await page.fill('.ai-servers input[name=url]', `127.0.0.1:${model.address().port}`)
  await page.fill('.ai-servers input[name=name]', 'llama.cpp · this machine')
  await page.click('.ai-servers form button.primary')
  await page.waitForSelector('.ai-server-row:has-text("this machine")')
  await page.click('.ai-servers .modal-head button').catch(() => {})
  await page.waitForSelector('[data-testid=model-pill]:has-text("qwen")')
}

/** A wider assistant: its cards read better once the camera zooms on them. */
async function widen(page, width = 500) {
  const r = await page.locator('.zone-right').evaluate((e) => e.previousElementSibling.getBoundingClientRect().toJSON())
  const panel = await page.locator('.zone-right').boundingBox()
  await page.mouse.move(r.x + r.width / 2, r.y + 300)
  await page.mouse.down()
  await page.mouse.move(r.x + r.width / 2 - (width - panel.width), r.y + 300, { steps: 8 })
  await page.mouse.up()
}

/** The board of the shop: five tickets, four of them planned. */
async function seedBoard(page) {
  await page.fill('.ai-composer textarea', 'Plan the shop')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.ai-msg.assistant .md:has-text("Five tickets")', { timeout: 20000 })
  await page.click('.rail-left .rail-btn[title="Kanban"]')
  for (const id of [1, 2, 3, 5]) {
    await page.click('[data-testid=kanban-open-board]')
    await page.click('[data-testid=kanban-view-board]').catch(() => {})
    await page.click(`[data-testid=ticket-card-${id}]`)
    await page.waitForSelector(`[data-testid=ticket-view]:has-text("#${id}")`)
    await page.click('[data-testid=ticket-plan-edit]')
    await page.fill('.tk-md-input', '## Approach\n- The handler and its page\n- Tests')
    await page.click('[data-testid=ticket-plan-save]')
    await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")')
  }
  // Only the board stays among the tabs of the tickets.
  for (const id of [1, 2, 3, 5]) await page.click(`.pane.active .tab:has-text("#${id}") .tab-close`).catch(() => {})
}

;(async () => {
  await listen(model)
  appPort = await freePort()
  fs.writeFileSync(
    WS + '/demo/app.js',
    `require('http').createServer((q, s) => { s.setHeader('Content-Type', 'text/html; charset=utf-8'); s.end('<body style="margin:0;font:500 22px system-ui;background:#f6f5f2;color:#111;display:grid;place-items:center;height:100vh"><div style="background:#fff;border-radius:24px;padding:48px 64px;box-shadow:0 20px 60px #0002;min-width:520px"><div style="font-size:44px;font-weight:900;letter-spacing:-1px">SHOP</div><p style="color:#0a7d38;font-weight:700">● Cart saved on the server</p><div style="display:flex;justify-content:space-between;border-top:1px solid #eee;padding:14px 0"><span>Mug × 2</span><b>18,00 €</b></div><div style="display:flex;justify-content:space-between;border-top:1px solid #eee;padding:14px 0"><span>Coffee 1 kg</span><b>24,00 €</b></div><div style="display:flex;justify-content:space-between;border-top:2px solid #111;padding:14px 0;font-weight:800"><span>Total</span><span>42,00 €</span></div><button style="font:inherit;font-weight:700;width:100%;margin-top:12px;padding:16px;border-radius:999px;border:0;background:#111;color:#fff">Checkout</button></div></body>') }).listen(process.argv[2], '127.0.0.1')\n`,
  )
  execSync('git init -q -b main && git add -A && git commit -q -m "The shop"', { cwd: WS + '/demo', env })

  const t = await start({ orchestrator: true, scale: SCALE, viewport: VIEW })
  const page = t.page
  page.on('dialog', (d) => d.accept())
  let win
  try {
    // ---------- setup, not recorded ----------
    await openProject(page)
    await addServer(page)
    await widen(page)
    await seedBoard(page)
    await open(page, 'server.go')
    await page.click('.rail-left .rail-btn[title="Explorer"]')
    await page.waitForTimeout(400)
    if (!(await page.isVisible('.tree-row:has-text("server.go")'))) await page.click('.tree-row:has-text("src")')
    await page.click('.ai-panel button[title="New conversation"]')
    await page.waitForTimeout(800)

    // ---------- 1. the idea: a question, the ticket on the board ----------
    let r = await record(page, 'idea')
    await r.mark('wide', 'body')
    await r.hold(1200)
    await r.mark('chat', '.ai-panel')
    await r.click('.ai-composer textarea', 200)
    await r.type('.ai-composer textarea', "Customers lose their cart when they switch devices. Let's fix that.")
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-ask-compare]', { timeout: 20000 })
    await r.hold(1500)
    await r.click('.ai-ask-compare .ai-ask-option:has-text("On the server")', 600)
    if (await page.isVisible('[data-testid=ai-ask-send]')) await r.click('[data-testid=ai-ask-send]')
    await r.click('.pane.active .tab:has-text("Kanban")', 300)
    await r.click('[data-testid=kanban-view-board]', 600)
    await r.mark('board', '[data-testid=kanban-board]')
    release('create')
    await page.waitForSelector('[data-testid=ticket-card-6]', { timeout: 20000 })
    await r.mark('card', '[data-testid=ticket-card-6]')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("on the board")', { timeout: 20000 })
    await r.hold(1500)
    await r.stop()

    // ---------- 2. the plan: the ticket fills ----------
    r = await record(page, 'plan')
    await r.click('[data-testid=ticket-card-6]', 600)
    await r.mark('ticket', '[data-testid=ticket-view]')
    await r.click('[data-testid=ticket-plan-generate]', 300)
    await r.mark('chat', '.ai-panel')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("Plan saved")', { timeout: 30000 })
    await page.waitForSelector('[data-testid=ticket-goal] >> nth=2')
    await r.mark('ticket', '[data-testid=ticket-view]')
    await r.hold(1500)
    await r.stop()

    // Between the scenes: the roadmap in the center, the conversation of the Orchestrator back.
    await page.click('.pane.active .tab:has-text("Kanban")')
    await page.click('[data-testid=kanban-view-roadmap]')
    if (!(await page.isVisible('[data-testid=ai-sidebar]'))) await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.click('.ai-chat-open:has-text("Customers lose") >> nth=0')
    await page.waitForSelector('.ai-msg.assistant .md:has-text("on the board")')
    if (await page.isVisible('[data-testid=ai-sidebar]')) await page.click('.ai-panel button[title="Conversations of the project"]')
    await page.waitForSelector('[data-testid=ai-sidebar]', { state: 'detached' })
    await page.waitForTimeout(600)

    // ---------- 3. LET'S GOOOO: the development starts ----------
    r = await record(page, 'go')
    await r.mark('chat', '.ai-panel')
    await r.mark('roadmap', '[data-testid=roadmap]')
    await r.mark('block', '[data-testid=roadmap-block-6]')
    await r.click('.ai-composer textarea', 200)
    await r.type('.ai-composer textarea', "LET'S GOOOO", 90)
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ai-action]', { timeout: 20000 })
    await r.hold(900)
    const opened = t.ctx.waitForEvent('page')
    await r.click('[data-testid=ai-action-run]', 200)
    win = await opened
    win.on('dialog', (d) => d.accept())
    await page.bringToFront()
    await page.waitForSelector('[data-testid=roadmap-block-6].active, [data-testid=roadmap-block-6].st-in_progress', { timeout: 20000 }).catch(() => {})
    await r.hold(1200)
    await r.mark('block', '[data-testid=roadmap-block-6]')
    // The roadmap: a click on the block opens the ticket, in progress.
    await r.click('[data-testid=roadmap-block-6]', 400)
    await r.mark('ticket', '[data-testid=ticket-view]')
    await r.hold(1500)
    await r.stop()

    // ---------- 4. the developer agent, in the window of its worktree ----------
    await win.setViewportSize(VIEW)
    await win.waitForSelector('[data-testid=worktree-banner]', { timeout: 30000 })
    await widen(win)
    await win.waitForTimeout(800)
    await win.bringToFront()
    r = await record(win, 'dev')
    await r.mark('chat', '.ai-panel')
    await r.mark('wide', 'body')
    release('dev')
    // Changes and commands are approved as they come.
    const approve = setInterval(() => win.click('[data-testid=ai-approval] button.primary', { timeout: 200 }).catch(() => {}), 150)
    await win.waitForSelector('[data-testid=ai-preview]', { timeout: 60000 })
    await win.waitForSelector('.ai-msg.assistant .md:has-text("same cart")', { timeout: 30000 })
    clearInterval(approve)
    await r.hold(1200)
    await r.mark('preview', '[data-testid=ai-preview]')
    await r.mark('chat', '.ai-panel')
    const tabbed = t.ctx.waitForEvent('page')
    await r.click('[data-testid=ai-preview-open]', 200)
    const app = await tabbed
    await app.setViewportSize(VIEW)
    await app.waitForFunction(() => document.body?.textContent.includes('SHOP'), null, { timeout: 30000 })
    await app.waitForTimeout(400)
    await app.screenshot({ path: path.join(OUT, 'app.jpg'), type: 'jpeg', quality: 92 })
    await r.hold(300)
    await r.stop()
  } catch (e) {
    await page.screenshot({ path: path.join(OUT, 'failure.png') }).catch(() => {})
    if (win) await win.screenshot({ path: path.join(OUT, 'failure-win.png') }).catch(() => {})
    throw e
  } finally {
    await t.browser.close()
  }
  fs.writeFileSync(path.join(OUT, 'manifest.json'), JSON.stringify(manifest, null, 1))
  model.close()
  for (const [k, s] of Object.entries(manifest.scenes)) console.log(`  ok   ${k}: ${s.frames.length} frames, ${s.dur.toFixed(1)} s`)
})().catch((e) => {
  console.log('  FAIL ' + e.message.split('\n').slice(0, 3).join(' | '))
  process.exit(1)
})
