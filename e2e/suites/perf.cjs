// Performance on a 100 000-line file: each keystroke and each scroll must fit in a few frames.
const fs = require('fs')
const { run, openProject, open, assert, WS } = require('../common.cjs')

const lines = []
for (let i = 0; i < 20000; i++) lines.push(`// block ${i}`, `function f${i}(a, b) {`, `  const s = "value ${i}" + a * b;`, '  return s.length > 3 ? s : null;', '}')
fs.writeFileSync(WS + '/demo/big.js', lines.join('\n') + '\n')

const LIMIT = Number(process.env.E2E_PERF_MS ?? 50)

/** Median and max of n actions, each followed by a rendered frame. */
const measure = (page, kind, n = 20) =>
  page.evaluate(
    async ([kind, n]) => {
      const el = document.querySelector('.pane.active .ed-content')
      const scroller = document.querySelector('.pane.active .ed-scroll')
      const res = []
      for (let i = 0; i < n; i++) {
        const t = performance.now()
        if (kind === 'type') el.dispatchEvent(new InputEvent('beforeinput', { inputType: 'insertText', data: 'x', bubbles: true, cancelable: true }))
        else if (kind === 'enter') el.dispatchEvent(new InputEvent('beforeinput', { inputType: 'insertParagraph', bubbles: true, cancelable: true }))
        else scroller.scrollTop = Math.random() * scroller.scrollHeight
        await new Promise((r) => requestAnimationFrame(() => setTimeout(r)))
        res.push(performance.now() - t)
      }
      res.sort((a, b) => a - b)
      return { median: res[n >> 1], max: res[n - 1] }
    },
    [kind, n],
  )

run(async ({ page }) => {
  await openProject(page)
  const t0 = Date.now()
  await open(page, 'big.js')
  console.log(`  ouverture : ${Date.now() - t0} ms`)
  await page.click('.pane.active .ed-content')
  await page.keyboard.press('Control+Home')
  for (const [where, key] of [['début', 'Control+Home'], ['fin', 'Control+End']]) {
    await page.keyboard.press(key)
    for (const kind of ['type', 'enter']) {
      const m = await measure(page, kind)
      assert(m.median < LIMIT, `${kind === 'type' ? 'frappe' : 'Entrée'} au ${where} : médiane ${m.median.toFixed(1)} ms, max ${m.max.toFixed(1)} ms`)
    }
  }
  const s = await measure(page, 'scroll')
  assert(s.median < LIMIT, `défilement : médiane ${s.median.toFixed(1)} ms, max ${s.max.toFixed(1)} ms`)
  const blocks = await page.$$eval('.pane.active .ed-block', (b) => b.length)
  assert(blocks > 1000, `texte découpé en blocs (${blocks})`)
})
