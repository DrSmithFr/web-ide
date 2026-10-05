// AI assistant, doodles: paperclip menu, modal with the conversation, pen, marker under the
// pen, pixel and object eraser (and the eraser end of a stylus), stylus pressure, undo / redo,
// frame presets, attach (chip opened again), PNG and description sent to the model,
// Ctrl+Shift+D and sending from the composer of the modal.
const http = require('http')
const { run, openProject, assert } = require('../common.cjs')

const requests = []
const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res) {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`)
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
  sse(res, { content: `Got ${text(lastUser).split('\n')[0].slice(0, 40)}` })
  end(res)
})

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
    assert(items.join('|') === 'File…|Doodle…', `paperclip menu: ${items.join(', ')}`)
    await page.click('.ctx-menu .ctx-item:has-text("Doodle…")')
    await page.waitForSelector('[data-testid=doodle]')
    assert(await page.isVisible('[data-testid=doodle] .ai-composer textarea'), 'the doodle modal shows the conversation and its composer')
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
    await page.fill('.ai-composer textarea', 'Here is my sketch')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Here is my sketch")', { timeout: 15000 })
    let user = requests[requests.length - 1].messages.find((m) => m.role === 'user')
    const kinds = user.content.map((p) => p.type)
    const desc = text(user)
    assert(kinds.includes('image_url') && user.content.find((p) => p.type === 'image_url').image_url.url.startsWith('data:image/png'), 'PNG sent to a model reading images')
    assert(desc.includes('Doodle "Doodle 1"') && desc.includes('Frame 800×800 (Square)') && desc.includes('Free pen strokes (hand drawn, shapes approximate): 1') && desc.includes('- red'), `description sent: ${desc}`)
    assert(await page.isVisible('.ai-msg.user .ai-att[data-kind=doodle]'), 'the message shows the doodle')

    // Ctrl+Shift+D, then sending from the composer of the modal attaches the doodle.
    await page.click('.ai-composer textarea')
    await page.keyboard.press('Control+Shift+D')
    await page.waitForSelector('[data-testid=doodle]')
    assert((await page.textContent('[data-testid=doodle] .dd-head h2')) === 'Doodle 1', 'Ctrl+Shift+D opens a new doodle')
    await stroke(page, 0.3, 0.3, 0.5, 0.5)
    await page.fill('[data-testid=doodle] .ai-composer textarea', 'Second sketch')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Second sketch")', { timeout: 15000 })
    user = [...requests[requests.length - 1].messages].reverse().find((m) => m.role === 'user')
    assert(user.content.some((p) => p.type === 'image_url') && text(user).includes('Doodle "Doodle 1"'), 'sent from the modal with the doodle')
    assert(!(await page.$('.ai-composer .ai-att')), 'the draft is empty after sending')
  } finally {
    fake.close()
  }
})
