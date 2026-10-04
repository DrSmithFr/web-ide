// Editor: text against the gutter, indentation guides, whitespace, multiple carets, folding.
const { run, openProject, open, assert } = require('../common.cjs')

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
})
