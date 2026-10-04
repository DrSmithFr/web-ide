// Docker tool against the local Docker: Compose stack of the project (start, stop, details,
// statistics, logs with filter, shell, tasks in the Console, down with volumes) and the Host tab.
// Needs docker with Compose and the postgres:17-alpine image (no download).
const fs = require('fs')
const { execSync } = require('child_process')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

try {
  execSync('docker compose version && docker image inspect postgres:17-alpine', { stdio: 'ignore' })
} catch {
  console.log('  skip docker: needs docker, Compose and the postgres:17-alpine image')
  process.exit(0)
}

const dir = WS + '/demo'
const name = 'webide-e2e-' + process.pid
const compose = (cmd) => execSync(`docker compose ${cmd}`, { cwd: dir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
fs.writeFileSync(
  dir + '/compose.yaml',
  `name: ${name}
services:
  app:
    image: postgres:17-alpine
    entrypoint: ["sh", "-c", "i=0; while true; do printf '\\\\033[32mtick %d\\\\033[0m\\\\n' $$i; echo warn$$i >&2; i=$$((i+1)); sleep 0.5; done"]
    ports: ["127.0.0.1::80"]
    volumes: ["data:/data", "./:/src:ro"]
  db:
    image: postgres:17-alpine
    entrypoint: ["sleep", "infinity"]
  extra:
    image: postgres:17-alpine
    entrypoint: ["sleep", "infinity"]
    profiles: ["debug"]
volumes:
  data:
`,
)
const cleanup = () => {
  try {
    compose('--profile debug down -v -t 0')
  } catch {
    /* already down */
  }
}
process.on('exit', cleanup)

const row = (svc) => `[data-testid=docker-service-${svc}]`
const dot = (page, svc) => page.$eval(`${row(svc)} .dk-dot`, (e) => e.className.replace('dk-dot', '').trim()).catch(() => 'none')
const waitDot = (page, svc, want, timeout = 30000) =>
  page.waitForFunction(([sel, want]) => document.querySelector(sel)?.className.includes(want), [`${row(svc)} .dk-dot`, want], { timeout }).then(() => true, () => false)
const menu = async (page, sel, label) => {
  await page.click(sel, { button: 'right' })
  await page.click(`.ctx-menu .ctx-item:has-text("${label}")`)
}

run(async ({ page }) => {
  page.on('dialog', (d) => d.accept())
  await openProject(page)
  await page.click('.rail-right .rail-btn[title="Docker"]')
  await page.waitForSelector('[data-testid=docker-stack]', { timeout: 15000 })
  assert((await page.textContent('[data-testid=docker-stack]')).includes(name), 'stack row named after the Compose project')
  assert((await page.isVisible(row('app'))) && (await page.isVisible(row('db'))), 'declared services listed')
  assert(!(await page.$(row('extra'))), 'service of an inactive profile hidden')
  assert((await page.textContent(row('db'))).includes('not created'), 'service without container: not created')

  // Profiles chosen in a menu.
  await page.click('[data-testid=docker-profiles]')
  await page.click('.ctx-menu .ctx-item:has-text("debug")')
  await page.waitForSelector(row('extra'))
  assert(true, 'profile debug shows its service')
  await page.click('[data-testid=docker-profiles]')
  await page.click('.ctx-menu .ctx-item:has-text("debug")')
  await page.waitForSelector(row('extra'), { state: 'detached' })

  // Start the stack from its row.
  await page.click('[data-testid=docker-stack] [data-action=start]')
  assert((await waitDot(page, 'app', 'up')) && (await waitDot(page, 'db', 'up')), 'stack started: services running')
  assert(/127\.0\.0\.1:\d+→80/.test(await page.textContent(`${row('app')} .dk-ports`)), 'published port in the row')

  // Details: inspect and live statistics.
  await page.click(row('app'))
  await page.waitForSelector('[data-testid=docker-detail] .dk-kv')
  await page.waitForFunction(() => document.querySelector('[data-testid=docker-mounts]')?.textContent.includes('/src'))
  const mounts = await page.textContent('[data-testid=docker-mounts]')
  assert(mounts.includes(`${name}_data`) && mounts.includes('→ /data') && mounts.includes('ro'), 'mounts: named volume and read-only bind: ' + mounts)
  assert((await page.textContent('[data-testid=docker-networks]')).includes(`${name}_default`), 'network listed')
  assert(/→80/.test(await page.textContent('[data-testid=docker-ports]')), 'ports listed')
  await page.waitForSelector('[data-testid=docker-cpu]', { timeout: 15000 })
  assert(/%/.test(await page.textContent('[data-testid=docker-cpu]')) && /B/.test(await page.textContent('[data-testid=docker-mem]')), 'CPU and memory shown')
  await page.screenshot({ path: OUT + '/docker-infos.png' })

  // Logs: followed live, stderr included, ANSI colors kept, filter.
  await page.click('[data-testid=docker-detail-logs]')
  await page.waitForFunction(() => (document.querySelector('[data-testid=docker-logs]')?.textContent.match(/tick \d+/g) ?? []).length >= 3, null, { timeout: 15000 })
  const before = await page.$$eval('.dk-log-line', (l) => l.length)
  await page.waitForTimeout(1500)
  assert((await page.$$eval('.dk-log-line', (l) => l.length)) > before, 'logs followed live')
  assert((await page.textContent('[data-testid=docker-logs]')).includes('warn'), 'stderr in the logs')
  assert(await page.$('.dk-log-line span[style*="color"]'), 'ANSI color rendered')
  assert(!(await page.textContent('[data-testid=docker-logs]')).includes('[32m'), 'escape sequences not shown')
  await page.fill('[data-testid=docker-log-filter]', 'warn')
  await page.waitForTimeout(200)
  const filtered = await page.$$eval('.dk-log-line', (l) => l.map((x) => x.textContent))
  assert(filtered.length > 0 && filtered.every((l) => l.includes('warn')), 'filter keeps the matching lines')
  await page.fill('[data-testid=docker-log-filter]', '')
  await page.click('[data-testid=docker-log-times]')
  assert(await page.isVisible('.dk-log-time'), 'timestamps shown on demand')
  await page.screenshot({ path: OUT + '/docker-logs.png' })

  // Logs of the stack: every service, with a service filter.
  await page.click('[data-testid=docker-stack]')
  await page.waitForFunction(() => document.querySelector('.dk-log-svc')?.textContent.startsWith('app'), null, { timeout: 15000 })
  assert(await page.isVisible('[data-testid=docker-log-service]'), 'stack logs: service filter')

  // Stop one service from its row.
  await page.click(`${row('db')} [data-action=stop]`)
  assert(await waitDot(page, 'db', 'down'), 'service stopped')

  // Shell in a container: a Console terminal.
  await page.click(row('app'))
  await page.click('[data-testid=docker-shell]')
  await page.waitForSelector('.console-tabs .tab.active:has-text("app")', { timeout: 10000 })
  await page.waitForSelector('.xterm')
  await page.waitForTimeout(1500)
  await page.keyboard.type('echo in-$((40+2)) && cat /etc/os-release | head -1\n')
  await page.waitForFunction(() => /in-42[\s\S]*Alpine/.test(document.querySelector('.term-slot:not([hidden]) .xterm-rows, .xterm-rows')?.textContent ?? ''), null, { timeout: 10000 }).then(
    () => assert(true, 'shell runs in the container'),
    () => assert(false, 'shell runs in the container'),
  )

  // Longer actions run as Console tasks.
  await menu(page, row('app'), 'Recreate')
  await page.waitForSelector('.console-tabs .tab.active:has-text("--force-recreate app")', { timeout: 10000 })
  assert(true, 'recreate runs in a Console task')

  // Host tab: every container, grouped by Compose project.
  await page.click('[data-testid=docker-tab-host]')
  await page.waitForSelector(`.dk-group:has-text("${name}")`, { timeout: 15000 })
  assert(await page.isVisible(`[data-testid=docker-container-${name}-app-1]`), 'host tab lists the containers of the project')
  await page.click('[data-testid=docker-tab-project]')

  // Down with volumes: the project name typed to confirm.
  await page.waitForSelector('[data-testid=docker-stack]')
  await menu(page, '[data-testid=docker-stack]', 'Down with volumes')
  await page.waitForSelector('.modal input')
  await page.fill('.modal input', name)
  await page.keyboard.press('Enter')
  await page.waitForSelector('.console-tabs .tab.active:has-text("down -v")', { timeout: 10000 })
  assert(await waitDot(page, 'app', 'none', 40000), 'down -v: containers removed')
  const volume = () => execSync(`docker volume ls -q --filter name=${name}_data`, { encoding: 'utf8' }).trim()
  for (let i = 0; i < 50 && volume(); i++) await page.waitForTimeout(200)
  assert(volume() === '', 'down -v: volume removed')
  await page.screenshot({ path: OUT + '/docker-down.png' })
})
