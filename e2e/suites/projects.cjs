// Projects: generated icon (home page, favicon), icon editor.
const fs = require('fs')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

const favicon = (page) => page.evaluate(() => decodeURIComponent(document.querySelector('link[rel="icon"]').href))

run(async ({ page }) => {
  await openProject(page)
  // A project without an icon gets one generated: initials of its name, saved in .ide.
  await page.waitForFunction(() => decodeURIComponent(document.querySelector('link[rel="icon"]').href).includes('>DE</text>'), null, { timeout: 5000 }).catch(() => {})
  assert((await favicon(page)).includes('>DE</text>'), 'favicon: generated icon with the initials')
  const deadline = Date.now() + 5000
  while (!fs.existsSync(WS + '/demo/.ide/icon.svg') && Date.now() < deadline) await page.waitForTimeout(100)
  assert(fs.existsSync(WS + '/demo/.ide/icon.svg') && JSON.parse(fs.readFileSync(WS + '/demo/.ide/icon.json', 'utf8')).text === 'DE', 'icon saved in .ide')

  // Home page: the card shows it; the editor changes it.
  await page.click('.menubar button[title="Projects"]')
  await page.waitForSelector('.project-card [data-testid=project-icon]')
  assert((await favicon(page)).includes('<path'), 'home page: favicon of the application')
  await page.click('.project-card:has-text("demo") [data-testid=project-icon-edit]')
  await page.click('[data-testid=icon-kind-glyph]')
  await page.fill('[data-testid=icon-search]', 'rock')
  await page.click('.ie-glyph[data-glyph=rocket]')
  await page.click('.ie-shape[data-shape=hexagon]')
  await page.click('[data-testid=icon-gradient]')
  await page.click('.ie-angle[data-angle="135"]')
  assert((await page.getAttribute('[data-testid=icon-preview]', 'src')).includes('linearGradient'), 'preview with the gradient')
  await page.screenshot({ path: OUT + '/icon-editor.png' })
  await page.click('[data-testid=icon-save]')
  await page.waitForFunction(() => decodeURIComponent(document.querySelector('.project-card [data-testid=project-icon]').src).includes('linearGradient'), null, { timeout: 5000 })
  const spec = JSON.parse(fs.readFileSync(WS + '/demo/.ide/icon.json', 'utf8'))
  assert(spec.kind === 'glyph' && spec.glyph === 'rocket' && spec.shape === 'hexagon' && spec.color2 && spec.angle === 135, 'icon description saved: ' + JSON.stringify(spec))
  assert(fs.readFileSync(WS + '/demo/.ide/icon.svg', 'utf8').includes('<polygon'), 'icon image saved')
  await page.screenshot({ path: OUT + '/home-icons.png' })

  // The project window uses the new icon.
  await openProject(page)
  await page.waitForFunction(() => decodeURIComponent(document.querySelector('link[rel="icon"]').href).includes('linearGradient'), null, { timeout: 5000 }).catch(() => {})
  assert((await favicon(page)).includes('linearGradient'), 'favicon follows the icon')
})
