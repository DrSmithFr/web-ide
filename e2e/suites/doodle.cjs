// AI assistant, doodles: paperclip menu, modal with the conversation, pen, marker under the
// pen, pixel and object eraser (and the eraser end of a stylus), stylus pressure, undo / redo,
// frame presets, attach (chip opened again), PNG and description sent to the model,
// Ctrl+Shift+D and sending from the composer of the modal.
const http = require('http')
const fs = require('fs')
const { run, openProject, assert, OUT, WS } = require('../common.cjs')

const requests = []
const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const text = (m) => (typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => p.text ?? '').join(' '))

const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 }, modalities: { vision: true } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  requests.push(r)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const lastUser = [...r.messages].reverse().find((m) => m.role === 'user')
  const last = r.messages[r.messages.length - 1]
  if (text(lastUser).startsWith('Make a ticket')) {
    if (last.role === 'tool') sse(res, { content: 'Ticket made.' })
    else sse(res, { tool_calls: [{ index: 0, id: 'k1', type: 'function', function: { name: 'kanban_create', arguments: JSON.stringify({ title: 'Login page', description: 'From the doodles.' }) } }] })
    return end(res, last.role === 'tool' ? 'stop' : 'tool_calls')
  }
  const firstUser = r.messages.find((m) => m.role === 'user')
  if (text(firstUser).startsWith('Back:')) {
    // The image sent by the user is page 1: annotated on a copy; an SVG of the model as a page;
    // an image that is neither an SVG nor "screen" (refused); a capture of the screen (waits).
    if (!r.messages.some((m) => m.role === 'tool')) {
      const call = (i, id, name, args) => ({ index: i, id, type: 'function', function: { name, arguments: JSON.stringify({ title: id, ...args }) } })
      sse(res, {
        tool_calls: [
          call(0, 'b1', 'board_draw_doodle', { clone: 1, elements: [{ type: 'ellipse', x: 1, y: 1, w: 2, h: 1, color: 'red' }] }),
          call(1, 'b2', 'board_draw_image', { image: '<svg viewBox="0 0 300 200"><rect x="10" y="10" width="280" height="180" fill="#9cf"/><circle cx="150" cy="100" r="60" fill="#f80"/></svg>' }),
          call(2, 'b3', 'board_draw_image', { image: 'img/logo.png' }),
          call(3, 'b4', 'board_draw_image', { image: 'notes.txt' }),
          call(4, 'b5', 'board_draw_image', { image: '../outside.png' }),
          call(5, 'b7', 'board_draw_image', { image: 'img/anim.gif' }),
          call(6, 'b6', 'board_draw_image', { image: 'screen' }),
        ],
      })
      return end(res, 'tool_calls')
    }
    sse(res, { content: 'Backgrounds done.' })
    return end(res)
  }
  if (text(firstUser).startsWith('Draw:')) {
    // board_draw: a page, then a copy of it with a red stroke and an invalid arrow, then the end.
    const tools = r.messages.filter((m) => m.role === 'tool').length
    const call = (id, args) => ({ index: 0, id, type: 'function', function: { name: 'board_draw_doodle', arguments: JSON.stringify(args) } })
    if (tools === 0) {
      sse(res, {
        tool_calls: [
          call('d1', {
            title: 'Login flow',
            size: '16:9',
            elements: [
              { type: 'layout', x: 40, y: 40, w: 500, h: 600, root: { split: { dir: 'rows', sizes: [1, 4], children: [{ name: 'header' }, { name: 'form' }] } } },
              { type: 'rect', id: 'btn', x: 700, y: 100, w: 240, h: 90, label: 'Sign in', fill: true, color: 'blue' },
              { type: 'ellipse', id: 'ok', x: 760, y: 450, w: 200, h: 120, label: 'Home' },
              { type: 'arrow', from: 'btn', to: 'ok' },
              { type: 'text', x: 600, y: 650, text: 'Happy path', size: 's' },
              { type: 'stroke', points: [[60, 700], [300, 690], [520, 700]], color: 'red' },
            ],
          }),
        ],
      })
      return end(res, 'tool_calls')
    }
    if (tools === 1) {
      sse(res, {
        tool_calls: [
          call('d2', { title: 'Login flow, fixed', clone: 1, elements: [{ type: 'stroke', points: [[700, 80], [960, 80]], color: 'red' }] }),
          { ...call('d3', { title: 'Bad', elements: [{ type: 'arrow', from: [0, 0], to: 'nope' }] }), index: 1 },
        ],
      })
      return end(res, 'tool_calls')
    }
    sse(res, { content: 'Drawn.' })
    return end(res)
  }
  sse(res, { content: `Got ${text(lastUser).split('\n')[0].slice(0, 40)}` })
  end(res)
})

// An 8×6 animated GIF, transparent around a square.
const GIF = Buffer.from('R0lGODlhCAAGAIEAAAAAAP8AAAAAAAAAACH/C05FVFNDQVBFMi4wAwEAAAAh+QQJFAAAACwAAAAACAAGAAAIEQABCBxIsCDBAAEMIjTI0GBAACH5BAkUAAAALAMAAgACAAIAgQAAAAAA/wAAAAAAAAgGAAMIDBAQADs=', 'base64')
// A 4×3 red PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAIAAAA7ljmRAAAAEElEQVR4nGP4z8AARww4OQD1MQv1NXv7ggAAAABJRU5ErkJggg==', 'base64')

