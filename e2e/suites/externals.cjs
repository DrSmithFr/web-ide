// External plugins (#128, #129, #130): installed from a git repository (a local one made here,
// its release archive served by the suite) in Settings > Plugins, with the permissions accepted
// one by one; used by the project, its tool, tab view and command run in sandboxed frames: no
// storage, no network, no page; a method of the core only with its grant; the shortcuts of the
// IDE work from the frame. Its backend (a Go program built here) runs confined: it answers its
// frame and emits to it, its assistant tool answers through the MCP, it cannot read the pod's
// key nor reach the network, it reads the project (files:read), the core refuses it fs.list. Updated to a version asking new permissions (asked again); a
// version for another IDE refused; removed, then installed again with the grant.
const fs = require('fs')
const os = require('os')
const path = require('path')
const http = require('http')
const { execFileSync } = require('child_process')
const { run, openProject, assert, mcp, WS } = require('../common.cjs')

const lockFile = path.join(WS, '..', 'data', 'plugins.lock')
const readLock = () => (fs.existsSync(lockFile) ? JSON.parse(fs.readFileSync(lockFile, 'utf8')).plugins : {})
const row = '.plugin-row[data-plugin=greet]'
const frame = (page, view) => page.frameLocator(`iframe.plugin-frame[data-view=${view}]`)

// The page module of the plugin: its tool checks what the frame reaches, its tab view shows its
// state, its command toasts.
const bundle = (base) => `
ide.register({
  views: {
    greet: async (el, ide) => {
      const out = {}
      try { localStorage.getItem('x'); out.localStorage = 'open' } catch { out.localStorage = 'blocked' }
      try { await new Promise((ok, ko) => { const r = indexedDB.open('x'); r.onsuccess = ok; r.onerror = () => ko(r.error) }); out.indexedDB = 'open' } catch { out.indexedDB = 'blocked' }
      try { await fetch('${base}/1.0.0/x'); out.fetch = 'open' } catch { out.fetch = 'blocked' }
      try { void parent.document.title; out.parent = 'open' } catch { out.parent = 'blocked' }
      try { await ide.rpc('fs.list', { path: '' }); out.rpc = 'answered' } catch (e) { out.rpc = 'refused: ' + e.message }
      el.innerHTML = '<p id="hello">' + ide.t('Hello from the frame') + '</p><pre id="checks">' + JSON.stringify(out) + '</pre><button id="open">open</button><button id="view">view</button>'
      el.innerHTML += '<button id="backend">backend</button><button id="probe">probe</button><button id="core">core</button><pre id="answer"></pre><pre id="event"></pre>'
      const show = (p) => p.then((r) => (el.querySelector('#answer').textContent = typeof r === 'string' ? r : JSON.stringify(r)), (e) => (el.querySelector('#answer').textContent = 'error: ' + e.message))
      ide.on('greet.said', (d) => (el.querySelector('#event').textContent = d))
      el.querySelector('#backend').onclick = () => show(ide.rpc('greet.hello'))
      el.querySelector('#probe').onclick = () => show(ide.rpc('greet.probe', { key: ${JSON.stringify(path.join(WS, '..', 'data', 'device.key'))} }))
      el.querySelector('#core').onclick = () => show(ide.rpc('greet.core'))
      el.querySelector('#open').onclick = () => ide.openFile('notes.txt')
      el.querySelector('#view').onclick = () => ide.openView('note', { text: 'kept' }, 'Note')
    },
    note: (el, ide) => { el.innerHTML = '<p id="note">' + ((ide.view.state && ide.view.state.text) || '') + '</p>' },
  },
  commands: { hello: (ide) => ide.toast('Command of Greet') },
})
`

