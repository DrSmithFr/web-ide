// Port forwards of an SSH project (Docker tool, Port forwards tab) through the SSH server of the tests
// (sshtestd, built here): add, use, stop and start, saved in .ide/forwards.json, reopened with
// the project, listed and closed from the home page.
const fs = require('fs')
const os = require('os')
const path = require('path')
const http = require('http')
const net = require('net')
const { execFileSync, spawn } = require('child_process')
const { run, assert, WS, OUT } = require('../../../e2e/common.cjs')

const pod = path.resolve(__dirname, '../../../pod')
const goBin = ['go', path.join(os.homedir(), 'sdk/go/bin/go')].find((g) => {
  try {
    execFileSync(g, ['version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})
const bin = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'sshtestd-')), 'sshtestd')
execFileSync(goBin, ['build', '-o', bin, './sshtest/sshtestd'], { cwd: pod })
const sshd = spawn(bin, [], { stdio: ['ignore', 'pipe', 'inherit'] })
process.on('exit', () => sshd.kill())
const sshPort = new Promise((resolve) => sshd.stdout.once('data', (d) => resolve(parseInt(String(d), 10))))

// The service of the "SSH host" (the same machine here).
const service = http.createServer((req, res) => res.end('hello from the ssh host'))
const listen = (srv) => new Promise((resolve) => srv.listen(0, '127.0.0.1', () => resolve(srv.address().port)))
const freePort = async () => {
  const s = net.createServer()
  const p = await listen(s)
  await new Promise((r) => s.close(r))
  return p
}
const get = (port) =>
  new Promise((resolve) => {
    http
      .get({ host: '127.0.0.1', port, timeout: 2000 }, (res) => {
        let body = ''
        res.on('data', (d) => (body += d))
        res.on('end', () => resolve(body))
      })
      .on('error', () => resolve(''))
      .on('timeout', function () {
        this.destroy()
        resolve('')
      })
  })

// Shows the Port forwards tab of its tool (the bottom zone may be closed after a navigation).
const showForwards = async (page) => {
  await page.waitForSelector('.menubar', { timeout: 15000 })
  if (!(await page.isVisible('[data-testid=forwards-tab-forwards]'))) await page.click('.rail-btn[data-id=forwards]')
  await page.click('[data-testid=forwards-tab-forwards]')
  await page.waitForSelector('[data-testid=forward-form]')
}

run(async ({ page }) => {
  page.on('dialog', (d) => d.accept())
  const remote = await listen(service)
  const local = await freePort()
  const dir = WS + '/other'

  // An SSH project on the test server, password asked when opening.
  await page.waitForSelector('.home .home-bar')
  await page.click('.home-bar button:has-text("New project")')
  await page.click('.modal [role=radio]:has-text("SSH")')
  await page.fill('.modal .field:has-text("Host") input', '127.0.0.1')
  await page.fill('.modal .field:has-text("Port") input', String(await sshPort))
  await page.selectOption('.modal .field:has-text("Authentication") select', 'password')
  await page.fill('.modal .field:has-text("Remote folder") input', dir)
  await page.fill('.modal .field:has-text("Title") input', 'remote')
  await page.click('.modal button:has-text("Create and open")')
  await page.waitForSelector('.modal input[type=password]', { timeout: 15000 })
  await page.fill('.modal input[type=password]', 'pw')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.menubar', { timeout: 15000 })
  assert(true, 'SSH project opened')

  await showForwards(page)
  await page.check('[data-testid=forward-lan]')
  assert((await page.textContent('[data-testid=forward-form]')).includes('without authentication'), 'local network scope warned')
  await page.uncheck('[data-testid=forward-lan]')
  await page.fill('[data-testid=forward-remote]', String(remote))
  await page.fill('[data-testid=forward-local]', String(local))
  await page.click('[data-testid=forward-form] button[type=submit]')
  await page.waitForSelector(`[data-testid=forward-${local}] .dk-dot.up`, { timeout: 10000 })
  assert((await get(local)) === 'hello from the ssh host', 'service of the SSH host reached through the port forward')
  assert(fs.readFileSync(dir + '/.ide/forwards.json', 'utf8').includes(`"remotePort": ${remote}`), 'port forward saved in .ide/forwards.json')
  await page.screenshot({ path: OUT + '/forwards-tab.png' })

  await page.click(`[data-testid=forward-${local}] [data-action=stop]`)
  await page.waitForSelector(`[data-testid=forward-${local}] .dk-dot.down`)
  assert((await get(local)) === '', 'stopped port forward closed')
  await page.click(`[data-testid=forward-${local}] [data-action=start]`)
  await page.waitForSelector(`[data-testid=forward-${local}] .dk-dot.up`)
  assert((await get(local)) === 'hello from the ssh host', 'port forward started again')

  // Home page: the open port forwards of every project, closed one by one or all at once.
  await page.click('.menubar button[title="Projects"]')
  await page.waitForSelector(`[data-testid=home-forward-${local}]`)
  assert((await page.textContent(`[data-testid=home-forward-${local}]`)).includes('remote'), 'home page lists the open port forward with its project')
  await page.screenshot({ path: OUT + '/forwards-home.png' })
  await page.click(`[data-testid=home-forward-${local}] button`)
  await page.waitForSelector('[data-testid=home-forwards]', { state: 'detached' })
  assert((await get(local)) === '', 'port forward closed from the home page')

  // Still enabled: opening the project opens it again.
  await page.click('.project-open:has-text("remote")')
  await showForwards(page)
  await page.waitForSelector(`[data-testid=forward-${local}] .dk-dot.up`, { timeout: 10000 })
  assert((await get(local)) === 'hello from the ssh host', 'enabled port forward reopened with the project')
  await page.click('.menubar button[title="Projects"]')
  await page.click('[data-testid=forwards-close-all]')
  await page.waitForSelector('[data-testid=home-forwards]', { state: 'detached' })
  assert((await get(local)) === '', 'close all')

  // Deleted from the tab.
  await page.click('.project-open:has-text("remote")')
  await showForwards(page)
  await page.waitForSelector(`[data-testid=forward-${local}]`, { timeout: 15000 })
  await page.click(`[data-testid=forward-${local}] [data-action=delete]`)
  await page.waitForSelector(`[data-testid=forward-${local}]`, { state: 'detached' })
  assert(!fs.readFileSync(dir + '/.ide/forwards.json', 'utf8').includes(String(remote)), 'port forward deleted')
  service.close()
  sshd.kill()
})
