// Projects: generated icon (home page, favicon), icon editor; menu bar with the selector of
// the worktrees, a branch opened in its own worktree and window.
const fs = require('fs')
const { execSync } = require('child_process')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

const favicon = (page) => page.evaluate(() => decodeURIComponent(document.querySelector('link[rel="icon"]').href))

const env = { ...process.env, GIT_AUTHOR_NAME: 'e2e', GIT_AUTHOR_EMAIL: 'e2e@x', GIT_COMMITTER_NAME: 'e2e', GIT_COMMITTER_EMAIL: 'e2e@x' }
const git = (cmd, cwd = WS + '/demo') => execSync(`git ${cmd}`, { cwd, env, encoding: 'utf8' }).trim()

run(async ({ page, ctx }) => {
  // The home page shows the version of the pod (git describe of the build).
  await page.waitForFunction(() => document.querySelector('[data-testid=pod-version]')?.textContent)
  const version = execSync(`${__dirname}/../../bin/web-ide-pod -version`, { encoding: 'utf8' }).trim()
  assert((await page.textContent('[data-testid=pod-version]')) === version, 'home page: version of the pod')
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

  // The setup command of the kanban runs in the new worktrees.
  await page.click('.rail-left .rail-btn[title="Kanban"]')
  await page.click('[data-testid=kanban-open-board]')
  await page.click('[data-testid=kanban-settings]')
  await page.fill('[data-testid=kanban-setup]', 'echo installed > setup.log')
  await page.click('[data-testid=kanban-settings-save]')

  // Open the branch "feature": a worktree shown in the same window, the main folder untouched.
  fs.writeFileSync(WS + '/demo/main.txt', 'main\n')
  await page.click('[data-testid=branch-selector]')
  await page.waitForSelector('.pick-item:has-text("main folder")')
  assert(await page.isVisible('.pick-item:has-text("main folder"):has-text("shown")'), 'selector: main folder, shown')
  await page.click('.pick-item:has-text("Open a branch")')
  await page.waitForSelector('.pick-item:has-text("feature")')
  const pages = ctx.pages().length
  await page.click('.pick-item:has-text("feature")')
  await page.waitForFunction(() => document.querySelector('[data-testid=branch-selector]')?.textContent.includes('feature'), null, { timeout: 8000 }).catch(() => {})
  assert((await page.textContent('[data-testid=branch-selector]')).includes('feature') && (await page.textContent('.mb-title')).includes('demo'), 'worktree shown: project title and its branch')
  assert(ctx.pages().length === pages && !page.url().includes('-w'), 'same window, no reload')
  assert(git('branch --show-current', WS + '/demo/.ide/worktrees/b-feature') === 'feature' && git('branch --show-current') === 'main', 'branch checked out in a worktree, main folder on main')
  assert(fs.readFileSync(WS + '/demo/wip.txt', 'utf8') === 'not committed\n', 'uncommitted change kept')
  await page.waitForFunction(() => decodeURIComponent(document.querySelector('link[rel="icon"]').href).includes('r="12"'), null, { timeout: 5000 }).catch(() => {})
  assert((await favicon(page)).includes('linearGradient') && (await favicon(page)).includes('r="12"'), 'worktree favicon: icon of the project with a dot')
  const wt = WS + '/demo/.ide/worktrees/b-feature'
  const deadline2 = Date.now() + 8000
  while (!fs.existsSync(wt + '/setup.log') && Date.now() < deadline2) await page.waitForTimeout(100)
  assert(fs.existsSync(wt + '/setup.log'), 'setup command run in the new worktree')
  await page.waitForSelector('.console-tabs .tab:has-text("Worktree setup") [data-testid=worktree-chip]:has-text("feature")', { timeout: 5000 })
  assert(true, 'the setup console carries the chip of the worktree')

  // The explorer shows the worktree; its files open with a chip, and stay open in the main folder.
  fs.writeFileSync(wt + '/feature.txt', 'feature\n')
  await page.click('.rail-left .rail-btn[title="Explorer"]')
  await page.waitForSelector(`.explorer .tree-row[data-path="${wt}/feature.txt"]`, { timeout: 5000 })
  await page.dblclick(`.explorer .tree-row[data-path="${wt}/feature.txt"]`)
  await page.waitForSelector('.tab:has-text("feature.txt") [data-testid=worktree-chip]:has-text("feature")')
  assert(true, 'file of the worktree: chip with its branch')
  await page.screenshot({ path: OUT + '/worktree-window.png' })
  await page.click('[data-testid=branch-selector]')
  await page.waitForSelector('.pick-item:has-text("feature"):has-text("shown")')
  await page.screenshot({ path: OUT + '/branch-selector.png' })
  await page.click('.pick-item:has-text("main folder")')
  await page.waitForSelector(`.explorer .tree-row[data-path="${WS}/demo/main.txt"]`, { timeout: 5000 })
  await page.dblclick(`.explorer .tree-row[data-path="${WS}/demo/main.txt"]`)
  await page.waitForSelector('.tab.active:has-text("main.txt")')
  assert(!(await page.isVisible('.tab:has-text("main.txt") [data-testid=worktree-chip]')) && (await page.isVisible('.tab:has-text("feature.txt") [data-testid=worktree-chip]')), 'both files open, chip only on the worktree one')
  assert((await page.textContent('[data-testid=branch-selector]')).includes('main') && ctx.pages().length === pages, 'back on the main folder in the same window')
  // An edit saved in the worktree file goes to the worktree.
  await page.click('.tab:has-text("feature.txt")')
  await page.click('.pane.active .ed-content')
  await page.keyboard.press('Control+End')
  await page.keyboard.type('edited')
  await page.keyboard.press('Control+s')
  const deadline3 = Date.now() + 5000
  while (!fs.readFileSync(wt + '/feature.txt', 'utf8').includes('edited') && Date.now() < deadline3) await page.waitForTimeout(100)
  assert(fs.readFileSync(wt + '/feature.txt', 'utf8').includes('edited'), 'file of the worktree saved in the worktree')

  // A reload keeps the worktree shown and its tabs.
  await page.click('[data-testid=branch-selector]')
  await page.click('.pick-item:has-text("feature")')
  await page.waitForFunction(() => document.querySelector('[data-testid=branch-selector]')?.textContent.includes('feature'), null, { timeout: 5000 })
  await page.waitForTimeout(600) // the session is saved after 400 ms
  await page.reload()
  await page.waitForFunction(() => document.querySelector('[data-testid=branch-selector]')?.textContent.includes('feature'), null, { timeout: 8000 }).catch(() => {})
  assert((await page.textContent('[data-testid=branch-selector]')).includes('feature'), 'worktree shown again after a reload')
  await page.waitForSelector('.tab:has-text("feature.txt") [data-testid=worktree-chip]', { timeout: 5000 })
  assert(await page.isVisible('.tab:has-text("main.txt")'), 'tabs of both worktrees restored')

  // A worktree may still open in its own window.
  await page.click('[data-testid=branch-selector]')
  await page.click('.pick-item:has-text("Open in a new window")')
  await page.waitForSelector('.pick-item:has-text("feature")')
  const [win] = await Promise.all([ctx.waitForEvent('page'), page.click('.pick-item:has-text("feature")')])
  await win.waitForSelector('.menubar')
  await win.waitForFunction(() => document.querySelector('[data-testid=branch-selector]')?.textContent.includes('feature'), null, { timeout: 8000 }).catch(() => {})
  assert((await win.textContent('[data-testid=branch-selector]')).includes('feature') && win.url().includes('-w'), 'worktree in its own window')
  await win.close()

  // Remove the worktree shown (uncommitted changes: confirmed): the window goes back to the
  // main folder and closes its tabs, the branch is kept.
  page.on('dialog', (d) => d.accept())
  await page.click('[data-testid=branch-selector]')
  await page.click('.pick-item:has-text("Remove a worktree")')
  await page.waitForSelector('.pick-item:has-text("feature")')
  await page.click('.pick-item:has-text("feature")')
  await page.waitForFunction(() => document.querySelector('[data-testid=branch-selector]')?.textContent.includes('main'), null, { timeout: 8000 }).catch(() => {})
  assert(!fs.existsSync(wt) && git('branch --list feature') !== '', 'worktree removed, branch kept')
  assert((await page.textContent('[data-testid=branch-selector]')).includes('main') && ctx.pages().length === pages, 'the window is back on the main folder')
  await page.waitForSelector('.tab:has-text("feature.txt")', { state: 'detached', timeout: 5000 })
  assert(await page.isVisible('.tab:has-text("main.txt")'), 'tabs of the worktree closed, the others kept')
})