const count = (page) => page.$$eval('[data-testid=dd-canvas] svg g > *', (e) => e.length)
const paths = (page) => page.$$eval('[data-testid=dd-canvas] svg g path', (ps) => ps.map((p) => ({ fill: p.getAttribute('fill'), opacity: p.getAttribute('stroke-opacity') })))

/** Mouse stroke from (x0, y0) to (x1, y1), in fractions of the canvas. */
async function stroke(page, x0, y0, x1, y1) {
  const b = await page.locator('[data-testid=dd-canvas]').boundingBox()
  await page.mouse.move(b.x + b.width * x0, b.y + b.height * y0)
  await page.mouse.down()
  for (let i = 1; i <= 12; i++) await page.mouse.move(b.x + b.width * (x0 + ((x1 - x0) * i) / 12), b.y + b.height * (y0 + ((y1 - y0) * i) / 12))
  await page.mouse.up()
}

/** Stylus stroke through synthetic pointer events (pressure, eraser button). */
function penStroke(page, x0, y0, x1, y1, opts) {
  return page.evaluate(
    ({ x0, y0, x1, y1, opts }) => {
      const el = document.querySelector('[data-testid=dd-canvas]')
      const b = el.getBoundingClientRect()
      const at = (f) => ({ clientX: b.left + b.width * (x0 + (x1 - x0) * f), clientY: b.top + b.height * (y0 + (y1 - y0) * f) })
      const base = { pointerId: 7, pointerType: 'pen', bubbles: true, isPrimary: true }
      el.dispatchEvent(new PointerEvent('pointerdown', { ...base, ...at(0), button: opts.eraser ? 5 : 0, buttons: opts.eraser ? 32 : 1, pressure: 0.2 }))
      for (let i = 1; i <= 12; i++) el.dispatchEvent(new PointerEvent('pointermove', { ...base, ...at(i / 12), buttons: opts.eraser ? 32 : 1, pressure: 0.2 + (0.7 * i) / 12 }))
      el.dispatchEvent(new PointerEvent('pointerup', { ...base, ...at(1), button: opts.eraser ? 5 : 0, buttons: 0 }))
    },
    { x0, y0, x1, y1, opts },
  )
}