run(async ({ page, errors }) => {
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
  const contributes = {
    tools: [{ id: 'greet', label: 'Greet', icon: 'sparkle', zone: 'right' }],
    tabViews: [{ id: 'note', label: 'Note' }],
    commands: [{ id: 'hello', label: 'Say hello from Greet' }],
    assistantTools: ['greet_hello'],
    toolSchemas: { greet_hello: { description: 'Says hello from the backend of Greet', properties: {} } },
  }
  // Its backend, a static Go program.
  const goBin = [process.env.GO, path.join(os.homedir(), 'sdk/go/bin/go'), 'go'].find((g) => g && (g === 'go' || fs.existsSync(g)))
  const backendBin = path.join(tmp, 'greet-backend')
  execFileSync(goBin, ['build', '-o', backendBin, path.join(__dirname, '..', 'fixtures', 'greet-backend', 'main.go')], { env: { ...process.env, CGO_ENABLED: '0' } })
  const release = (version, extra) => {
    const manifest = JSON.stringify({ id: 'greet', name: 'Greet', version, apiVersion: 1, assets: base + '/{version}/greet-{arch}.tar.gz', backend: 'bin/greet', contributes, ...extra }, null, 2)
    fs.writeFileSync(path.join(repo, 'plugin.json'), manifest)
    git('add', '-A')
    git('commit', '-q', '-m', version)
    git('tag', 'v' + version)
    const dir = path.join(tmp, 'build-' + version)
    fs.mkdirSync(path.join(dir, 'web'), { recursive: true })
    fs.mkdirSync(path.join(dir, 'i18n'), { recursive: true })
    fs.writeFileSync(path.join(dir, 'plugin.json'), manifest)
    fs.writeFileSync(path.join(dir, 'web', 'index.js'), bundle(base))
    fs.mkdirSync(path.join(dir, 'bin'), { recursive: true })
    fs.copyFileSync(backendBin, path.join(dir, 'bin', 'greet'))
    fs.chmodSync(path.join(dir, 'bin', 'greet'), 0o755)
    fs.writeFileSync(path.join(dir, 'i18n', 'fr.json'), JSON.stringify({ 'Hello from the frame': 'Bonjour depuis le cadre' }))
    execFileSync('tar', ['czf', path.join(tmp, version + '.tar.gz'), '-C', dir, '.'])
    assets[version] = fs.readFileSync(path.join(tmp, version + '.tar.gz'))
  }
  release('1.0.0', { ide: '^1.0', permissions: ['files:read'] })

  await openProject(page)
  page.on('dialog', (d) => d.accept())
  const openSettings = async () => {
    await page.click('.menubar button[title^="Settings"]')
    await page.click('.settings-nav button:has-text("Plugins")')
  }
  const closeSettings = () => page.keyboard.press('Escape')
  // Its tool shown (a desk restored after a reload may show it already: a click would hide it).
  const showGreet = async () => {
    await page.waitForSelector('.rail-btn[data-id=greet]')
    if (!(await page.isVisible('iframe.plugin-frame[data-view=greet]'))) await page.click('.rail-btn[data-id=greet]')
  }
  await openSettings()

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
  const text = await page.textContent(row)
  assert(text.includes('External') && text.includes('files:read') && text.includes(repo), 'the external row: ' + text)
  let lock = readLock()
  assert(lock.greet?.version === '1.0.0' && lock.greet.grants.join() === 'files:read' && lock.greet.sha256.length === 64 && lock.greet.url === repo, 'the lock: ' + JSON.stringify(lock))
  assert(fs.existsSync(path.join(WS, '..', 'data', 'plugins', 'greet', '1.0.0', 'web', 'index.js')), 'extracted')
  assert(!(await page.$('.rail-btn[data-id=greet]')), 'not used by the project: no tool')

  // Used by the project: listed in .ide/plugins.json with its repository; its tool comes.
  await page.check('[data-testid=plugin-greet]')
  await page.waitForTimeout(300)
  const choices = JSON.parse(fs.readFileSync(WS + '/demo/.ide/plugins.json', 'utf8'))
  assert(choices.external?.greet === repo, 'used by the project: ' + JSON.stringify(choices))
  await closeSettings()
  await page.waitForSelector('.rail-btn[data-id=greet]')

  // Its tool, in a sandboxed frame: an opaque origin, no network, no page; no core method
  // without its grant.
  await page.click('.rail-btn[data-id=greet]')
  const sandbox = await page.getAttribute('iframe.plugin-frame[data-view=greet]', 'sandbox')
  assert(sandbox === 'allow-scripts', 'sandboxed, no allow-same-origin: ' + sandbox)
  await frame(page, 'greet').locator('#checks').waitFor()
  let checks = JSON.parse(await frame(page, 'greet').locator('#checks').textContent())
  assert(checks.localStorage === 'blocked' && checks.indexedDB === 'blocked', 'no storage of the origin: ' + JSON.stringify(checks))
  assert(checks.fetch === 'blocked', 'no network (CSP): ' + JSON.stringify(checks))
  assert(checks.parent === 'blocked', 'no access to the page: ' + JSON.stringify(checks))
  assert(checks.rpc.startsWith('refused') && checks.rpc.includes('fs.list is not granted to Greet'), 'a core method without its grant: ' + checks.rpc)
  assert((await frame(page, 'greet').locator('#hello').textContent()) === 'Hello from the frame', 'its texts')

  // Its backend, confined: it answers its frame and emits to it; it reads the project
  // (files:read), not the pod's key nor the network; the core refuses it fs.list.
  const answer = async (button, want) => {
    await frame(page, 'greet').locator('#' + button).click()
    await frame(page, 'greet').locator('#answer:not(:empty)').waitFor()
    const got = await frame(page, 'greet').locator('#answer').textContent()
    await frame(page, 'greet').locator('#answer').evaluate((e) => (e.textContent = ''))
    return got
  }
  let got = await answer('backend')
  assert(got === 'Hello from the backend', 'the backend answers its frame: ' + got)
  await frame(page, 'greet').locator('#event:has-text("said by the backend")').waitFor()
  got = JSON.parse(await answer('probe'))
  assert(got.key === 'blocked' && got.net === 'blocked' && got.project === 'ok', 'the backend is confined: ' + JSON.stringify(got))
  got = await answer('core')
  assert(got.startsWith('refused') && got.includes('fs.list is not granted to Greet'), 'a core call of the backend without its grant: ' + got)
  const tool = await mcp('greet_hello', {})
  assert(!tool.result?.isError && tool.result?.content?.[0]?.text === 'Hello from the tool of the backend', 'its assistant tool through the MCP: ' + JSON.stringify(tool))

  // What the sandbox API does: a file opened, a tab view with its state (kept after a reload).
  await frame(page, 'greet').locator('#open').click()
  await page.waitForSelector('.tab:has-text("notes.txt")')
  await frame(page, 'greet').locator('#view').click()
  await frame(page, 'note').locator('#note:has-text("kept")').waitFor()
  await page.reload()
  await frame(page, 'note').locator('#note:has-text("kept")').waitFor()

  // The shortcuts of the IDE from the frame: Search Everywhere, then its command.
  await showGreet()
  await frame(page, 'greet').locator('#checks').click()
  await page.keyboard.press('Control+Shift+a')
  await page.waitForSelector('.pick.se')
  await page.keyboard.type('Say hello from Greet')
  await page.waitForSelector('.pick-item.selected:has-text("Say hello from Greet")')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.toast:has-text("Command of Greet")')

  // A new version asks the network and fs.list: the update asks only those.
  release('1.1.0', { ide: '^1.0', permissions: ['files:read', 'net', 'core:fs.list'] })
  await openSettings()
  await page.click('[data-testid=plugin-update-greet]')
  await page.waitForSelector('.plugin-grants')
  const asked = await page.textContent('.plugin-grants')
  assert(asked.includes('1.1.0') && asked.includes('Reach the network') && asked.includes('Call fs.list of the IDE') && !asked.includes('Read the files'), 'only the new permissions are asked: ' + asked)
  await page.check('[data-testid=plugin-grant-net]')
  await page.click('[data-testid=plugin-grants-accept]')
  await page.waitForFunction(() => document.querySelector('.plugin-row[data-plugin=greet]')?.textContent.includes('1.1.0'))
  lock = readLock()
  assert(lock.greet.version === '1.1.0' && lock.greet.grants.join() === 'files:read,net', 'updated in the lock, fs.list not granted: ' + JSON.stringify(lock.greet))
  assert(!fs.existsSync(path.join(WS, '..', 'data', 'plugins', 'greet', '1.0.0')), 'the old version removed')

  // Up to date now; a version for another IDE is refused with its reason.
  await page.click('[data-testid=plugin-update-greet]')
  await page.waitForSelector('.toast:has-text("Greet is up to date")')
  release('2.0.0', { ide: '^99' })
  await page.fill('[data-testid=plugin-install-url]', repo)
  await page.fill('[data-testid=plugin-install-constraint]', '^2')
  await page.click('[data-testid=plugin-install-find]')
  await page.waitForSelector('.toast-error:has-text("needs the IDE ^99")')

  // Removed: the row, the lock entry and the folder go; the project still names it.
  await page.click('[data-testid=plugin-remove-greet]')
  await page.waitForSelector('[data-testid=plugin-external-greet] >> text=Not installed on this pod.')
  assert(!readLock().greet && !fs.existsSync(path.join(WS, '..', 'data', 'plugins', 'greet')), 'removed from the pod')
  assert(!(await page.$('.rail-btn[data-id=greet]')), 'its tool gone')

  // Installed again from the row, fs.list granted: the frame gets its answer.
  await page.fill('[data-testid=plugin-install-constraint]', '')
  await page.click(row + ' button:has-text("Install")')
  await page.waitForSelector('.plugin-grants')
  for (const g of ['files:read', 'net', 'core:fs.list']) await page.check(`[data-testid="plugin-grant-${g}"]`)
  await page.click('[data-testid=plugin-grants-accept]')
  await page.waitForFunction(() => document.querySelector('.plugin-row[data-plugin=greet]')?.textContent.includes('core:fs.list'))
  await closeSettings()
  await page.reload()
  await showGreet()
  await frame(page, 'greet').locator('#checks').waitFor()
  checks = JSON.parse(await frame(page, 'greet').locator('#checks').textContent())
  assert(checks.rpc === 'answered', 'a core method with its grant: ' + checks.rpc)

  server.close()
  fs.rmSync(tmp, { recursive: true, force: true })
  // The fetch of the frame refused by its CSP is what was asked.
  errors.splice(0, errors.length, ...errors.filter((e) => !/Content Security Policy/.test(e)))
})
