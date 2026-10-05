// Screenshots and GIFs of the documentation (docs/images), made on a clean copy of this
// repository: the development cycle of a ticket, from the briefing to the merge.
//
//   node e2e/shots/shots.cjs           replay the recorded conversations (no model needed)
//   node e2e/shots/shots.cjs record    record them with a real model (LLM_URL, LLM_MODEL)
//
// The copy is taken at the commit of the recording, so the replayed tool calls apply to the
// same files. Recording again: delete recording.json.gz first.
const fs = require('fs')
const os = require('os')
const path = require('path')
const { spawn, execFileSync } = require('child_process')
const { chromium } = require('playwright-core')
const proxy = require('./llm-proxy.cjs')
const screencast = require('./screencast.cjs')

const ROOT = path.resolve(__dirname, '../..')
const IMAGES = path.join(ROOT, 'docs/images')
const REC = path.join(__dirname, 'recording.json.gz')
const MODE = process.argv[2] === 'record' ? 'record' : 'replay'
const PORT = 4529
const VIEW = { width: 1440, height: 900 }
const BRIEF =
  'When the pod runs with -allow-remote, it only prints http://0.0.0.0:4433/?token=… at startup. ' +
  'I would like it to print the addresses that other machines can actually open (LAN, Tailscale…).'

function chrome() {
  if (process.env.CHROME) return process.env.CHROME
  const cache = path.join(os.homedir(), '.cache/ms-playwright')
  const dirs = fs.existsSync(cache) ? fs.readdirSync(cache).filter((d) => /^chromium-\d+$/.test(d)).sort().reverse() : []
  for (const d of dirs) if (fs.existsSync(path.join(cache, d, 'chrome-linux64/chrome'))) return path.join(cache, d, 'chrome-linux64/chrome')
  throw new Error('Chromium not found: set CHROME=/path/to/chrome')
}

