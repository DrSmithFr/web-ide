// Git panel: status, diff tab, gutter markers, stage, commit, history, discard, branches.
const fs = require('fs')
const { execSync } = require('child_process')
const { run, openProject, open, assert, text, WS, OUT } = require('../common.cjs')

const repo = WS + '/demo'
const env = { ...process.env, GIT_AUTHOR_NAME: 'e2e', GIT_AUTHOR_EMAIL: 'e2e@x', GIT_COMMITTER_NAME: 'e2e', GIT_COMMITTER_EMAIL: 'e2e@x' }
const git = (cmd) => execSync(`git ${cmd}`, { cwd: repo, env, encoding: 'utf8' })
git('init -q -b main')
git('add -A')
git('commit -q -m "initial state"')

run(async ({ page }) => {
  page.on('dialog', (d) => d.accept())
  await openProject(page)
  await page.click('.rail-left .rail-btn[title="Git"]')
  await page.waitForSelector('.git-panel .git-branch')
  assert((await page.textContent('.git-branch')).includes('main'), 'current branch shown')
  const logged = await page.waitForSelector('.git-commit-item:has-text("initial state")', { timeout: 5000 }).then(() => true, () => false)
  assert(logged, 'the history lists the commit')

  // A change made outside the IDE shows up.
  fs.writeFileSync(repo + '/notes.txt', 'line one\nline 2 (changed)\nline three\nline four\n')
  await page.waitForSelector('.git-row:has-text("notes.txt")', { timeout: 10000 })
  assert(true, 'changed file listed')
  await page.click('.git-row:has-text("notes.txt")')
  await page.waitForSelector('.diff-view .diff')
  const del = await page.$$eval('.diff-line.del', (e) => e.map((x) => x.textContent))
  const add = await page.$$eval('.diff-line.add', (e) => e.map((x) => x.textContent))
  assert(del.includes('line two') && add.includes('line 2 (changed)') && add.includes('line four'), 'side-by-side diff: ' + JSON.stringify({ del, add }))
  assert((await page.textContent('.diff-stats')).includes('+2'), 'diff statistics')
  await page.screenshot({ path: OUT + '/git-diff.png' })

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
  await page.waitForSelector('.tree-name.git-modified:has-text("main.go"), .git-row:has-text("main.go")', { timeout: 10000 })
  assert(await page.isVisible('.git-row:has-text("main.go")'), 'saved file listed')

  // Stage and commit notes.txt.
  await page.hover('.git-row:has-text("notes.txt")')
  await page.click('.git-row:has-text("notes.txt") button[title="Stage"]')
  await page.waitForSelector('.git-section:has-text("Staged") .git-row:has-text("notes.txt")', { timeout: 5000 })
  assert(true, 'file staged')
  await page.fill('.git-commit textarea', 'notes: line 2')
  await page.press('.git-commit textarea', 'Control+Enter')
  await page.waitForSelector('.git-commit-item:has-text("notes: line 2")', { timeout: 8000 })
  assert(git('log -1 --pretty=%s').trim() === 'notes: line 2', 'commit created in the repository')
  const gone = await page.waitForFunction(() => ![...document.querySelectorAll('.git-row')].some((r) => r.textContent.includes('notes.txt')), null, { timeout: 5000 }).then(() => true, () => false)
  assert(gone, 'notes.txt is not in the changes anymore')

  // Discard main.go: the file and the open buffer come back to HEAD.
  await page.hover('.git-row:has-text("main.go")')
  await page.click('.git-row:has-text("main.go") button[title="Discard the changes"]')
  await page.waitForFunction(() => !document.querySelector('.git-row'), null, { timeout: 8000 }).catch(() => {})
  assert(!fs.readFileSync(repo + '/src/main.go', 'utf8').includes('changed'), 'changes discarded on disk')
  await page.waitForFunction(() => !document.querySelector('.pane.active .ed-content')?.textContent.includes('// added'), null, { timeout: 5000 }).catch(() => {})
  assert(!(await text(page)).includes('// added'), 'the open buffer follows')

  // New branch.
  await page.click('.git-branch button[title="Switch branch"]')
  await page.waitForSelector('.pick-item')
  await page.click('.pick-item:has-text("New branch")')
  await page.waitForSelector('.modal input')
  await page.fill('.modal input', 'feature/e2e')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.git-branch:has-text("feature/e2e")', { timeout: 5000 }).catch(() => {})
  assert(git('branch --show-current').trim() === 'feature/e2e', 'branch created and active')
})
