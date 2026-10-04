// Git tool: Commit tab (tree of changes with check boxes, diff, commit, commit and push),
// History tab (graph, commit detail, actions), discard, branches.
const fs = require('fs')
const { execSync } = require('child_process')
const { run, openProject, open, assert, text, WS, OUT } = require('../common.cjs')

const repo = WS + '/demo'
const origin = WS + '/../origin.git'
const env = { ...process.env, GIT_AUTHOR_NAME: 'e2e', GIT_AUTHOR_EMAIL: 'e2e@x', GIT_COMMITTER_NAME: 'e2e', GIT_COMMITTER_EMAIL: 'e2e@x' }
const git = (cmd, cwd = repo) => execSync(`git ${cmd}`, { cwd, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
git('init -q -b main')
git('add -A')
git('commit -q -m "initial state"')
git(`init -q --bare -b main ${origin}`, WS)
git(`remote add origin ${origin}`)

const row = (name) => `.git-tree .tree-row:has(.tree-name:text-is("${name}"))`
const box = (name) => `.git-tree .tree-row[data-path$="/${name}"] .git-check`
const checkState = (page, name) =>
  page.$eval(box(name), (c) => (c.indeterminate ? 'mixed' : c.checked ? 'on' : 'off')).catch(() => 'none')
const waitCheck = (page, name, want) =>
  page
    .waitForFunction(
      ([sel, want]) => {
        const c = document.querySelector(sel)
        return c && (c.indeterminate ? 'mixed' : c.checked ? 'on' : 'off') === want
      },
      [box(name), want],
      { timeout: 8000 },
    )
    .then(() => true, () => false)
const menu = async (page, sel, label) => {
  await page.click(sel, { button: 'right' })
  await page.click(`.ctx-menu .ctx-item:has-text("${label}")`)
}
const until = async (fn, ms = 10000) => {
  for (const end = Date.now() + ms; Date.now() < end; await new Promise((r) => setTimeout(r, 200))) if (fn()) return true
  return false
}

run(async ({ page }) => {
  page.on('dialog', (d) => (d.type() === 'prompt' ? d.accept('') : d.accept()))
  await openProject(page)
  await page.click('.rail-left .rail-btn[title="Git"]')
  await page.waitForSelector('.git-panel .git-branch')
  assert((await page.textContent('.git-branch')).includes('main'), 'current branch shown')
  assert(await page.isVisible('[data-testid=git-tab-commit]') && (await page.isVisible('[data-testid=git-tab-history]')), 'Commit and History tabs')

  // A change made outside the IDE shows up in the tree; a click opens the diff against HEAD.
  fs.writeFileSync(repo + '/notes.txt', 'line one\nline 2 (changed)\nline three\nline four\n')
  await page.waitForSelector(row('notes.txt'), { timeout: 10000 })
  assert(true, 'changed file listed')
  assert((await checkState(page, 'notes.txt')) === 'off', 'unstaged file: box unchecked')
  await page.click(row('notes.txt'))
  await page.waitForSelector('.diff-view .diff')
  const del = await page.$$eval('.diff-line.del', (e) => e.map((x) => x.textContent))
  const add = await page.$$eval('.diff-line.add', (e) => e.map((x) => x.textContent))
  assert(del.includes('line two') && add.includes('line 2 (changed)') && add.includes('line four'), 'side-by-side diff: ' + JSON.stringify({ del, add }))
  assert((await page.textContent('.diff-stats')).includes('+2'), 'diff statistics')
  assert((await page.textContent('.diff-view .toolbar')).includes('working tree ↔ HEAD'), 'diff against HEAD')

  // Gutter markers of an edited file.
  await open(page, 'main.go')
  await page.click('.pane.active .ed-content')
  await page.keyboard.press('Control+Home')
  await page.keyboard.press('End')
  await page.keyboard.type(' // changed')
  await page.keyboard.press('Control+End')
  await page.keyboard.type('// added\n')
  await page.waitForSelector('.pane.active .ed-mark.mark-mod', { timeout: 5000 }).catch(() => {})
  assert(await page.isVisible('.pane.active .ed-mark.mark-mod'), 'changed line marker')
  assert(await page.isVisible('.pane.active .ed-mark.mark-add'), 'added line marker')
  await page.keyboard.press('Control+s')
  await page.waitForSelector(row('main.go'), { timeout: 10000 }).catch(() => {})
  assert(await page.isVisible(row('main.go')), 'saved file listed under its folder')
  assert(await page.isVisible('.git-tree .tree-row:has(.tree-name:text-is("src"))'), 'folder row')

  // Single folders are joined on one row; untracked files are red.
  fs.mkdirSync(repo + '/a/b', { recursive: true })
  fs.writeFileSync(repo + '/a/b/c.txt', 'c\n')
  await page.waitForSelector(row('c.txt'), { timeout: 10000 })
  assert(await page.isVisible('.git-tree .tree-row:has(.tree-name:text-is("a/b"))'), 'compacted folders: a/b')
  assert(await page.isVisible(`${row('c.txt')} .tree-name.git-untracked`), 'untracked color')
  await page.screenshot({ path: OUT + '/git-changes.png' })

  // The root box stages everything, then unstages everything.
  await page.click('.git-tree .tree-top .git-check')
  assert(await waitCheck(page, 'c.txt', 'on'), 'root box: all staged')
  assert(git('diff --cached --name-only').includes('a/b/c.txt'), 'untracked file added to the index')
  await page.click('.git-tree .tree-top .git-check')
  assert(await waitCheck(page, 'notes.txt', 'off'), 'root box: all unstaged')

  // A box stages one file; the parent folder shows a partial state.
  await page.click(`${row('notes.txt')} .git-check`)
  assert(await waitCheck(page, 'notes.txt', 'on'), 'file staged')
  const top = await page.$eval('.git-tree .tree-top .git-check', (c) => c.indeterminate)
  assert(top, 'root box partially checked')
  await page.fill('.git-commit textarea', 'notes: line 2')
  await page.press('.git-commit textarea', 'Control+Enter')
  assert(await until(() => git('log -1 --pretty=%s').trim() === 'notes: line 2'), 'commit created in the repository')
  const gone = await page.waitForFunction(() => !document.querySelector('.git-tree .tree-row[data-path$="/notes.txt"]'), null, { timeout: 5000 }).then(() => true, () => false)
  assert(gone, 'notes.txt is not in the changes anymore')

  // Discard main.go and the untracked file from the menu: the open buffer follows.
  await menu(page, row('main.go'), 'Discard the changes')
  await menu(page, row('c.txt'), 'Discard the changes')
  await page.waitForFunction(() => !document.querySelector('.git-tree'), null, { timeout: 8000 }).catch(() => {})
  assert(!fs.readFileSync(repo + '/src/main.go', 'utf8').includes('changed'), 'changes discarded on disk')
  assert(!fs.existsSync(repo + '/a/b/c.txt'), 'untracked file deleted')
  await page.waitForFunction(() => !document.querySelector('.pane.active .ed-content')?.textContent.includes('// added'), null, { timeout: 5000 }).catch(() => {})
  assert(!(await text(page)).includes('// added'), 'the open buffer follows')

  // Commit and push: a branch without upstream is pushed to origin with tracking.
  fs.writeFileSync(repo + '/notes.txt', 'pushed\n')
  await page.waitForSelector(row('notes.txt'), { timeout: 10000 })
  await page.click(`${row('notes.txt')} .git-check`)
  await waitCheck(page, 'notes.txt', 'on')
  await page.fill('.git-commit textarea', 'pushed commit')
  await page.click('.git-commit button:has-text("Commit and push")')
  const head = () => git('rev-parse HEAD').trim()
  const remoteMain = () => {
    try {
      return git(`--git-dir=${origin} rev-parse main`).trim()
    } catch {
      return ''
    }
  }
  assert(await until(() => remoteMain() !== '' && remoteMain() === head(), 15000), 'commit pushed to origin')
  assert(await until(() => git('rev-parse --abbrev-ref main@{upstream}').trim() === 'origin/main'), 'upstream set')

  // Amend starts from the last message; force-with-lease pushes the rewritten commit.
  await page.click('.git-commit label:has-text("Amend") input')
  await page.waitForFunction(() => document.querySelector('.git-commit textarea').value === 'pushed commit', null, { timeout: 5000 }).catch(() => {})
  assert((await page.inputValue('.git-commit textarea')) === 'pushed commit', 'amend fills the last message')
  await page.fill('.git-commit textarea', 'pushed commit (amended)')
  await page.click('.git-commit .btn-group button[title="Force the push"]')
  await page.click('.ctx-menu .ctx-item:has-text("--force-with-lease")')
  assert(await until(() => git(`--git-dir=${origin} log -1 --pretty=%s main`).trim() === 'pushed commit (amended)', 15000), 'amended commit force-pushed')

  // History: graph of the current branch, refs, detail of the selected commit.
  git('switch -q -c side HEAD~1')
  fs.writeFileSync(repo + '/side.txt', 'side\n')
  git('add side.txt')
  git('commit -q -m "on side"')
  git('switch -q main')
  git('merge -q --no-ff -m "merge side" side')
  await page.click('[data-testid=git-tab-history]')
  await page.waitForSelector('.git-log-row:has-text("merge side")', { timeout: 8000 })
  const subjects = await page.$$eval('.git-log-row .git-subject', (e) => e.map((x) => x.textContent))
  assert(subjects.join('|') === 'merge side|on side|pushed commit (amended)|notes: line 2|initial state', 'history in graph order: ' + subjects.join('|'))
  assert((await page.$$('.git-log-row .git-graph circle')).length === 5, 'one graph node per commit')
  const lanes = await page.$eval('.git-log-row:has-text("on side") .git-graph', (s) => s.querySelectorAll('path').length)
  assert(lanes >= 2, 'the merged branch runs beside main')
  assert(await page.isVisible('.git-log-row:has-text("merge side") .git-ref.ref-head:text-is("main")'), 'HEAD -> main ref')
  await page.screenshot({ path: OUT + '/git-history.png' })

  await page.click('.git-log-row:has-text("on side")')
  await page.waitForSelector('[data-testid=git-detail] .git-message', { timeout: 5000 })
  assert((await page.textContent('[data-testid=git-detail] .git-message')).trim() === 'on side', 'detail: message')
  assert((await page.textContent('[data-testid=git-detail]')).includes('e2e <e2e@x>'), 'detail: author')
  await page.click('[data-testid=git-commit-files] .tree-row:has(.tree-name:text-is("side.txt"))')
  await page.waitForSelector('.diff-view .diff-line.add', { timeout: 5000 })
  assert((await page.textContent('.diff-view .toolbar')).includes('its parent'), 'commit file diff against the parent')
  assert((await page.$$eval('.diff-line.add', (e) => e.map((x) => x.textContent))).includes('side'), 'commit diff content')

  // Search: no graph, matching commits only.
  await page.fill('.git-search input', 'amended')
  await page.waitForFunction(() => document.querySelectorAll('.git-log-row').length === 1, null, { timeout: 5000 }).catch(() => {})
  assert((await page.$$('.git-log-row')).length === 1 && !(await page.$('.git-log-row .git-graph')), 'search filters the history')
  await page.fill('.git-search input', '')
  await page.waitForSelector('.git-log-row:has-text("merge side")')

  // Actions: revert, new branch here, reset.
  await menu(page, '.git-log-row:has-text("on side")', 'Revert')
  assert(await until(() => git('log -1 --pretty=%s').startsWith('Revert "on side"')), 'revert commit created')
  assert(!fs.existsSync(repo + '/side.txt'), 'revert undid the file')
  await page.waitForSelector('.git-log-row:has-text("Revert")', { timeout: 8000 })
  await page.click('.git-log-row:has-text("merge side")')
  await menu(page, '.git-log-row:has-text("merge side")', 'Reset here (hard)')
  assert(await until(() => git('log -1 --pretty=%s').trim() === 'merge side'), 'hard reset')
  await page.click('.git-log-row:has-text("initial state")')
  await page.click('[data-testid=git-detail] button[title="New branch here…"]')
  await page.waitForSelector('.modal input')
  await page.fill('.modal input', 'from-initial')
  await page.keyboard.press('Enter')
  assert(await until(() => git('branch --show-current').trim() === 'from-initial'), 'branch created at the commit')
  assert(git('rev-parse HEAD').trim() === git('rev-list --max-parents=0 main').trim(), 'new branch on the selected commit')

  // New branch from the branch bar.
  await page.click('.git-branch button[title="Switch branch"]')
  await page.waitForSelector('.pick-item')
  await page.click('.pick-item:has-text("New branch")')
  await page.waitForSelector('.modal input')
  await page.fill('.modal input', 'feature/e2e')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.git-branch:has-text("feature/e2e")', { timeout: 5000 }).catch(() => {})
  assert(git('branch --show-current').trim() === 'feature/e2e', 'branch created and active')

  // A new project is a git repository, with the optional remote as origin.
  const fresh = WS + '/fresh'
  fs.mkdirSync(fresh)
  await page.click('.menubar button[title="Projects"]')
  await page.click('.home-bar button:has-text("New project")')
  await page.fill('.modal .field:has-text("Folder") input', fresh)
  await page.fill('[data-testid=project-remote]', 'git@example.com:me/fresh.git')
  await page.click('.modal button:has-text("Create and open")')
  await page.waitForSelector('.menubar')
  const git2 = (cmd) => execSync(`git ${cmd}`, { cwd: fresh, env, encoding: 'utf8' }).trim()
  assert(fs.existsSync(fresh + '/.git') && git2('symbolic-ref --short HEAD') === 'main', 'new project: repository on main')
  assert(git2('remote get-url origin') === 'git@example.com:me/fresh.git', 'new project: remote origin')
})
