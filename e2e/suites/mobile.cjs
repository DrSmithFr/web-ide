// Phone layout (375×812, touch): one bar of three rows (project and status, the menus, the
// icons of the views), no side rails; one view at a time, full screen: a tool shown from its
// icon or from the Tools menu, the editor back when a file is opened; the line of the caret
// stays visible when the keyboard shrinks the visible area; the page does not zoom.
const fs = require('fs')
const { run, openProject, assert, OUT, WS } = require('../common.cjs')

fs.writeFileSync(WS + '/demo/long.txt', Array.from({ length: 200 }, (_, i) => `line ${i + 1}`).join('\n') + '\n')

const visible = (page, sel) => page.isVisible(sel)
const view = (page) =>
  page.evaluate(() => {
    const shown = [...document.querySelectorAll('.phone-body > .center, .phone-body > .zone')].filter((e) => e.offsetWidth > 0)
    return shown.map((e) => (e.classList.contains('center') ? 'editor' : e.getAttribute('data-tool'))).join(',')
  })
const active = (page) => page.getAttribute('[data-testid=mobile-rail] .rail-btn.active', 'data-id')

run(
  async ({ page }) => {
    await openProject(page)
    const meta = await page.getAttribute('meta[name=viewport]', 'content')
    assert(meta.includes('maximum-scale=1'), 'the viewport does not zoom: ' + meta)
    assert(await visible(page, '[data-testid=phone-bar] .menubar-row'), 'row 1: project and status')
    assert(await visible(page, '[data-testid=phone-bar] .menus .menu-btn'), 'row 2: the menus')
    assert(await visible(page, '[data-testid=mobile-rail]'), 'row 3: the icons of the views')
    assert(!(await page.$('.rail')) && !(await page.$('.resizer')), 'no side rails nor resizers')
    const width = await page.evaluate(() => document.documentElement.scrollWidth)
    assert(width <= 375, 'nothing wider than the screen: ' + width)
    assert((await view(page)) === 'editor' && (await active(page)) === 'editor', 'the editor first')
    await page.screenshot({ path: OUT + '/mobile-editor.png' })

    // The menus open from row 2 and fit the screen.
    await page.click('.menubar .menu-btn:has-text("File")')
    await page.waitForSelector('.ctx-menu')
    const box = await page.$eval('.ctx-menu', (e) => e.getBoundingClientRect().right)
    assert(box <= 375, 'the File menu opens and fits the screen')
    await page.keyboard.press('Escape')

    // A tool full screen; a file opened brings the editor back.
    await page.click('[data-testid=mobile-rail] [data-id=explorer]')
    await page.waitForSelector('.phone-body > .zone[data-tool=explorer]')
    assert((await view(page)) === 'explorer' && (await active(page)) === 'explorer', 'the explorer full screen, alone')
    await page.screenshot({ path: OUT + '/mobile-explorer.png' })
    await page.click('.tree-row:has-text("long.txt")')
    await page.waitForFunction(() => document.querySelector('.phone-body > .center:not(.hidden) .tab.active')?.textContent.includes('long.txt'))
    assert((await view(page)) === 'editor' && (await active(page)) === 'editor', 'opening a file brings the editor to the front')

    // The Tools menu shows a tool the same way; the icon of the view follows.
    await page.click('.menubar .menu-btn:has-text("Tools")')
    await page.click('.ctx-menu .ctx-item:has-text("Console")')
    await page.waitForSelector('.phone-body > .zone[data-tool=console]')
    assert((await view(page)) === 'console' && (await active(page)) === 'console', 'a tool shown from the menu is full screen')
    await page.click('[data-testid=mobile-rail] [data-id=assistant]')
    await page.waitForSelector('.phone-body > .zone[data-tool=assistant]')
    assert((await view(page)) === 'assistant', 'the assistant full screen')
    await page.screenshot({ path: OUT + '/mobile-assistant.png' })

    // No field takes the focus by itself (the keyboard would open): the user touches it.
    const typing = () => page.evaluate(() => !!document.activeElement?.matches('input, textarea'))
    await page.tap('[data-testid=mobile-rail] [data-id=search]')
    await page.waitForSelector('.phone-body > .zone[data-tool=search]')
    await page.waitForTimeout(300)
    assert(!(await typing()), 'the search tool opens without focusing its field')
    await page.tap('.zone[data-tool=search] input')
    assert(await typing(), 'a touch on the search field focuses it')
    await page.tap('[data-testid=mobile-rail] [data-id=assistant]')
    await page.waitForSelector('.phone-body > .zone[data-tool=assistant] .ai-composer textarea')
    await page.waitForTimeout(300)
    assert(!(await typing()), 'the assistant opens without focusing its message box')
    await page.tap('.ai-composer textarea')
    assert(await typing(), 'a touch on the message box focuses it')

    // The icon of a view gives it the focus; the editor without the keyboard.
    await page.tap('[data-testid=mobile-rail] [data-id=explorer]')
    await page.waitForFunction(() => document.activeElement?.closest('.zone[data-tool=explorer]'))
    assert(true, 'the icon of a tool gives it the focus')
    await page.tap('[data-testid=mobile-rail] [data-id=editor]')
    await page.waitForFunction(() => document.activeElement?.classList.contains('ed-content'))
    assert((await page.$eval('.pane.active .ed-content', (e) => e.inputMode)) === 'none', 'the editor icon gives the focus to the editor, keyboard closed')
    // A touch moves the caret without the keyboard; a double tap opens it.
    await page.tap('.pane.active .ed-content')
    assert((await page.$eval('.pane.active .ed-content', (e) => e.inputMode)) === 'none', 'one touch: no keyboard')
    await page.waitForTimeout(500)
    await page.tap('.pane.active .ed-content')
    await page.tap('.pane.active .ed-content')
    const mode = await page.$eval('.pane.active .ed-content', (e) => [e.inputMode, document.activeElement === e])
    assert(mode[0] === 'text' && mode[1], 'a double tap opens the keyboard: ' + mode)

    // The keyboard shrinks the visible area: the line of the caret stays in sight.
    await page.keyboard.press('Control+End')
    await page.setViewportSize({ width: 375, height: 420 })
    await page.waitForTimeout(400)
    const caret = await page.$eval('.pane.active .ed-curline', (e) => {
      const r = e.getBoundingClientRect()
      return { top: r.top, bottom: r.bottom, h: window.visualViewport.height }
    })
    assert(caret.top >= 0 && caret.bottom <= caret.h, `the caret line stays visible above the keyboard: ${JSON.stringify(caret)}`)
    const appH = await page.$eval('.app', (e) => e.getBoundingClientRect().height)
    assert(Math.abs(appH - 420) < 2, 'the page takes the visible height: ' + appH)
    await page.screenshot({ path: OUT + '/mobile-keyboard.png' })
  },
  { mobile: true, viewport: { width: 375, height: 812 } },
)