const git = (cwd, ...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim()

/** Workspace with a copy of the repository at `commit`, without remote (the base is main). */
function workspace(tmp, commit) {
  const ws = path.join(tmp, 'ws')
  const repo = path.join(ws, 'web-ide')
  fs.mkdirSync(ws, { recursive: true })
  git(ws, 'clone', '-q', '--no-local', ROOT, repo)
  git(repo, 'checkout', '-q', '-B', 'main', commit)
  git(repo, 'remote', 'remove', 'origin')
  // The icon of the project, and what the build needs before the first front end build.
  fs.mkdirSync(path.join(repo, '.ide'), { recursive: true })
  for (const f of ['icon.svg', 'icon.json']) if (fs.existsSync(path.join(ROOT, '.ide', f))) fs.copyFileSync(path.join(ROOT, '.ide', f), path.join(repo, '.ide', f))
  fs.mkdirSync(path.join(repo, 'pod/webdist/dist'), { recursive: true })
  fs.writeFileSync(path.join(repo, 'pod/webdist/dist/index.html'), '')
  for (const d of ['web', 'e2e']) fs.symlinkSync(path.join(ROOT, d, 'node_modules'), path.join(repo, d, 'node_modules'))
  // Other projects of the home page.
  for (const [name, file] of [['shop-api', 'main.go'], ['blog', 'index.md'], ['infra', 'compose.yaml']]) {
    fs.mkdirSync(path.join(ws, name))
    fs.writeFileSync(path.join(ws, name, file), '\n')
    git(path.join(ws, name), 'init', '-q')
  }
  return { ws, repo }
}

/** Data folder: high contrast theme, the model server of the proxy. */
function data(tmp, llmPort, model) {
  const dir = path.join(tmp, 'data')
  fs.mkdirSync(dir, { recursive: true })
  const settings = { theme: 'contraste', language: 'en' }
  fs.writeFileSync(path.join(dir, 'settings.json'), JSON.stringify({ current: 1, nextId: 2, entries: [{ id: 1, ts: new Date().toISOString(), label: 'Initial settings', settings }] }))
  const server = { id: 's1', name: 'llama.cpp', kind: 'auto', url: `http://127.0.0.1:${llmPort}`, apiKey: '' }
  fs.writeFileSync(path.join(dir, 'llm.json'), JSON.stringify({ servers: [server], server: 's1', model }))
  const day = (n) => new Date(Date.now() - n * 86400_000).toISOString()
  const projects = [
    ['shop-api', 'Shop API', 'Orders and payments service (Go, PostgreSQL)', 1],
    ['blog', 'Blog', 'Static site and its drafts', 3],
    ['infra', 'Infra', 'Compose stacks of the home server', 6],
  ].map(([id, title, description, n]) => ({ id, title, description, type: 'local', path: path.join(tmp, 'ws', id), createdAt: day(30), openedAt: day(n) }))
  fs.writeFileSync(path.join(dir, 'projects.json'), JSON.stringify(projects))
  return dir
}

async function startPod(tmp, ws, dataDir) {
  const home = path.join(tmp, 'home')
  fs.mkdirSync(home, { recursive: true })
  // git and the other tools speak English, so the model does too.
  const env = { ...process.env, LANG: 'C.UTF-8', LC_ALL: 'C.UTF-8', LANGUAGE: 'en', WEBIDE_INSTRUCTIONS_HOME: home, PATH: `${os.homedir()}/go/bin:${os.homedir()}/sdk/go/bin:${process.env.PATH}` }
  const args = ['-addr', `127.0.0.1:${PORT}`, '-data', dataDir, '-workspace', ws, '-static', path.join(ROOT, 'pod/webdist/dist')]
  const pod = spawn(path.join(ROOT, 'bin/web-ide-pod'), args, { env, stdio: ['ignore', fs.openSync(path.join(tmp, 'pod.log'), 'w'), 'inherit'] })
  for (let i = 0; i < 100 && !fs.existsSync(path.join(dataDir, 'token')); i++) await new Promise((ok) => setTimeout(ok, 100))
  await new Promise((ok) => setTimeout(ok, 300))
  return { pod, token: fs.readFileSync(path.join(dataDir, 'token'), 'utf8').trim() }
}

const shot = (page, name, clip) => page.screenshot({ path: path.join(IMAGES, name + '.png'), clip })
const LONG = MODE === 'record' ? 45 * 60_000 : 3 * 60_000

/** Waits for the end of the turn of the assistant. */
const idle = (page) =>
  page.waitForFunction(() => document.querySelector('[data-testid=send]') && !document.querySelector('[data-testid=stop]'), null, { timeout: LONG, polling: 250 })

/** Answers the questions of ask_user with their first (recommended) option. */
async function answer(page) {
  const done = new Set()
  for (let i = 0; i < 40; i++) {
    if (await page.isVisible('[data-testid=ai-ask-send]:not([disabled])')) {
      await page.waitForTimeout(800)
      await page.click('[data-testid=ai-ask-send]')
      return true
    }
    const q = page.locator('[data-testid=ai-ask-question]')
    if (!(await q.isVisible())) return false
    const text = await q.textContent()
    if (!done.has(text)) {
      done.add(text)
      await page.waitForTimeout(700)
      await page.locator('.ai-ask-option').first().click()
    } else if (await page.isVisible('[data-testid=ai-ask-next]:not([disabled])')) await page.click('[data-testid=ai-ask-next]')
    await page.waitForTimeout(400)
  }
  return false
}

/** Widens the right panel (the assistant) to `width` pixels with its resizer. */
async function widenRight(page, width = 560) {
  const handles = await page.$$('.resizer-x')
  const boxes = await Promise.all(handles.map((h) => h.boundingBox()))
  const box = boxes.filter(Boolean).sort((a, b) => b.x - a.x)[0]
  const x = box.x + box.width / 2
  const y = box.y + box.height / 2
  const panel = VIEW.width - x - 40
  await page.mouse.move(x, y)
  await page.mouse.down()
  await page.mouse.move(x - (width - panel), y, { steps: 4 })
  await page.mouse.up()
}

/** Opens a file with "go to file" and puts the caret after the first match of `at`. */
async function openAt(page, file, at) {
  await page.keyboard.press('Control+Shift+n')
  await page.waitForSelector('.pick-input')
  await page.keyboard.type(file)
  await page.waitForFunction((f) => document.querySelector('.pick-item.selected')?.textContent.includes(f.split('/').pop()), file)
  await page.keyboard.press('Enter')
  await page.waitForSelector('.pane.active .ed-content')
  await page.waitForTimeout(500)
  await page.keyboard.press('Control+f')
  await page.waitForSelector('.findbar')
  await page.fill('.find-input', at)
  await page.waitForTimeout(300)
  await page.keyboard.press('Escape')
  await page.keyboard.press('ArrowRight')
}

async function say(page, text) {
  await page.click('.ai-composer textarea')
  await page.keyboard.type(text, { delay: 12 })
  await page.waitForTimeout(300)
  await page.keyboard.press('Enter')
}

;(async () => {
  // A fixed folder: the recorded commands of the model contain its paths.
  const tmp = path.join(os.tmpdir(), 'web-ide-shots')
  fs.rmSync(tmp, { recursive: true, force: true })
  fs.mkdirSync(tmp)
  const upstream = process.env.LLM_URL ?? 'http://127.0.0.1:9931'
  const llm = await proxy.start({ mode: MODE, file: REC, upstream })
  if (!llm.rec.commit) {
    if (MODE === 'replay') throw new Error('no recording: run with "record" first')
    llm.rec.commit = git(ROOT, 'rev-parse', 'HEAD')
    llm.rec.model = process.env.LLM_MODEL ?? (await (await fetch(upstream + '/v1/models')).json()).data[0].id
    llm.save()
  }
  const { ws, repo } = workspace(tmp, llm.rec.commit)
  const dataDir = data(tmp, llm.port, llm.rec.model)
  const { pod, token } = await startPod(tmp, ws, dataDir)
  const browser = await chromium.launch({ executablePath: chrome(), headless: true })
  const ctx = await browser.newContext({ viewport: VIEW, locale: 'en-US' })
  // The model applies its file changes without a confirmation for each one.
  await ctx.addInitScript(() => localStorage.getItem('webide.llm.prefs') || localStorage.setItem('webide.llm.prefs', '{"autoApply":true}'))
  const page = await ctx.newPage()
  page.on('dialog', (d) => d.accept())
  page.on('pageerror', (e) => console.error('pageerror: ' + e.message))
  try {
    await page.goto(`http://127.0.0.1:${PORT}/?token=${token}`)
    for (const id of ['shop-api', 'blog', 'infra']) {
      await page.goto(`http://127.0.0.1:${PORT}/project/${id}`)
      await page.waitForSelector('.menubar')
      await page.waitForTimeout(1500)
    }
    await page.goto(`http://127.0.0.1:${PORT}/`)
    await page.click('.ws-dir:has-text("web-ide")')
    await page.waitForSelector('.menubar')
    await page.waitForTimeout(1500)

    // Board: worktree setup, a few tickets around the one of the scenario.
    await page.click('.rail-left .rail-btn[title="Kanban"]')
    await page.click('[data-testid=kanban-open-board]')
    await page.click('[data-testid=kanban-settings]')
    await page.fill('[data-testid=kanban-setup]', `mkdir -p pod/webdist/dist && touch pod/webdist/dist/index.html && ln -s ${ROOT}/web/node_modules web/ && ln -s ${ROOT}/e2e/node_modules e2e/`)
    await page.click('[data-testid=kanban-settings-save]')
    for (const [title, plan, goals] of [
      ['Terminal: copy on select'],
      ['Remember the width of the side panels'],
      ['Search Everywhere: find settings', '1. Index the setting labels\n2. Open the settings modal on the entry', ['Settings are listed', 'Enter opens the setting']],
    ]) {
      await page.locator('.pane .tab:has-text("Kanban")').first().click()
      await page.click('[data-testid=kanban-new]')
      await page.fill('[data-testid=kanban-title]', title)
      await page.click('[data-testid=kanban-create]')
      await page.waitForSelector(`[data-testid=ticket-view] [data-testid=ticket-title]:has-text("${title}")`)
      if (plan) {
        for (const g of goals) {
          await page.fill('[data-testid=ticket-goal-input]', g)
          await page.keyboard.press('Enter')
        }
        await page.click('[data-testid=ticket-plan-edit]')
        await page.fill('.tk-md-input', plan)
        await page.click('[data-testid=ticket-plan-save]')
        await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")')
      }
    }
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await widenRight(page)
    await page.waitForSelector(`[data-testid=model-pill]:has-text("${llm.rec.model}")`)

    // 1. Briefing: the need in a few words, the model asks, then writes the ticket.
    let stop = await screencast.start(page)
    await page.click('.ai-composer textarea')
    await page.keyboard.press('Shift+Tab')
    await page.keyboard.press('Shift+Tab')
    await page.waitForSelector('[data-testid=ai-mode].briefing')
    await say(page, BRIEF)
    let asked = false
    for (let round = 0; round < 6 && !(await page.isVisible('[data-testid=ai-ticket-bar]')); round++) {
      await idle(page)
      if (await page.isVisible('[data-testid=ai-ask-question]')) {
        if (!asked) await shot(page, 'briefing')
        asked = true
        await answer(page)
      } else if (!(await page.isVisible('[data-testid=ai-ticket-bar]'))) await say(page, 'Yes, write the ticket.')
    }
    await idle(page)
    await page.waitForTimeout(1000)
    await stop(path.join(IMAGES, 'briefing.gif'), { max: 30 })
    const n = Number(/#(\d+)/.exec(await page.textContent('[data-testid=ai-ticket-bar]'))[1])
    console.log(`ticket #${n} written`)

    // 2. Plan: written by the model from the ticket.
    await page.click(`[data-testid=ticket-card-${n}]`)
    await page.waitForSelector('[data-testid=ticket-view]')
    await page.click('[data-testid=ticket-plan-generate]')
    await page.waitForSelector('[data-testid=ticket-status]:has-text("To do")', { timeout: LONG })
    await idle(page)
    // The ticket view grows a moment after the plan is saved: scroll once it is laid out.
    await page.waitForTimeout(1500)
    const planHead = page.locator('.tk-section-toggle', { hasText: 'Implementation plan' }).first()
    for (let i = 0; i < 5; i++) {
      // Just below the header of the ticket, which stays at the top of the view.
      await planHead.evaluate((e) => {
        e.scrollIntoView({ block: 'start' })
        let p = e.parentElement
        while (p && !/auto|scroll/.test(getComputedStyle(p).overflowY)) p = p.parentElement
        if (p) p.scrollTop -= 150
      })
      await page.waitForTimeout(500)
      const y = (await planHead.boundingBox()).y
      if (y > 190 && y < 260) break
    }
    await shot(page, 'plan')

    // 3. Development in the worktree window, by the model.
    const [win] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-testid=ticket-start]')])
    win.on('dialog', (d) => d.accept())
    await win.waitForSelector('[data-testid=worktree-banner]', { timeout: 60_000 })
    await win.waitForSelector('.ai-composer')
    await widenRight(win)
    // The file the model changes, open beside the conversation: its edits show up live.
    await openAt(win, 'pod/main.go', 'func main')
    stop = await screencast.start(win, { speed: 2 })
    const toTest = () => page.isVisible('[data-testid=ticket-status]:has-text("To test")')
    for (let round = 0; round < 4 && !(await toTest()); round++) {
      await win.waitForTimeout(3000)
      await idle(win)
      if (await win.isVisible('[data-testid=ai-ask-question]')) await answer(win)
      else if (!(await toTest())) await say(win, 'Go on until the ticket is ready to test.')
    }
    await idle(win)
    await win.waitForTimeout(1000)
    await stop(path.join(IMAGES, 'develop.gif'), { width: 900, fps: 5, max: 40 })
    await openAt(win, 'pod/main.go', 'func reachable')
    await win.evaluate(() => document.querySelectorAll('.ai-messages').forEach((e) => (e.scrollTop = e.scrollHeight)))
    await win.waitForTimeout(800)
    await shot(win, 'develop')

    // 4. Test: the ticket shows how to test it and the change; merge, then close.
    await page.bringToFront()
    await page.waitForSelector('[data-testid=ticket-diff-file]', { timeout: 30_000 })
    await page.locator('[data-testid=ticket-diff-file] .tk-file-head').first().click()
    await page.waitForTimeout(800)
    await shot(page, 'review')
    await page.click('[data-testid=ticket-merge]')
    await page.waitForSelector('[data-testid=ticket-merged]', { timeout: 30_000 })
    await page.click('[data-testid=ticket-close]')
    await page.waitForSelector('[data-testid=ticket-status]:has-text("Done")', { timeout: 30_000 })
    await win.close().catch(() => {})

    // 5. The editor: completion by the language server, a terminal below; explorer instead of
    // the kanban, no assistant.
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.click('.rail-left .rail-btn[title="Explorer"]')
    await openAt(page, 'pod/main.go', 'flag.Parse()')
    await page.keyboard.press('Control+Shift+Backquote')
    await page.waitForSelector('.xterm', { timeout: 10_000 })
    await page.waitForTimeout(1500)
    // A neutral prompt rather than the one of the user running the scenario.
    await page.keyboard.type("PS1='$ '; clear\n")
    await page.waitForTimeout(500)
    await page.keyboard.type('git log --oneline --graph -6\n')
    await page.waitForTimeout(800)
    await page.click('.pane.active .ed-content')
    await openAt(page, 'pod/main.go', 'flag.Parse()')
    await page.keyboard.press('End')
    await page.keyboard.press('Enter')
    await page.keyboard.type('log.Pri', { delay: 60 })
    await page.waitForSelector('.completion-item', { timeout: 60_000 })
    await page.waitForTimeout(800)
    await shot(page, 'editor')
    await page.keyboard.press('Escape')
    for (let i = 0; i < 4; i++) await page.keyboard.press('Control+z')
    await page.click('.rail-left .rail-btn[title="Kanban"]')

    // 6. The board with the done ticket, without the side panels; the Git history with the
    // merged branch and the diff of the merge.
    await page.click('.rail-btn[title="Console"]')
    await page.click('.rail-left .rail-btn[title="Kanban"]')
    await page.locator('.pane .tab:has-text("Kanban")').first().click()
    await page.click('button:has-text("Done and abandoned")')
    await page.waitForTimeout(800)
    await shot(page, 'kanban')
    await page.click('.rail-left .rail-btn[title="Git"]')
    await page.click('[data-testid=git-tab-history]')
    await page.click('.git-log-row:has-text("Merge #")')
    await page.waitForSelector('[data-testid=git-detail] .git-message')
    await page.click('[data-testid=git-commit-files] .tree-row:has(.tree-name:text-is("main.go"))')
    await page.waitForSelector('.diff-view .diff-line.add', { timeout: 10_000 })
    await page.waitForTimeout(800)
    await shot(page, 'git')
    await page.click('.menubar button[title="Projects"]')
    await page.waitForSelector('.project-card')
    await page.waitForTimeout(800)
    await shot(page, 'home', { x: 0, y: 0, width: VIEW.width, height: 440 })
    console.log('shots written in docs/images (merge in the copy: ' + git(repo, 'log', '--oneline', '-1') + ')')
  } catch (e) {
    await page.screenshot({ path: path.join(tmp, 'failure.png') }).catch(() => {})
    console.error(e)
    console.error('failure screenshot and pod log in ' + tmp)
    process.exitCode = 1
  } finally {
    await browser.close()
    pod.kill()
    llm.close()
    if (!process.exitCode) fs.rmSync(tmp, { recursive: true, force: true })
  }
})()
