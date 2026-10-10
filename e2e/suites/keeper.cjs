// Keeper: a terminal survives a restart of the pod (run.sh starts a keeper for this suite and
// gives the command restarting the pod). The page attaches again from its offset: the output
// goes on without gap or duplicate, input works, closing the terminal ends its process. A
// conversation of the assistant survives too: restarted while the answer is written, then while
// its command runs, it ends whole, the model asked once per step.
const fs = require('fs')
const net = require('net')
const { execFileSync, execFile } = require('child_process')
const path = require('path')

const POD = path.join(__dirname, '../../bin/web-ide-pod')
/** Updates the keeper of the test in place (web-ide-pod keeper -upgrade): its output. */
const upgrade = () =>
  new Promise((resolve) => execFile(POD, ['keeper', '-upgrade', '-data', process.env.E2E_DATA], (err, stdout, stderr) => resolve({ err, out: stdout + stderr })))
const http = require('http')
const { run, openProject, assert } = require('../common.cjs')

// A model server that writes slowly: 40 words in 4 s, then runs a command, then ends.
const requests = []
const fake = http.createServer(async (req, res) => {
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'slow-model' }] }))
  if (req.url !== '/v1/chat/completions') return res.writeHead(404).end()
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  requests.push(r)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const send = (delta, finish = null) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`)
  const results = r.messages.filter((m) => m.role === 'tool').length
  if (results === 0) {
    for (let i = 0; i < 40 && !res.destroyed; i++) {
      send({ content: `word${i} ` })
      await new Promise((ok) => setTimeout(ok, 100))
    }
    send({ tool_calls: [{ index: 0, id: 'k1', type: 'function', function: { name: 'run_command', arguments: JSON.stringify({ command: 'sleep 3; echo done-cmd', timeout: 30 }) } }] })
    send({}, 'tool_calls')
  } else {
    send({ content: 'finished' })
    send({}, 'stop')
  }
  res.end('data: [DONE]\n\n')
})

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

  // The keeper updates itself in place (re-exec): same pid, the counter goes on.
  const atUpgrade = Math.max(...ticks(await page.textContent('.xterm-rows')))
  const up = await upgrade()
  assert(!up.err && /updated in place \(pid \d+\)/.test(up.out), 'the keeper re-executes itself, same pid: ' + up.out.trim())
  const goesOn = await page
    .waitForFunction((n) => new RegExp(`tick ${n}\\b`).test(document.querySelector('.xterm-rows')?.textContent ?? ''), atUpgrade + 10, { timeout: 15000 })
    .then(() => true, () => false)
  const after = ticks(await page.textContent('.xterm-rows'))
  assert(goesOn && consecutive(after) && (await keeperList())[0].id === listed[0].id, 'the counter goes on across the update of the keeper: ' + after.join(' '))

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

  // The assistant: the pod restarts while the answer is written, then while its command runs.
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  try {
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.waitForSelector('.ai-panel .ai-empty')
    await page.click('.ai-empty button:has-text("Add a model server")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Add")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("slow-model")', { timeout: 5000 })
    await page.fill('.ai-composer .ed-content', 'Write slowly then run the command')
    await page.keyboard.press('Control+Enter')
    await page.waitForFunction(() => /word5 /.test(document.querySelector('.ai-msg.live')?.textContent ?? ''), null, { timeout: 10000 })
    execFileSync('bash', [process.env.E2E_RESTART_POD])
    const whole = await page
      .waitForFunction(() => [...document.querySelectorAll('.ai-msg.assistant')].some((m) => /word0 [\s\S]*word39/.test(m.textContent)), null, { timeout: 20000 })
      .then(() => true, () => false)
    const text = await page.$$eval('.ai-msg.assistant', (e) => e.map((m) => m.textContent).join(' | '))
    const words = [...text.matchAll(/word(\d+)/g)].map((m) => Number(m[1]))
    assert(whole && consecutive(words) && words.length === 40, 'the answer written across the restart is whole, once: ' + words.join(' '))
    assert(!/Interrupted/.test(await page.textContent('.ai-panel')), 'the conversation is not interrupted')

    // The command runs (sleep 3): the pod restarts again.
    await page.waitForSelector('.ai-tool.running', { timeout: 10000 })
    await page.waitForTimeout(800)
    execFileSync('bash', [process.env.E2E_RESTART_POD])
    const finished = await page.waitForFunction(() => /finished/.test(document.querySelector('.ai-panel .ai-messages')?.textContent ?? ''), null, { timeout: 30000 }).then(() => true, () => false)
    const toolResult = requests[1]?.messages.find((m) => m.role === 'tool')?.content ?? ''
    assert(finished && /done-cmd/.test(toolResult) && /Exit code: 0/.test(toolResult), 'the command ends across the restart and its result reaches the model: ' + toolResult.slice(0, 120))
    assert(requests.length === 2, 'the model is asked once per step: ' + requests.length + ' requests')

    // An update of the keeper while an answer is written waits for its end.
    await page.click('.ai-panel button[title="New conversation"]')
    await page.fill('.ai-composer .ed-content', 'Write slowly again')
    await page.keyboard.press('Control+Enter')
    await page.waitForFunction(() => /word3 /.test(document.querySelector('.ai-msg.live')?.textContent ?? ''), null, { timeout: 10000 })
    const waited = await upgrade()
    assert(!waited.err && /waiting for HTTP request/.test(waited.out) && /updated in place/.test(waited.out), 'the update waits for the answer being written: ' + waited.out.trim().split('\n').join(' / '))
    const whole2 = await page
      .waitForFunction(() => [...document.querySelectorAll('.ai-msg.assistant')].some((m) => /word0 [\s\S]*word39/.test(m.textContent)), null, { timeout: 20000 })
      .then(() => true, () => false)
    assert(whole2 && !/Interrupted/.test(await page.textContent('.ai-panel')), 'the answer is whole after the update of the keeper')
  } finally {
    fake.close()
  }
})
