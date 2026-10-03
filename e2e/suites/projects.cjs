// Projects: generated icon (home page, favicon), icon editor; menu bar with the selector of
// the worktrees, a branch opened in its own worktree and window.
const fs = require('fs')
const { execSync } = require('child_process')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

const favicon = (page) => page.evaluate(() => decodeURIComponent(document.querySelector('link[rel="icon"]').href))

const env = { ...process.env, GIT_AUTHOR_NAME: 'e2e', GIT_AUTHOR_EMAIL: 'e2e@x', GIT_COMMITTER_NAME: 'e2e', GIT_COMMITTER_EMAIL: 'e2e@x' }
const git = (cmd, cwd = WS + '/demo') => execSync(`git ${cmd}`, { cwd, env, encoding: 'utf8' }).trim()

run(async ({ page, ctx }) => {
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

  // Menu bar: home, project (icon, title, branch), menus … pod status, settings.
  const order = await page.$$eval('.menubar > *', (els) => els.map((e) => e.className.split(' ')[0] + (e.title ? ':' + e.title.split(' ')[0] : '')))
  assert(order[0] === 'icon-btn:Projects' && order[1] === 'mb-project' && order[2] === 'menus' && order[order.length - 2].startsWith('pod-status') && order[order.length - 1] === 'icon-btn:Settings', 'menu bar order: ' + order.join(', '))
  assert((await page.textContent('.mb-title')).includes('demo') && (await page.isVisible('[data-testid=menubar-icon] img')), 'icon and title of the project')
  // The new project is a repository: commit it, add a branch, leave a change uncommitted.
  git('add -A')
  git('commit -q -m init')
  git('branch feature')
  fs.writeFileSync(WS + '/demo/wip.txt', 'not committed\n')
  await page.waitForFunction(() => document.querySelector('[data-testid=branch-selector]')?.textContent.includes('main'), null, { timeout: 5000 }).catch(() => {})
  assert((await page.textContent('[data-testid=branch-selector]')).includes('main'), 'current branch in the menu bar')
  assert((await page.title()) === 'demo · main', 'window title: ' + (await page.title()))

  // Open the branch "feature": a worktree in its own window, the main folder untouched.
  await page.click('[data-testid=branch-selector]')
  await page.waitForSelector('.pick-item:has-text("main folder")')
  assert(await page.isVisible('.pick-item:has-text("this window")'), 'selector: main folder, this window')
  await page.click('.pick-item:has-text("Open a branch")')
  await page.waitForSelector('.pick-item:has-text("feature")')
  const [win] = await Promise.all([ctx.waitForEvent('page'), page.click('.pick-item:has-text("feature")')])
  await win.waitForSelector('.menubar')
  await win.waitForFunction(() => document.querySelector('[data-testid=branch-selector]')?.textContent.includes('feature'), null, { timeout: 8000 }).catch(() => {})
  assert((await win.textContent('[data-testid=branch-selector]')).includes('feature') && (await win.textContent('.mb-title')).includes('demo'), 'worktree window: project title and its branch')
  assert(git('branch --show-current', WS + '/demo/.ide/worktrees/b-feature') === 'feature' && git('branch --show-current') === 'main', 'branch checked out in a worktree, main folder on main')
  assert(fs.readFileSync(WS + '/demo/wip.txt', 'utf8') === 'not committed\n', 'uncommitted change kept')
  await win.waitForFunction(() => decodeURIComponent(document.querySelector('link[rel="icon"]').href).includes('r="12"'), null, { timeout: 5000 }).catch(() => {})
  assert((await favicon(win)).includes('linearGradient') && (await favicon(win)).includes('r="12"'), 'worktree favicon: icon of the project with a dot')
  await win.screenshot({ path: OUT + '/worktree-window.png' })

  // From the worktree window, the list shows both; the main folder brings back its window.
  await win.click('[data-testid=branch-selector]')
  await win.waitForSelector('.pick-item:has-text("feature"):has-text("this window")')
  assert(await win.isVisible('.pick-item:has-text("main folder")'), 'selector of the worktree window')
  await win.screenshot({ path: OUT + '/branch-selector.png' })
  const pages = ctx.pages().length
  await win.click('.pick-item:has-text("main folder")')
  await win.waitForTimeout(500)
  assert(ctx.pages().length === pages, 'the window of the main folder is reused')
})
