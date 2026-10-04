// Editor: text against the gutter, indentation guides, whitespace, multiple carets, folding.
const { run, openProject, open, assert, OUT } = require('../common.cjs')

run(async ({ page }) => {
  await openProject(page)
  await open(page, 'main.go')

  // The first character of a line touches the gutter.
  const gap = await page.evaluate(() => {
    const t = document.querySelector('.pane.active .ed-block').firstChild
    const r = document.createRange()
    r.setStart(t, 0)
    r.setEnd(t, 1)
    return r.getBoundingClientRect().left - document.querySelector('.pane.active .ed-gutter').getBoundingClientRect().right
  })
  assert(Math.abs(gap) < 1, 'no space between the gutter and the text (' + gap + ')')

  // Indentation guides: one per indented block, the one of the caret block stands out.
  const caretAt = async (line, col = 0) => {
    await page.click('.pane.active .ed-content')
    await page.keyboard.press('Control+Home')
    for (let i = 0; i < line; i++) await page.keyboard.press('ArrowDown')
    for (let i = 0; i < col; i++) await page.keyboard.press('ArrowRight')
  }
  const lineTop = (line) => page.evaluate((l) => {
    const t = document.querySelector('.pane.active .ed-block').firstChild
    const off = t.data.split('\n').slice(0, l).join('\n').length + (l ? 1 : 0)
    const r = document.createRange()
    r.setStart(t, off)
    r.setEnd(t, off + 1)
    return r.getBoundingClientRect().top - document.querySelector('.pane.active .ed-guides').getBoundingClientRect().top
  }, line)
  await caretAt(12)
  await page.waitForSelector('.pane.active .ed-guide.active')
  const guides = await page.$$eval('.pane.active .ed-guide', (g) => g.map((e) => ({ top: e.offsetTop, h: e.offsetHeight, active: e.classList.contains('active') })))
  assert(guides.length === 2, 'a guide per indented block (' + guides.length + ')')
  const active = guides.find((g) => g.active)
  assert(Math.abs(active.top - (await lineTop(12))) < 4 && guides.filter((g) => g.active).length === 1, 'the guide of the caret block is the active one')
  await caretAt(7)
  await page.waitForFunction((top) => Math.abs(document.querySelector('.pane.active .ed-guide.active')?.offsetTop - top) < 4, await lineTop(8))
  assert(true, 'on the line opening a block, its guide is the active one')
  await page.screenshot({ path: OUT + '/editor-guides.png' })
})
