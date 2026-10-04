// Tunnels of an SSH project (Docker tool, Tunnels tab) through the SSH server of the tests
// (sshtestd, built here): add, use, stop and start, saved in .ide/tunnels.json, reopened with
// the project, listed and closed from the home page.
const fs = require('fs')
const os = require('os')
const path = require('path')
const http = require('http')
const net = require('net')
const { execFileSync, spawn } = require('child_process')
const { run, assert, WS, OUT } = require('../common.cjs')

const pod = path.resolve(__dirname, '../../pod')
const goBin = ['go', path.join(os.homedir(), 'sdk/go/bin/go')].find((g) => {
  try {
    execFileSync(g, ['version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})
const bin = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'sshtestd-')), 'sshtestd')
execFileSync(goBin, ['build', '-o', bin, './internal/sshtest/sshtestd'], { cwd: pod })
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

// Shows the Tunnels tab of the Docker tool (the bottom zone may be closed after a navigation).
const showTunnels = async (page) => {
  await page.waitForSelector('.menubar', { timeout: 15000 })
  if (!(await page.isVisible('[data-testid=docker-tab-tunnels]'))) await page.click('.rail-right .rail-btn[title="Docker"]')
  await page.click('[data-testid=docker-tab-tunnels]')
  await page.waitForSelector('[data-testid=tunnel-form]')
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

  await showTunnels(page)
  await page.check('[data-testid=tunnel-lan]')
  assert((await page.textContent('[data-testid=tunnel-form]')).includes('without authentication'), 'local network scope warned')
  await page.uncheck('[data-testid=tunnel-lan]')
  await page.fill('[data-testid=tunnel-remote]', String(remote))
  await page.fill('[data-testid=tunnel-local]', String(local))
  await page.click('[data-testid=tunnel-form] button[type=submit]')
  await page.waitForSelector(`[data-testid=tunnel-${local}] .dk-dot.up`, { timeout: 10000 })
  assert((await get(local)) === 'hello from the ssh host', 'service of the SSH host reached through the tunnel')
  assert(fs.readFileSync(dir + '/.ide/tunnels.json', 'utf8').includes(`"remotePort": ${remote}`), 'tunnel saved in .ide/tunnels.json')
  await page.screenshot({ path: OUT + '/tunnels-tab.png' })

  await page.click(`[data-testid=tunnel-${local}] [data-action=stop]`)
  await page.waitForSelector(`[data-testid=tunnel-${local}] .dk-dot.down`)
  assert((await get(local)) === '', 'stopped tunnel closed')
  await page.click(`[data-testid=tunnel-${local}] [data-action=start]`)
  await page.waitForSelector(`[data-testid=tunnel-${local}] .dk-dot.up`)
  assert((await get(local)) === 'hello from the ssh host', 'tunnel started again')

  // Home page: the open tunnels of every project, closed one by one or all at once.
  await page.click('.menubar button[title="Projects"]')
  await page.waitForSelector(`[data-testid=home-tunnel-${local}]`)
  assert((await page.textContent(`[data-testid=home-tunnel-${local}]`)).includes('remote'), 'home page lists the open tunnel with its project')
  await page.screenshot({ path: OUT + '/tunnels-home.png' })
  await page.click(`[data-testid=home-tunnel-${local}] button`)
  await page.waitForSelector('[data-testid=home-tunnels]', { state: 'detached' })
  assert((await get(local)) === '', 'tunnel closed from the home page')

  // Still enabled: opening the project opens it again.
  await page.click('.project-open:has-text("remote")')
  await showTunnels(page)
  await page.waitForSelector(`[data-testid=tunnel-${local}] .dk-dot.up`, { timeout: 10000 })
  assert((await get(local)) === 'hello from the ssh host', 'enabled tunnel reopened with the project')
  await page.click('.menubar button[title="Projects"]')
  await page.click('[data-testid=tunnels-close-all]')
  await page.waitForSelector('[data-testid=home-tunnels]', { state: 'detached' })
  assert((await get(local)) === '', 'close all')

  // Deleted from the tab.
  await page.click('.project-open:has-text("remote")')
  await showTunnels(page)
  await page.waitForSelector(`[data-testid=tunnel-${local}]`, { timeout: 15000 })
  await page.click(`[data-testid=tunnel-${local}] [data-action=delete]`)
  await page.waitForSelector(`[data-testid=tunnel-${local}]`, { state: 'detached' })
  assert(!fs.readFileSync(dir + '/.ide/tunnels.json', 'utf8').includes(String(remote)), 'tunnel deleted')
  service.close()
  sshd.kill()
})