run(async ({ page }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    // The paperclip opens a menu: a file or a doodle.
    await page.click('[data-testid=ai-attach]')
    const items = await page.$$eval('.ctx-menu .ctx-item .ctx-label', (e) => e.map((x) => x.textContent.trim()))
    assert(items.join('|') === 'File…|Doodle…|Screenshot…', `paperclip menu: ${items.join(', ')}`)
    await page.click('.ctx-menu .ctx-item:has-text("Doodle…")')
    await page.waitForSelector('[data-testid=doodle]')
    assert(await page.isVisible('[data-testid=doodle] .ai-composer .ed-content'), 'the doodle modal shows the conversation and its composer')
    assert((await page.textContent('[data-testid=doodle] .dd-head h2')) === 'Doodle 1', 'first doodle named Doodle 1')

    // Pen, then marker: the marker is drawn under the pen.
    await page.click('[data-testid=dd-pen]')
    await page.click('[data-testid=dd-color-red]')
    await stroke(page, 0.3, 0.4, 0.6, 0.4)
    await page.keyboard.press('m')
    assert(await page.isVisible('[data-testid=dd-marker].on'), 'M picks the marker')
    await stroke(page, 0.3, 0.5, 0.6, 0.5)
    let ps = await paths(page)
    assert(ps.length === 2 && ps[0].opacity === '0.45' && ps[1].opacity === '1', `marker under the pen: ${JSON.stringify(ps)}`)

    // Undo, redo.
    await page.keyboard.press('Control+z')
    assert((await paths(page)).length === 1, 'Ctrl+Z removes the last stroke')
    await page.click('[data-testid=dd-redo]')
    assert((await paths(page)).length === 2, 'redo brings it back')

    // Pixel eraser across the pen stroke: it splits in two.
    await page.keyboard.press('e')
    await page.click('[data-testid=dd-eraser-pixel]')
    await stroke(page, 0.45, 0.35, 0.45, 0.44)
    ps = await paths(page)
    assert(ps.length === 3, `pixel eraser cuts the stroke in two (${ps.length} paths)`)
    await page.keyboard.press('Control+z')
    assert((await paths(page)).length === 2, 'one erasing gesture is one undo step')
    // Object eraser: the whole marker stroke goes.
    await page.click('[data-testid=dd-eraser-object]')
    await stroke(page, 0.35, 0.47, 0.35, 0.53)
    ps = await paths(page)
    assert(ps.length === 1 && ps[0].opacity === '1', 'object eraser removes the whole marker stroke')

    // Stylus: pressure gives a filled outline; its eraser end erases without changing tool.
    await page.keyboard.press('p')
    await penStroke(page, 0.3, 0.6, 0.6, 0.65, {})
    ps = await paths(page)
    assert(ps.length === 2 && ps[1].fill !== 'none', 'stylus pressure: stroke of varying width')
    await penStroke(page, 0.45, 0.58, 0.45, 0.68, { eraser: true })
    assert((await paths(page)).length === 1, 'the eraser end of the stylus erases')
    assert(await page.isVisible('[data-testid=dd-pen].on'), 'the tool stays the pen')

    // Frame presets.
    await page.selectOption('[data-testid=dd-preset]', 'square')
    await page.waitForSelector('.dd-frame-label:has-text("Square · 800×800")')
    assert(true, 'frame preset changes the frame')

    // Escape asks before discarding; dismissed, the doodle stays.
    await page.focus('[data-testid=dd-canvas]')
    page.once('dialog', (d) => d.dismiss())
    await page.keyboard.press('Escape')
    await page.waitForTimeout(200)
    assert(await page.isVisible('[data-testid=doodle]'), 'closing a drawn doodle asks first')

    // Attach: a chip in the draft, which opens the doodle again.
    await page.click('[data-testid=dd-attach]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    await page.waitForSelector('.ai-composer .ai-att[data-kind=doodle] img')
    assert(true, 'attached: doodle chip with a thumbnail')
    await page.click('.ai-composer .ai-att[data-kind=doodle]')
    await page.waitForSelector('[data-testid=doodle]')
    assert((await paths(page)).length === 1, 'the chip opens the doodle again')
    await page.click('[data-testid=dd-close]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    assert(true, 'closing an unchanged doodle asks nothing')

    // Sent: image and description.
    await page.fill('.ai-composer .ed-content', 'Here is my sketch')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Here is my sketch")', { timeout: 15000 })
    let user = requests[requests.length - 1].messages.find((m) => m.role === 'user')
    const kinds = user.content.map((p) => p.type)
    const desc = text(user)
    assert(kinds.includes('image_url') && user.content.find((p) => p.type === 'image_url').image_url.url.startsWith('data:image/png'), 'PNG sent to a model reading images')
    assert(desc.includes('Doodle "Doodle 1"') && desc.includes('Frame 800×800 (Square)') && desc.includes('Free pen strokes (hand drawn, shapes approximate): 1') && desc.includes('- red'), `description sent: ${desc}`)
    assert(text(requests[requests.length - 1].messages[0]).includes('A doodle joined by the user comes as an image followed by its text description'), 'the system prompt explains the doodles')
    assert(desc.includes('Rely on this description'), 'the description says how to read it')

    // In the thread: preview, description, enlarge, reuse.
    const card = '.ai-panel .ai-msg.user [data-testid=ai-doodle]'
    await page.waitForSelector(card)
    await page.click(`${card} summary`)
    assert((await page.textContent(`${card} pre`)).includes('Doodle "Doodle 1"'), 'the description sent can be read in the thread')
    await page.click(`${card} .ai-doodle-img`)
    await page.waitForSelector('[data-testid=diagram-viewer]')
    assert(true, 'the preview enlarges')
    await page.keyboard.press('Escape')
    await page.waitForSelector('[data-testid=diagram-viewer]', { state: 'detached' })
    await page.click(`${card} [data-testid=ai-doodle-reuse]`)
    await page.waitForSelector('[data-testid=doodle]')
    assert((await paths(page)).length === 1 && (await page.textContent('[data-testid=doodle] .dd-head h2')) === 'Doodle 1', 'Reuse opens an editable copy of the doodle')
    await page.click('[data-testid=dd-close]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })

    // Ctrl+Shift+D, then sending from the composer of the modal attaches the doodle.
    await page.click('.ai-composer .ed-content')
    await page.keyboard.press('Control+Shift+D')
    await page.waitForSelector('[data-testid=doodle]')
    assert((await page.textContent('[data-testid=doodle] .dd-head h2')) === 'Doodle 1', 'Ctrl+Shift+D opens a new doodle')
    await stroke(page, 0.3, 0.3, 0.5, 0.5)
    await page.fill('[data-testid=doodle] .ai-composer .ed-content', 'Second sketch')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Second sketch")', { timeout: 15000 })
    user = [...requests[requests.length - 1].messages].reverse().find((m) => m.role === 'user')
    assert(user.content.some((p) => p.type === 'image_url') && text(user).includes('Doodle "Doodle 1"'), 'sent from the modal with the doodle')
    assert(!(await page.$('.ai-composer .ai-att')), 'the draft is empty after sending')

    // The board: the doodles of the conversation as read-only pages. The side panel is
    // narrow: the board shows instead of the conversation.
    await page.click('[data-testid=ai-board-toggle]')
    await page.waitForSelector('[data-testid=bd-board] [data-testid=bd-view]')
    assert((await page.$$('[data-testid=bd-thumb]')).length === 2 && (await page.textContent('[data-testid=bd-title]')).startsWith('Page 2'), 'board: two pages, the last one shown')
    assert(!(await page.isVisible('.ai-panel .ai-main')), 'narrow assistant: the board replaces the conversation')
    await page.click('[data-testid=bd-thumb] >> nth=0')
    assert((await page.textContent('[data-testid=bd-title]')) === 'Page 1 · Doodle 1', 'a thumbnail shows its page')
    const page1 = await page.$$eval('[data-testid=bd-view] path', (p) => p.length)
    const vb = () => page.getAttribute('[data-testid=bd-view]', 'viewBox')
    const fitted = await vb()
    await page.hover('[data-testid=bd-view]')
    await page.mouse.wheel(0, -400)
    assert((await vb()) !== fitted, 'the wheel zooms the page')
    await page.click('[data-testid=bd-fit]')
    assert((await vb()) === fitted, 'Fit shows the whole page again')
    // Reuse: an editable copy, sent as a new page; the page itself stays as it was.
    await page.click('[data-testid=bd-reuse]')
    await page.waitForSelector('[data-testid=doodle]')
    await stroke(page, 0.6, 0.6, 0.8, 0.7)
    await page.fill('[data-testid=doodle] .ai-composer .ed-content', 'Third sketch')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    await page.click('[data-testid=ai-board-toggle]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Third sketch")', { timeout: 15000 })
    await page.click('[data-testid=ai-board-toggle]')
    await page.waitForSelector('[data-testid=bd-board]')
    assert((await page.$$('[data-testid=bd-thumb]')).length === 3 && (await page.textContent('[data-testid=bd-title]')).startsWith('Page 3'), 'the copy sent is a new page, shown')
    await page.click('[data-testid=bd-thumb] >> nth=0')
    assert((await page.$$eval('[data-testid=bd-view] path', (p) => p.length)) === page1, 'the reused page is unchanged')
    await page.screenshot({ path: OUT + '/board-narrow.png' })
    // Back to the conversation; a doodle card shows its page on the board.
    await page.click('[data-testid=ai-board-toggle]')
    await page.waitForSelector('.ai-panel .ai-main', { state: 'visible' })
    await page.click('.ai-panel .ai-msg.user [data-testid=ai-doodle-board] >> nth=0')
    await page.waitForSelector('[data-testid=bd-title]:has-text("Page 1")')
    assert(true, 'Show on the board opens the page of the card')
    await page.click('[data-testid=ai-board-toggle]')
    // A wide detached window: the board is a column next to the conversation, resizable.
    const main = page.url()
    await page.goto(new URL(new URL(main).pathname.replace(/\/$/, '') + '/tool/assistant', main).href)
    await page.waitForSelector('.ai-panel.detached')
    await page.waitForSelector('.ai-panel .ai-msg.user [data-testid=ai-doodle]', { timeout: 10000 })
    await page.click('[data-testid=ai-board-toggle]')
    await page.waitForSelector('[data-testid=ai-board-split]')
    assert((await page.isVisible('.ai-panel .ai-main')) && (await page.$$('[data-testid=bd-thumb]')).length === 3, 'wide window: conversation and board side by side')
    const col = () => page.$eval('.ai-board-col', (e) => e.getBoundingClientRect().width)
    const colBefore = await col()
    const sb = await page.$eval('[data-testid=ai-board-split]', (e) => { const r = e.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 } })
    await page.mouse.move(sb.x, sb.y)
    await page.mouse.down()
    await page.mouse.move(sb.x - 150, sb.y, { steps: 5 })
    await page.mouse.up()
    assert((await col()) > colBefore + 100, `the splitter widens the board (${Math.round(colBefore)} → ${Math.round(await col())})`)
    await page.screenshot({ path: OUT + '/board-wide.png' })
    // Another conversation: its own board, empty.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.waitForSelector('[data-testid=bd-empty]')
    assert(true, 'a new conversation has an empty board')
    await page.click('[data-testid=ai-board-toggle]')
    await page.goto(main)
    await page.waitForSelector('.ai-panel')

    // Shapes, text, selection.
    await page.click('.ai-composer .ed-content')
    await page.keyboard.press('Control+Shift+D')
    await page.waitForSelector('[data-testid=doodle]')
    await page.keyboard.press('r')
    await stroke(page, 0.2, 0.2, 0.4, 0.45)
    await page.keyboard.press('o')
    await stroke(page, 0.6, 0.2, 0.8, 0.45)
    await page.keyboard.press('a')
    await stroke(page, 0.38, 0.3, 0.62, 0.3)
    await page.keyboard.press('t')
    const cb = await page.locator('[data-testid=dd-canvas]').boundingBox()
    await page.mouse.click(cb.x + cb.width * 0.24, cb.y + cb.height * 0.24)
    await page.waitForSelector('[data-testid=dd-text-edit]')
    await page.keyboard.type('Login')
    await page.keyboard.press('Escape')
    await page.waitForSelector('[data-testid=dd-text-edit]', { state: 'detached' })
    assert((await count(page)) === 4 && (await page.textContent('[data-testid=dd-canvas] svg g text')) === 'Login', 'rectangle, ellipse, arrow and text drawn')

    // Select: click, move, Shift+click, duplicate, delete, rubber band.
    await page.keyboard.press('v')
    await page.mouse.click(cb.x + cb.width * 0.35, cb.y + cb.height * 0.4)
    assert((await page.$$('[data-testid=dd-sel]')).length === 1, 'a click selects the rectangle')
    const before = await page.getAttribute('[data-testid=dd-canvas] svg g path >> nth=0', 'd')
    const arrowD = () => page.getAttribute('[data-testid=dd-canvas] svg g path >> nth=2', 'd')
    const arrowBefore = await arrowD()
    await stroke(page, 0.35, 0.4, 0.35, 0.5)
    assert((await page.getAttribute('[data-testid=dd-canvas] svg g path >> nth=0', 'd')) !== before, 'dragging moves the selection')
    assert((await arrowD()) !== arrowBefore, 'the arrow tied to the rectangle follows it')
    await page.keyboard.press('Control+z')
    assert((await page.getAttribute('[data-testid=dd-canvas] svg g path >> nth=0', 'd')) === before && (await arrowD()) === arrowBefore, 'the move is undone in one step')
    // The arrow selected alone: its ends are tied; one dragged away comes loose.
    await page.mouse.click(cb.x + cb.width * 0.5, cb.y + cb.height * 0.3)
    assert((await page.isVisible('[data-testid=dd-end-from].tied')) && (await page.isVisible('[data-testid=dd-end-to].tied')), 'both ends of the arrow are tied')
    const end = await page.locator('[data-testid=dd-end-to]').boundingBox()
    await page.mouse.move(end.x + 5, end.y + 5)
    await page.mouse.down()
    await page.mouse.move(cb.x + cb.width * 0.5, cb.y + cb.height * 0.7, { steps: 5 })
    assert(await page.isVisible('[data-testid=dd-target]').then((v) => !v), 'no target away from the shapes')
    await page.mouse.up()
    assert(!(await page.isVisible('[data-testid=dd-end-to].tied')), 'an end dragged away comes loose')
    await page.keyboard.press('Control+z')
    assert(await page.isVisible('[data-testid=dd-end-to].tied'), 'undo ties it again')
    await page.keyboard.press('Escape')
    await page.mouse.click(cb.x + cb.width * 0.35, cb.y + cb.height * 0.4)
    await page.keyboard.down('Shift')
    await page.mouse.click(cb.x + cb.width * 0.7, cb.y + cb.height * 0.32)
    await page.keyboard.up('Shift')
    assert((await page.$$('[data-testid=dd-sel]')).length === 2, 'Shift+click adds to the selection')
    await page.keyboard.press('Control+d')
    assert((await count(page)) === 6 && (await page.$$('[data-testid=dd-sel]')).length === 2, 'Ctrl+D duplicates and selects the copies')
    await page.keyboard.press('Delete')
    assert((await count(page)) === 4, 'Delete removes the selection')
    await stroke(page, 0.05, 0.05, 0.95, 0.95)
    assert((await page.$$('[data-testid=dd-sel]')).length === 4, 'the rubber band selects what it contains')
    await page.click('[data-testid=dd-color-blue]')
    const strokes = await page.$$eval('[data-testid=dd-canvas] svg g path', (ps) => ps.map((p) => p.getAttribute('stroke')))
    assert(strokes.every((c) => c === '#0969da' || c === '#58a6ff'), `a color applies to the selection: ${strokes}`)
    await page.keyboard.press('Escape')
    assert((await page.$$('[data-testid=dd-sel]')).length === 0 && (await page.isVisible('[data-testid=doodle]')), 'Escape clears the selection first')

    // Resize the rectangle by a corner.
    await page.mouse.click(cb.x + cb.width * 0.35, cb.y + cb.height * 0.4)
    const handle = await page.locator('[data-testid=dd-sel-se]').boundingBox()
    await page.mouse.move(handle.x + 5, handle.y + 5)
    await page.mouse.down()
    await page.mouse.move(handle.x + 60, handle.y + 40, { steps: 5 })
    await page.mouse.up()
    assert((await page.getAttribute('[data-testid=dd-canvas] svg g path >> nth=0', 'd')) !== before, 'a corner handle resizes the selection')
    await page.keyboard.press('Control+z')

    // Double click edits a text.
    await page.dblclick('[data-testid=dd-canvas] svg g text')
    await page.waitForSelector('[data-testid=dd-text-edit]')
    await page.keyboard.press('End')
    await page.keyboard.type(' page')
    await page.keyboard.press('Escape')
    assert((await page.textContent('[data-testid=dd-canvas] svg g text')) === 'Login page', 'double click edits the text')

    // Copy, then paste in another doodle.
    await page.keyboard.press('Control+a')
    await page.keyboard.press('Control+c')
    await page.click('[data-testid=dd-attach]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    await page.click('[data-testid=ai-attach]')
    await page.click('.ctx-menu .ctx-item:has-text("Doodle…")')
    await page.waitForSelector('[data-testid=doodle]')
    assert((await page.textContent('[data-testid=doodle] .dd-head h2')) === 'Doodle 2', 'second doodle of the draft named Doodle 2')
    await page.focus('[data-testid=dd-canvas]')
    await page.keyboard.press('Control+v')
    assert((await count(page)) === 4, 'elements pasted from another doodle')
    page.once('dialog', (d) => d.accept())
    await page.click('[data-testid=dd-close]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })

    // Screenshot (the screen picker is replaced by a canvas stream): background of a new doodle.
    await page.evaluate(() => {
      const c = document.createElement('canvas')
      c.width = 640
      c.height = 360
      const g = c.getContext('2d')
      const paint = () => {
        g.fillStyle = '#2a6'
        g.fillRect(0, 0, 640, 360)
      }
      paint()
      setInterval(paint, 50)
      navigator.mediaDevices.getDisplayMedia = async () => c.captureStream(20)
    })
    await page.click('[data-testid=ai-attach]')
    await page.click('.ctx-menu .ctx-item:has-text("Screenshot…")')
    await page.waitForSelector('[data-testid=doodle] image.dd-bg')
    assert((await page.textContent('.dd-frame-label')).includes('Image · 640×360'), 'a screenshot opens a doodle with it as background, the frame at its size')
    // Crop: the frame is free to resize over the image.
    const se = await page.locator('[data-testid=dd-handle-se]').boundingBox()
    await page.mouse.move(se.x + 6, se.y + 6)
    await page.mouse.down()
    await page.mouse.move(se.x - 100, se.y - 20, { steps: 5 })
    await page.mouse.up()
    const label = await page.textContent('.dd-frame-label')
    const [fw, fh] = label.split('· ')[1].split('×').map(Number)
    assert(fw < 640 && fh < 360 && Math.abs(fw / fh - 640 / 360) > 0.05, `the frame crops the image freely (${label})`)
    await page.click('[data-testid=dd-bg-remove]')
    assert(!(await page.$('image.dd-bg')), 'the background can be removed')
    await page.keyboard.press('Control+z')
    assert(!!(await page.$('image.dd-bg')), 'removing the background is undone')

    // Background from a file, then pasted from the clipboard.
    const png = Buffer.from((await page.evaluate(() => {
      const c = document.createElement('canvas')
      c.width = 300
      c.height = 200
      c.getContext('2d').fillRect(0, 0, 300, 200)
      return c.toDataURL('image/png')
    })).split(',')[1], 'base64')
    await page.setInputFiles('[data-testid=dd-bg-input]', { name: 'shot.png', mimeType: 'image/png', buffer: png })
    await page.waitForFunction(() => document.querySelector('.dd-frame-label')?.textContent.includes('300×200'))
    assert(true, 'a picked image becomes the background')
    await page.evaluate(() => {
      const c = document.createElement('canvas')
      c.width = 120
      c.height = 90
      return new Promise((r) =>
        c.toBlob((b) => {
          const dt = new DataTransfer()
          dt.items.add(new File([b], 'pasted.png', { type: 'image/png' }))
          document.querySelector('[data-testid=dd-canvas]').dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true }))
          r()
        }),
      )
    })
    await page.waitForFunction(() => document.querySelector('.dd-frame-label')?.textContent.includes('120×90'))
    assert(true, 'a pasted image becomes the background')
    await page.keyboard.press('r')
    await stroke(page, 0.4, 0.4, 0.6, 0.6)
    await page.click('[data-testid=dd-attach]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })

    // Description: numbered shapes, label, arrow ends.
    await page.fill('.ai-composer .ed-content', 'Shapes')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Shapes")', { timeout: 15000 })
    user = [...requests[requests.length - 1].messages].reverse().find((m) => m.role === 'user')
    const d2 = text(user)
    assert(d2.includes('annotates an image given by the user') && d2.includes('120×90 (cropped from the image)'), `background described: ${d2}`)
    assert(/\[1\] rectangle, blue, [^\n]*labeled "Login page"/.test(d2) && /\[3\] arrow, blue, from \[1\] to \[2\]/.test(d2) && /\[4\] text "Login page", medium, blue, [^\n]*inside \[1\]/.test(d2), `description of shapes: ${d2}`)

    // Layout: a box split in columns, then rows; a divider dragged; zones named.
    await page.click('.ai-composer .ed-content')
    await page.keyboard.press('Control+Shift+D')
    await page.waitForSelector('[data-testid=doodle]')
    await page.keyboard.press('k')
    await stroke(page, 0.1, 0.1, 0.9, 0.9)
    await page.waitForSelector('[data-testid=dd-zone-menu]')
    assert(true, 'a layout drawn shows the menu of its zone')
    await page.click('[data-testid=dd-cols-2]')
    const lb = await page.locator('[data-testid=dd-canvas]').boundingBox()
    await page.mouse.click(lb.x + lb.width * 0.7, lb.y + lb.height * 0.5)
    await page.click('[data-testid=dd-rows-2]')
    await stroke(page, 0.5, 0.7, 0.3, 0.7)
    await page.mouse.dblclick(lb.x + lb.width * 0.2, lb.y + lb.height * 0.5)
    await page.waitForSelector('[data-testid=dd-zone-name]')
    await page.keyboard.type('sidebar')
    await page.keyboard.press('Enter')
    await page.mouse.click(lb.x + lb.width * 0.7, lb.y + lb.height * 0.3)
    await page.click('[data-testid=dd-zone-rename]')
    await page.keyboard.type('header')
    await page.keyboard.press('Enter')
    const names = await page.$$eval('[data-testid=dd-canvas] svg g text', (e) => e.map((x) => x.textContent))
    assert(names.includes('sidebar') && names.includes('header'), `zone names drawn: ${names}`)
    await page.keyboard.press('Escape')
    assert(!(await page.$('[data-testid=dd-zone-menu]')) && (await page.isVisible('[data-testid=doodle]')), 'Escape closes the zone menu first')
    // A text in the sidebar, a rectangle in the unnamed zone below the header.
    await page.keyboard.press('t')
    await page.mouse.click(lb.x + lb.width * 0.15, lb.y + lb.height * 0.2)
    await page.waitForSelector('[data-testid=dd-text-edit]')
    await page.keyboard.type('Menu')
    await page.keyboard.press('Escape')
    await page.keyboard.press('r')
    await stroke(page, 0.5, 0.6, 0.7, 0.8)
    await page.click('[data-testid=dd-attach]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    await page.fill('.ai-composer .ed-content', 'Layout')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Layout")', { timeout: 15000 })
    user = [...requests[requests.length - 1].messages].reverse().find((m) => m.role === 'user')
    const d3 = text(user)
    assert(
      /\[1\] layout, [^\n]*split in columns \(left to right\):\n  - column 1: 25 % "sidebar", holds \[2\]\n  - column 2: 75 %, split in rows \(top to bottom\):\n    - row 1: 50 % "header"\n    - row 2: 50 %, holds \[3\]/.test(d3),
      `layout described as a tree with what its zones hold: ${d3}`,
    )
    assert(/\[2\] text "Menu", [^\n]*inside \[1\] zone "sidebar"/.test(d3) && /\[3\] rectangle, [^\n]*inside \[1\] zone column 2 › row 2/.test(d3), `elements name their zone: ${d3}`)

    // Deleting zones: the neighbor takes the place, a single part left merges into its parent.
    await page.click('.ai-composer .ed-content')
    await page.keyboard.press('Control+Shift+D')
    await page.waitForSelector('[data-testid=doodle]')
    await page.keyboard.press('k')
    await stroke(page, 0.1, 0.1, 0.9, 0.9)
    await page.click('[data-testid=dd-cols-3]')
    const parts = () => page.$eval('[data-testid=dd-canvas] svg g path', (p) => p.getAttribute('d').split('M').length - 1)
    assert((await parts()) === 3, 'three columns: the outline and two dividers')
    const rows = await page.$$eval('[data-testid=dd-zone-menu] .dd-zone-row', (r) => r.map((x) => Math.round(x.getBoundingClientRect().top)))
    assert(rows.length === 4 && new Set(rows).size === 4, `one line per group in the zone menu: ${rows}`)
    await page.mouse.click(lb.x + lb.width * 0.5, lb.y + lb.height * 0.5)
    await page.click('[data-testid=dd-zone-delete]')
    assert((await parts()) === 2, 'a zone deleted: one divider left')
    await page.mouse.click(lb.x + lb.width * 0.2, lb.y + lb.height * 0.5)
    await page.click('[data-testid=dd-zone-delete]')
    assert((await parts()) === 1 && !(await page.$('[data-testid=dd-zone-delete]')), 'the last part merges into the layout')
    await page.keyboard.press('Control+z')
    assert((await parts()) === 2, 'deleting a zone is undone')

    // Filled rectangle; help of the keys.
    await page.keyboard.press('r')
    await page.click('[data-testid=dd-fill]')
    await stroke(page, 0.4, 0.3, 0.6, 0.5)
    const filled = await page.$eval('[data-testid=dd-canvas] svg g path:last-child', (p) => [p.getAttribute('fill'), p.getAttribute('fill-opacity')])
    assert(filled[0] !== 'none' && filled[1] === '0.2', `a filled rectangle has a light tint: ${filled}`)
    await page.click('[data-testid=dd-fill]')
    await page.keyboard.press('?')
    await page.waitForSelector('[data-testid=dd-help]:has-text("Keyboard shortcuts")')
    assert((await page.textContent('[data-testid=dd-help]')).includes('Duplicate'), '? shows the keys')
    await page.keyboard.press('Escape')
    assert(!(await page.$('[data-testid=dd-help]')) && (await page.isVisible('[data-testid=doodle]')), 'Escape closes the help first')
    page.once('dialog', (d) => d.accept())
    await page.click('[data-testid=dd-close]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })

    // A ticket created by the model gets the doodles of the conversation.
    await page.fill('.ai-composer .ed-content', 'Make a ticket of it')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Ticket made.")', { timeout: 15000 })
    const toolMsg = requests[requests.length - 1].messages.find((m) => m.role === 'tool')
    assert(/Ticket #\d+ created in the backlog \(status New\)\. \d+ doodles of the conversation attached to it as PNG files\./.test(text(toolMsg)), `doodles attached to the ticket: ${text(toolMsg)}`)
    assert(text(requests[requests.length - 1].messages[0]).includes('attached to the tickets you create or update as PNG files'), 'the prompt says the doodles go to the tickets')

    // The model draws on the board: a page, a copy of it, an invalid call; the image reaches
    // the model; the board says so; the pages go to the tickets.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer .ed-content', 'Draw: the login flow')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Drawn.")', { timeout: 20000 })
    const msgs = requests[requests.length - 1].messages
    const d = (id) => text(msgs.find((m) => m.role === 'tool' && m.tool_call_id === id))
    assert(d('d1').includes('A new blank page (not a clone'), 'a blank page says it is no clone')
    assert(d('d1').includes('Page 1 "Login flow" drawn by you') && d('d1').includes('"header"') && d('d1').includes('Sign in') && d('d1').includes('Happy path'), 'page described to the model: ' + d('d1'))
    const afterD1 = msgs[msgs.findIndex((m) => m.tool_call_id === 'd1') + 1]
    assert(afterD1.role === 'user' && afterD1.content.some((p) => p.type === 'image_url' && p.image_url.url.startsWith('data:image/png')), 'the image of the page follows the tool result')
    assert(d('d2').includes('Page 2 "Login flow, fixed"') && d('d2').includes('A clone of page 1'), 'copy of page 1: ' + d('d2'))
    assert(d('d3').startsWith('Error:') && d('d3').includes('element 1 (arrow): to "nope" is no element id'), 'invalid arrow refused: ' + d('d3'))
    assert((await page.$$('[data-testid=ai-page-card]')).length === 2, 'a card per page in the thread')
    await page.waitForSelector('.toast:has-text("New page on the board")')
    await page.click('[data-testid=ai-board-toggle]')
    await page.waitForSelector('[data-testid=bd-title]:has-text("Page 2")')
    assert((await page.textContent('[data-testid=bd-board] .bd-by')).includes('drawn by the assistant'), 'drawn by the assistant')
    const p2 = await page.$$eval('[data-testid=bd-view] path', (p) => p.length)
    await page.click('[data-testid=bd-thumb] >> nth=0')
    const p1 = await page.$$eval('[data-testid=bd-view] path', (p) => p.length)
    assert(p2 === p1 + 1, `the copy has one stroke more, page 1 unchanged (${p1} → ${p2})`)
    await page.screenshot({ path: OUT + '/board-model.png' })
    await page.click('[data-testid=ai-board-toggle]')
    await page.fill('.ai-composer .ed-content', 'Make a ticket of it')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Ticket made.") >> nth=-1', { timeout: 15000 })
    const tk = text([...requests[requests.length - 1].messages].reverse().find((m) => m.role === 'tool'))
    assert(tk.includes('2 doodles of the conversation attached'), 'the pages of the model go to the ticket: ' + tk)

    // Images: the image sent by the user is a page, annotated on a copy; an SVG of the model
    // as a page; an image that is neither an SVG nor "screen"; a capture of the screen refused.
    fs.mkdirSync(WS + '/demo/img', { recursive: true })
    fs.writeFileSync(WS + '/demo/img/logo.png', PNG)
    fs.writeFileSync(WS + '/demo/img/anim.gif', GIF)
    await page.click('.ai-panel button[title="New conversation"]')
    await page.setInputFiles('.ai-composer input[type=file]', { name: 'shot.png', mimeType: 'image/png', buffer: PNG })
    await page.waitForSelector('.ai-composer .ai-att[data-kind=image]')
    await page.fill('.ai-composer .ed-content', 'Back: annotate')
    await page.keyboard.press('Control+Enter')
    await page.waitForSelector('[data-testid=ai-capture]', { timeout: 30000 })
    assert(true, 'the capture of the screen waits for the user')
    await page.click('[data-testid=ai-capture-refuse]')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Backgrounds done.")', { timeout: 20000 })
    const bm = requests[requests.length - 1].messages
    const b = (id) => text(bm.find((m) => m.role === 'tool' && m.tool_call_id === id))
    assert(b('b1').includes('Page 2 "b1"') && b('b1').includes('Frame 4×3') && b('b1').includes('A clone of page 1'), 'the image sent, annotated on a copy: ' + b('b1'))
    assert(b('b2').includes('Page 3 "b2"') && b('b2').includes('background image: an SVG written by you (300×200)'), 'an SVG of the model as a page: ' + b('b2'))
    assert(b('b3').includes('Page 4 "b3"') && b('b3').includes('background image: the image img/logo.png (4×3)'), 'an image of the project as a page: ' + b('b3'))
    assert(b('b4').startsWith('Error:') && b('b4').includes('"notes.txt" is none of them'), 'not an image: ' + b('b4'))
    assert(b('b5').startsWith('Error:') && b('b5').includes('outside the project'), 'no image outside the project: ' + b('b5'))
    assert(b('b6').includes('refused to share their screen'), 'capture refused: ' + b('b6'))
    // A transparent animated GIF: a page of its size, its transparent part white (not black).
    assert(b('b7').includes('Page 5 "b7"') && b('b7').includes('the image img/anim.gif (8×6)'), 'a GIF of the project as a page: ' + b('b7'))
    const gifPage = bm.find((m) => m.role === 'user' && Array.isArray(m.content) && m.content.some((p) => p.type === 'text' && p.text.includes('"b7"')))
    const gifPNG = gifPage.content[gifPage.content.findIndex((p) => p.type === 'text' && p.text.includes('"b7"')) + 1].image_url.url
    const corner = await page.evaluate(async (src) => {
      const img = new Image()
      await new Promise((r) => ((img.onload = r), (img.src = src)))
      const c = document.createElement('canvas')
      c.width = img.width
      c.height = img.height
      const g = c.getContext('2d')
      g.drawImage(img, 0, 0)
      return [...g.getImageData(1, 1, 1, 1).data]
    }, gifPNG)
    assert(corner[0] > 200 && corner[1] > 200 && corner[2] > 200, `the transparent part of the GIF is white: ${corner}`)
    assert(!(await page.$('[data-testid=ai-capture]')), 'the card goes once answered')
    await page.click('[data-testid=ai-board-toggle]')
    await page.waitForSelector('[data-testid=bd-title]:has-text("Page 5")')
    assert((await page.$$('[data-testid=bd-thumb]')).length === 5 && (await page.$$('[data-testid=bd-view] image')).length === 1, 'five pages: the image sent, its clone, the SVG, the PNG and the GIF of the project')
    assert((await page.getAttribute('[data-testid=bd-view] image', 'href')).startsWith('data:image/gif'), 'the GIF of the project stays a GIF on its page (animated)')
    await page.click('[data-testid=bd-thumb] >> nth=0')
    assert((await page.textContent('[data-testid=bd-title]')) === 'Page 1 · shot.png', 'the image sent is page 1')
    await page.screenshot({ path: OUT + '/board-background.png' })
    await page.click('[data-testid=ai-board-toggle]')
  } finally {
    fake.close()
  }
})
