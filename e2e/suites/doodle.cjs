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

    // Shapes, text, selection.
    await page.click('.ai-composer textarea')
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
    await stroke(page, 0.35, 0.4, 0.35, 0.5)
    assert((await page.getAttribute('[data-testid=dd-canvas] svg g path >> nth=0', 'd')) !== before, 'dragging moves the selection')
    await page.keyboard.press('Control+z')
    assert((await page.getAttribute('[data-testid=dd-canvas] svg g path >> nth=0', 'd')) === before, 'the move is undone in one step')
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
    await page.fill('.ai-composer textarea', 'Shapes')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Shapes")', { timeout: 15000 })
    user = [...requests[requests.length - 1].messages].reverse().find((m) => m.role === 'user')
    const d2 = text(user)
    assert(d2.includes('annotates an image given by the user') && d2.includes('120×90 (cropped from the image)'), `background described: ${d2}`)
    assert(/\[1\] rectangle, blue, [^\n]*labeled "Login page"/.test(d2) && /\[3\] arrow, blue, from \[1\] to \[2\]/.test(d2) && /\[4\] text "Login page", medium, blue, [^\n]*inside \[1\]/.test(d2), `description of shapes: ${d2}`)

    // Layout: a box split in columns, then rows; a divider dragged; zones named.
    await page.click('.ai-composer textarea')
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
    await page.click('[data-testid=dd-attach]')
    await page.waitForSelector('[data-testid=doodle]', { state: 'detached' })
    await page.fill('.ai-composer textarea', 'Layout')
    await page.keyboard.press('Enter')
    await page.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Got Layout")', { timeout: 15000 })
    user = [...requests[requests.length - 1].messages].reverse().find((m) => m.role === 'user')
    const d3 = text(user)
    assert(/\[1\] layout, [^\n]*split in columns \(left to right\):\n  - 25 % "sidebar"\n  - 75 %, split in rows \(top to bottom\):\n    - 50 % "header"\n    - 50 %/.test(d3), `layout described as a tree: ${d3}`)
  } finally {
    fake.close()
  }
})
