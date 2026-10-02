// Git panel: status, diff tab, gutter markers, stage, commit, history, discard, branches.
const fs = require('fs')
const { execSync } = require('child_process')
const { run, openProject, open, assert, text, WS, OUT } = require('../common.cjs')

const repo = WS + '/demo'
const env = { ...process.env, GIT_AUTHOR_NAME: 'e2e', GIT_AUTHOR_EMAIL: 'e2e@x', GIT_COMMITTER_NAME: 'e2e', GIT_COMMITTER_EMAIL: 'e2e@x' }
const git = (cmd) => execSync(`git ${cmd}`, { cwd: repo, env, encoding: 'utf8' })
git('init -q -b main')
git('add -A')
git('commit -q -m "état initial"')

run(async ({ page }) => {
  page.on('dialog', (d) => d.accept())
  await openProject(page)
  await page.click('.rail-left .rail-btn[title="Git"]')
  await page.waitForSelector('.git-panel .git-branch')
  assert((await page.textContent('.git-branch')).includes('main'), 'branche courante affichée')
  const logged = await page.waitForSelector('.git-commit-item:has-text("état initial")', { timeout: 5000 }).then(() => true, () => false)
  assert(logged, "l'historique liste le commit")

  // A change made outside the IDE shows up.
  fs.writeFileSync(repo + '/notes.txt', 'line one\nline 2 (modifiée)\nline three\nline four\n')
  await page.waitForSelector('.git-row:has-text("notes.txt")', { timeout: 10000 })
  assert(true, 'fichier modifié listé')
  await page.click('.git-row:has-text("notes.txt")')
  await page.waitForSelector('.diff-view .diff')
  const del = await page.$$eval('.diff-line.del', (e) => e.map((x) => x.textContent))
  const add = await page.$$eval('.diff-line.add', (e) => e.map((x) => x.textContent))
  assert(del.includes('line two') && add.includes('line 2 (modifiée)') && add.includes('line four'), 'diff côte à côte : ' + JSON.stringify({ del, add }))
  assert((await page.textContent('.diff-stats')).includes('+2'), 'statistiques du diff')
  await page.screenshot({ path: OUT + '/git-diff.png' })

  // Gutter markers of an edited file.
  await open(page, 'main.go')
  await page.click('.pane.active .ed-content')
  await page.keyboard.press('Control+Home')
  await page.keyboard.press('End')
  await page.keyboard.type(' // modifié')
  await page.keyboard.press('Control+End')
  await page.keyboard.type('// ajout\n')
  await page.waitForSelector('.pane.active .ed-mark.mark-mod', { timeout: 5000 }).catch(() => {})
  assert(await page.isVisible('.pane.active .ed-mark.mark-mod'), 'marqueur de ligne modifiée')
  assert(await page.isVisible('.pane.active .ed-mark.mark-add'), 'marqueur de ligne ajoutée')
  await page.keyboard.press('Control+s')
  await page.waitForSelector('.tree-name.git-modified:has-text("main.go"), .git-row:has-text("main.go")', { timeout: 10000 })
  assert(await page.isVisible('.git-row:has-text("main.go")'), 'fichier enregistré listé')

  // Stage and commit notes.txt.
  await page.hover('.git-row:has-text("notes.txt")')
  await page.click('.git-row:has-text("notes.txt") button[title="Indexer"]')
  await page.waitForSelector('.git-section:has-text("Indexés") .git-row:has-text("notes.txt")', { timeout: 5000 })
  assert(true, 'fichier indexé')
  await page.fill('.git-commit textarea', 'notes : ligne 2')
  await page.press('.git-commit textarea', 'Control+Enter')
  await page.waitForSelector('.git-commit-item:has-text("notes : ligne 2")', { timeout: 8000 })
  assert(git('log -1 --pretty=%s').trim() === 'notes : ligne 2', 'commit créé dans le dépôt')
  const gone = await page.waitForFunction(() => ![...document.querySelectorAll('.git-row')].some((r) => r.textContent.includes('notes.txt')), null, { timeout: 5000 }).then(() => true, () => false)
  assert(gone, 'notes.txt ne figure plus dans les modifications')

  // Discard main.go: the file and the open buffer come back to HEAD.
  await page.hover('.git-row:has-text("main.go")')
  await page.click('.git-row:has-text("main.go") button[title="Annuler les modifications"]')
  await page.waitForFunction(() => !document.querySelector('.git-row'), null, { timeout: 8000 }).catch(() => {})
  assert(!fs.readFileSync(repo + '/src/main.go', 'utf8').includes('modifié'), 'modifications annulées sur le disque')
  await page.waitForFunction(() => !document.querySelector('.pane.active .ed-content')?.textContent.includes('// ajout'), null, { timeout: 5000 }).catch(() => {})
  assert(!(await text(page)).includes('// ajout'), 'le buffer ouvert suit')

  // New branch.
  await page.click('.git-branch button[title="Changer de branche"]')
  await page.waitForSelector('.pick-item')
  await page.click('.pick-item:has-text("Nouvelle branche")')
  await page.waitForSelector('.modal input')
  await page.fill('.modal input', 'feature/e2e')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.git-branch:has-text("feature/e2e")', { timeout: 5000 }).catch(() => {})
  assert(git('branch --show-current').trim() === 'feature/e2e', 'branche créée et active')
})
