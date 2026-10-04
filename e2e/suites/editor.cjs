// Editor: text against the gutter, indentation guides, whitespace, multiple carets, folding.
const fs = require('fs')
const { run, openProject, open, assert, text, WS, OUT } = require('../common.cjs')

run(async ({ page }) => {
  fs.writeFileSync(WS + '/demo/multi.txt', 'foo bar foo\nfoo baz\n    foo qux\n')
  fs.writeFileSync(WS + '/demo/conf.yaml', 'server:\n  host: x\n  ports:\n    - 80\n    - 443\nname: demo\n')
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

  // Whitespace (View menu): dots for spaces, arrows for tabs, a mark at each line end.
  const whitespace = async () => {
    await page.click('.menu-btn:has-text("View")')
    await page.click('.ctx-menu .ctx-item:has-text("Show whitespace")')
  }
  await whitespace()
  await page.waitForSelector('.pane.active .ed-ws i.t', { state: 'attached', timeout: 3000 })
  const ws = await page.evaluate(() => {
    const q = (s) => document.querySelectorAll('.pane.active .ed-ws ' + s)
    const tab = q('i.t')[0].getBoundingClientRect()
    const main = document.querySelector('.pane.active .ed-main').getBoundingClientRect()
    const cw = document.querySelector('.pane.active .ed-content').getBoundingClientRect().width
    return { tabs: q('i.t').length, eol: q('i.n').length, spaces: q('i:not([class])').length, tabLeft: tab.left - main.left, tabWidth: tab.width }
  })
  assert(ws.tabs === 2 && ws.eol === 14 && ws.spaces > 10, 'tabs, spaces and line ends marked ' + JSON.stringify(ws))
  const cw = await page.evaluate(() => {
    const t = document.querySelector('.pane.active .ed-block').firstChild
    const r = document.createRange()
    r.setStart(t, 0)
    r.setEnd(t, 7)
    return r.getBoundingClientRect().width / 7
  })
  assert(Math.abs(ws.tabLeft) < 1 && Math.abs(ws.tabWidth - 4 * cw) < 1, 'the tab mark covers the tab')
  await page.screenshot({ path: OUT + '/editor-whitespace.png' })
  await whitespace()
  await page.waitForFunction(() => !document.querySelector('.pane.active .ed-ws').firstChild)
  assert(true, 'whitespace hidden again')

  // Multiple carets.
  await open(page, 'multi.txt')
  const content = '.pane.active .ed-content'
  const reset = async () => {
    await page.click(content)
    await page.keyboard.press('Escape')
    await page.keyboard.press('Control+a')
    await page.keyboard.insertText('foo bar foo\nfoo baz\n    foo qux\n')
  }
  // Screen position of a line and column of the text.
  const at = (line, col) => page.evaluate(([l, c]) => {
    const t = document.querySelector('.pane.active .ed-block').firstChild
    const off = t.data.split('\n').slice(0, l).reduce((n, x) => n + x.length + 1, 0) + c
    const r = document.createRange()
    r.setStart(t, off)
    r.setEnd(t, off + 1)
    const b = r.getBoundingClientRect()
    return { x: b.left + 1, y: b.top + b.height / 2 }
  }, [line, col])
  const carets = async () => {
    await page.waitForTimeout(80)
    return page.$$eval('.pane.active .ed-caret', (l) => l.length)
  }

  // Alt+J: the word at the caret, then its next occurrences; Shift+Alt+J removes the last one.
  await page.click(content)
  await page.keyboard.press('Control+Home')
  await page.keyboard.press('ArrowRight')
  await page.keyboard.press('Alt+j')
  assert((await page.evaluate(() => getSelection().toString())) === 'foo', 'Alt+J selects the word at the caret')
  await page.keyboard.press('Alt+j')
  await page.keyboard.press('Alt+j')
  await page.keyboard.press('Alt+j')
  assert((await carets()) === 3, 'Alt+J adds the next occurrences (' + (await carets()) + ' other carets)')
  await page.keyboard.press('Alt+Shift+j')
  await page.keyboard.type('X')
  assert((await text(page)) === 'X bar X\nX baz\n    foo qux\n', 'typing replaces every selection')
  await page.keyboard.press('Backspace')
  await page.keyboard.type('ab')
  assert((await text(page)) === 'ab bar ab\nab baz\n    foo qux\n', 'Backspace at every caret')
  await page.keyboard.press('Escape')
  assert((await carets()) === 0, 'Escape leaves a single caret')
  await page.keyboard.press('Control+z')
  assert((await text(page)) === 'a bar a\na baz\n    foo qux\n', 'one undo step per edit of all the carets')

  // Ctrl+Alt+Shift+J: every occurrence.
  await reset()
  await page.keyboard.press('Control+Home')
  await page.keyboard.press('Control+Alt+Shift+j')
  await page.keyboard.type('zz')
  assert((await text(page)) === 'zz bar zz\nzz baz\n    zz qux\n', 'every occurrence selected and replaced')

  // Column selection with the middle button, then the arrows, End and Enter at every caret.
  await reset()
  const p0 = await at(0, 0)
  const p2 = await at(2, 0)
  await page.mouse.move(p0.x - 1, p0.y)
  await page.mouse.down({ button: 'middle' })
  await page.mouse.move(p2.x - 1, p2.y, { steps: 4 })
  await page.mouse.up({ button: 'middle' })
  assert((await carets()) === 2, 'middle button drag: a caret per line')
  await page.keyboard.type('> ')
  await page.keyboard.press('End')
  await page.keyboard.type(';')
  assert((await text(page)) === '> foo bar foo;\n> foo baz;\n>     foo qux;\n', 'typing, End at every caret')
  await page.screenshot({ path: OUT + '/editor-carets.png' })
  await page.keyboard.press('Shift+Home')
  assert((await page.$$eval('.pane.active .ed-caret', (l) => l.length)) === 2, 'Shift+Home extends every selection')
  await page.keyboard.press('Delete')
  assert((await text(page)) === '\n\n\n', 'Delete removes every selection')

  // Alt+Shift+drag: a box from column 4 to 7.
  await reset()
  const a = await at(0, 4)
  const b = await at(1, 7)
  await page.keyboard.down('Alt')
  await page.keyboard.down('Shift')
  await page.mouse.move(a.x - 1, a.y)
  await page.mouse.down()
  await page.mouse.move(b.x - 1, b.y, { steps: 4 })
  await page.mouse.up()
  await page.keyboard.up('Shift')
  await page.keyboard.up('Alt')
  await page.keyboard.type('Z')
  assert((await text(page)) === 'foo Z foo\nfoo Z\n    foo qux\n', 'Alt+Shift+drag selects a column')

  // Alt+click adds a caret, a click goes back to one.
  await reset()
  await page.click(content)
  await page.keyboard.press('Control+Home')
  const c = await at(1, 0)
  await page.keyboard.down('Alt')
  await page.mouse.click(c.x - 1, c.y)
  await page.keyboard.up('Alt')
  await page.keyboard.type('#')
  assert((await text(page)) === '#foo bar foo\n#foo baz\n    foo qux\n', 'Alt+click adds a caret')
  await page.mouse.click(c.x + 30, c.y)
  assert((await carets()) === 0, 'a click leaves a single caret')

  // Folding: gutter markers, placeholders, shortcuts, fold all, session.
  await open(page, 'main.go')
  const nums = () => page.textContent('.pane.active .ed-gutter-nums')
  const holders = async () => {
    await page.waitForTimeout(80)
    return page.$$eval('.pane.active .ed-placeholder', (l) => l.length)
  }
  await page.hover('.pane.active .ed-gutter')
  await page.click('.pane.active .ed-fold[data-fold="7"]')
  await page.waitForSelector('.pane.active .ed-placeholder')
  assert(!(await nums()).split('\n').includes('9') && (await nums()).includes('10'), 'gutter marker folds the function body')
  assert(await page.isVisible('.pane.active .ed-fold.folded[data-fold="7"]'), 'folded marker')
  await page.screenshot({ path: OUT + '/editor-fold.png' })
  await page.click('.pane.active .ed-placeholder')
  await page.waitForFunction(() => document.querySelector('.pane.active .ed-gutter-nums').textContent.split('\n').includes('9'))
  assert((await holders()) === 0, 'a click on the placeholder unfolds')

  await caretAt(12, 2)
  await page.keyboard.press('Control+Minus')
  assert((await holders()) === 1 && !(await nums()).split('\n').includes('13'), 'Ctrl+- folds the block of the caret')
  assert((await page.evaluate(() => getSelection().focusOffset)) > 0, 'the caret leaves the hidden lines')
  await page.keyboard.press('Control+Equal')
  assert((await holders()) === 0, 'Ctrl+= unfolds it')
  await page.keyboard.press('Control+Shift+Minus')
  assert((await holders()) === 2, 'Ctrl+Shift+- folds every block')
  await page.keyboard.press('Control+Shift+Equal')
  assert((await holders()) === 0, 'Ctrl+Shift+= unfolds every block')

  // A search result in a folded block unfolds it; typing in the header keeps the fold.
  await caretAt(12, 2)
  await page.keyboard.press('Control+Minus')
  await page.keyboard.type(' ')
  assert((await holders()) === 1, 'typing on the header line keeps the fold')
  await page.keyboard.press('Backspace')
  await page.keyboard.press('Control+g')
  await page.keyboard.type('13')
  await page.keyboard.press('Enter')
  await page.waitForTimeout(200)
  assert((await holders()) === 0, 'going to a hidden line unfolds it')

  // Kept in the session.
  await caretAt(7)
  await page.keyboard.press('Control+Minus')
  await page.waitForTimeout(1500)
  await page.reload()
  await page.waitForSelector('.pane.active .ed-placeholder', { timeout: 15000 })
  assert(!(await nums()).split('\n').includes('9'), 'folds restored after a reload')

  // YAML: by indentation.
  await open(page, 'conf.yaml')
  await page.click('.pane.active .ed-content')
  await page.keyboard.press('Control+Shift+Minus')
  assert((await holders()) === 1 && (await nums()).trim().split('\n').join(',') === '1,6,7', 'YAML folded by indentation (' + (await nums()).trim().split('\n').join(',') + ')')
})
