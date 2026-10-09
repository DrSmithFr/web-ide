// Keeper: a terminal survives a restart of the pod (run.sh starts a keeper for this suite and
// gives the command restarting the pod). The page attaches again from its offset: the output
// goes on without gap or duplicate, input works, closing the terminal ends its process.
const fs = require('fs')
const net = require('net')
const { execFileSync } = require('child_process')
const { run, openProject, assert } = require('../common.cjs')

/** Asks the keeper of the test its processes (frames: uint32 length, JSON header, newline). */
function keeperList() {
  return new Promise((resolve, reject) => {
    const c = net.connect(process.env.E2E_DATA + '/keeper.sock')
    let buf = Buffer.alloc(0)
    c.on('connect', () => {
      const h = Buffer.from(JSON.stringify({ id: 1, op: 'list', args: {} }) + '\n')
      const len = Buffer.alloc(4)
      len.writeUInt32BE(h.length)
      c.write(Buffer.concat([len, h]))
    })
    c.on('data', (d) => {
      buf = Buffer.concat([buf, d])
      if (buf.length < 4 || buf.length < 4 + buf.readUInt32BE(0)) return
      const frame = buf.subarray(4, 4 + buf.readUInt32BE(0)).toString()
      c.end()
      resolve(JSON.parse(frame.slice(0, frame.indexOf('\n'))).result)
    })
    c.on('error', reject)
  })
}

const ticks = (text) => [...text.matchAll(/tick (\d+)/g)].map((m) => Number(m[1]))
const consecutive = (n) => n.every((v, i) => i === 0 || v === n[i - 1] + 1)

run(async ({ page }) => {
  assert(/keeper: .*keeper\.sock/.test(fs.readFileSync(process.env.E2E_DATA + '/../pod.log', 'utf8')), 'the pod uses the keeper of the test')
  await openProject(page)
  await page.keyboard.press('Control+Shift+Backquote')
  await page.waitForSelector('.xterm', { timeout: 5000 })
  await page.waitForTimeout(800)
  await page.keyboard.type('i=0; while true; do echo tick $i; i=$((i+1)); sleep 0.2; done\n')
  await page.waitForFunction(() => /tick 5\b/.test(document.querySelector('.xterm-rows')?.textContent ?? ''), null, { timeout: 5000 })
  const listed = await keeperList()
  assert(listed.length === 1 && listed[0].pty && !listed[0].exited, 'the terminal runs in the keeper: ' + JSON.stringify(listed))

  // The pod restarts (kill -TERM, the keeper stays); the page reconnects by itself.
  const before = Math.max(...ticks(await page.textContent('.xterm-rows')))
  execFileSync('bash', [process.env.E2E_RESTART_POD])
  const target = before + 12
  const resumed = await page
    .waitForFunction((n) => new RegExp(`tick ${n}\\b`).test(document.querySelector('.xterm-rows')?.textContent ?? ''), target, { timeout: 15000 })
    .then(() => true, () => false)
  const seen = ticks(await page.textContent('.xterm-rows'))
  assert(resumed && consecutive(seen) && seen[0] <= before + 1, `the counter goes on across the restart, no gap or duplicate (before ${before}): ${seen.join(' ')}`)
  assert((await keeperList())[0].id === listed[0].id, 'the same process, adopted by the new pod')

  // Input works again.
  await page.click('.xterm')
  await page.keyboard.press('Control+c')
  await page.keyboard.type('echo back$((1+1))\n')
  const back = await page.waitForFunction(() => /back2/.test(document.querySelector('.xterm-rows')?.textContent ?? ''), null, { timeout: 5000 }).then(() => true, () => false)
  assert(back, 'input reaches the adopted terminal')

  // Closing the terminal ends its process in the keeper.
  await page.click('.console-tabs .tab.active .tab-close')
  let gone = false
  for (let i = 0; i < 30 && !gone; i++) {
    gone = (await keeperList()).length === 0
    if (!gone) await page.waitForTimeout(100)
  }
  assert(gone, 'a closed terminal leaves the keeper')
})
