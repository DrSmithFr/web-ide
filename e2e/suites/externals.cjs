// External plugins, step 1 (#128): installed from a git repository (a local one made here, its
// release archive served by the suite) in Settings > Plugins, with the permissions accepted one
// by one; listed, not runnable yet; used by the project; updated to a version asking a new
// permission (asked again); a version for another IDE refused; removed.
const fs = require('fs')
const os = require('os')
const path = require('path')
const http = require('http')
const { execFileSync } = require('child_process')
const { run, openProject, assert, WS } = require('../common.cjs')

const lockFile = path.join(WS, '..', 'data', 'plugins.lock')
const readLock = () => (fs.existsSync(lockFile) ? JSON.parse(fs.readFileSync(lockFile, 'utf8')).plugins : {})

run(async ({ page }) => {
  // The release archives, by version (any architecture).
  const assets = {}
  const server = http.createServer((req, res) => {
    const v = req.url.split('/')[1]
    if (!assets[v]) return res.writeHead(404).end()
    res.writeHead(200).end(assets[v])
  })
  await new Promise((r) => server.listen(0, '127.0.0.1', r))
  const base = `http://127.0.0.1:${server.address().port}`

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'e2e-externals-'))
  const repo = path.join(tmp, 'greet')
  const git = (...args) =>
    execFileSync('git', args, { cwd: repo, env: { ...process.env, GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@x', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@x', GIT_CONFIG_GLOBAL: '/dev/null' } })
  fs.mkdirSync(repo)
  git('init', '-q', '-b', 'main')
  const release = (version, extra) => {
    const manifest = JSON.stringify(
      { id: 'greet', name: 'Greet', version, apiVersion: 1, assets: base + '/{version}/greet-{arch}.tar.gz', contributes: { tools: [{ id: 'greet', label: 'Greet', icon: 'sparkle', zone: 'right' }] }, ...extra },
      null,
      2,
    )
    fs.writeFileSync(path.join(repo, 'plugin.json'), manifest)
    git('add', '-A')
    git('commit', '-q', '-m', version)
    git('tag', 'v' + version)
    const dir = path.join(tmp, 'build-' + version)
    fs.mkdirSync(path.join(dir, 'web'), { recursive: true })
    fs.writeFileSync(path.join(dir, 'plugin.json'), manifest)
    fs.writeFileSync(path.join(dir, 'web', 'index.js'), 'export {}')
    execFileSync('tar', ['czf', path.join(tmp, version + '.tar.gz'), '-C', dir, '.'])
    assets[version] = fs.readFileSync(path.join(tmp, version + '.tar.gz'))
  }
  release('1.0.0', { ide: '^1.0', permissions: ['files:read'] })

  await openProject(page)
  page.on('dialog', (d) => d.accept())
  await page.click('.menubar button[title^="Settings"]')
  await page.click('.settings-nav button:has-text("Plugins")')

  // Find: the dialog shows the version and its permission, unchecked; installed with it.
  await page.fill('[data-testid=plugin-install-url]', repo)
  await page.click('[data-testid=plugin-install-find]')
  await page.waitForSelector('.plugin-grants')
  const dialog = await page.textContent('.plugin-grants')
  assert(dialog.includes('Greet') && dialog.includes('1.0.0') && dialog.includes('Read the files of the project'), 'the grants dialog: ' + dialog)
  assert(!(await page.isChecked('[data-testid="plugin-grant-files:read"]')), 'a permission is not accepted by default')
  await page.check('[data-testid="plugin-grant-files:read"]')
  await page.click('[data-testid=plugin-grants-accept]')
  await page.waitForSelector('[data-testid=plugin-external-greet]')
  const row = await page.textContent('[data-plugin=greet]')
  assert(row.includes('External') && row.includes('Not runnable yet') && row.includes('files:read') && row.includes(repo), 'the external row: ' + row)
  let lock = readLock()
  assert(lock.greet?.version === '1.0.0' && lock.greet.grants.join() === 'files:read' && lock.greet.sha256.length === 64 && lock.greet.url === repo, 'the lock: ' + JSON.stringify(lock))
  assert(fs.existsSync(path.join(WS, '..', 'data', 'plugins', 'greet', '1.0.0', 'web', 'index.js')), 'extracted')
  assert(!(await page.$('.rail-btn[data-id=greet]')), 'not runnable yet: no tool')

  // Used by the project: listed in .ide/plugins.json with its repository.
  await page.check('[data-testid=plugin-greet]')
  await page.waitForTimeout(300)
  const choices = JSON.parse(fs.readFileSync(WS + '/demo/.ide/plugins.json', 'utf8'))
  assert(choices.external?.greet === repo, 'used by the project: ' + JSON.stringify(choices))

  // A new version asks the network: the update asks it again.
  release('1.1.0', { ide: '^1.0', permissions: ['files:read', 'net'] })
  await page.click('[data-testid=plugin-update-greet]')
  await page.waitForSelector('.plugin-grants')
  const asked = await page.textContent('.plugin-grants')
  assert(asked.includes('1.1.0') && asked.includes('Reach the network') && !asked.includes('Read the files'), 'only the new permission is asked: ' + asked)
  await page.check('[data-testid=plugin-grant-net]')
  await page.click('[data-testid=plugin-grants-accept]')
  await page.waitForFunction(() => document.querySelector('[data-plugin=greet]')?.textContent.includes('1.1.0'))
  lock = readLock()
  assert(lock.greet.version === '1.1.0' && lock.greet.grants.join() === 'files:read,net', 'updated in the lock: ' + JSON.stringify(lock.greet))
  assert(!fs.existsSync(path.join(WS, '..', 'data', 'plugins', 'greet', '1.0.0')), 'the old version removed')

  // Up to date now; a version for another IDE is refused with its reason.
  await page.click('[data-testid=plugin-update-greet]')
  await page.waitForSelector('.toast:has-text("Greet is up to date")')
  release('2.0.0', { ide: '^99' })
  await page.fill('[data-testid=plugin-install-constraint]', '^2')
  await page.click('[data-testid=plugin-install-find]')
  await page.waitForSelector('.toast-error:has-text("needs the IDE ^99")')

  // Removed: the row, the lock entry and the folder go.
  await page.click('[data-testid=plugin-remove-greet]')
  await page.waitForSelector('[data-testid=plugin-external-greet] >> text=Not installed on this pod.')
  assert(!readLock().greet && !fs.existsSync(path.join(WS, '..', 'data', 'plugins', 'greet')), 'removed from the pod')
  // The project still names it: shown as not installed here, with Install.
  assert(await page.isVisible('[data-plugin=greet] button:has-text("Install")'), 'a plugin of the project missing on this pod')

  server.close()
  fs.rmSync(tmp, { recursive: true, force: true })
})
