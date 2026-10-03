// Kanban and git: development started from a ticket creates its branch and worktree, opens
// the worktree as a project in its own window where the conversation runs; changes and
// diff in the ticket; closing removes the worktree and freezes the change.
const fs = require('fs')
const http = require('http')
const { execSync } = require('child_process')
const { run, openProject, assert, WS, OUT } = require('../common.cjs')

const repo = WS + '/demo'
const env = { ...process.env, GIT_AUTHOR_NAME: 'e2e', GIT_AUTHOR_EMAIL: 'e2e@x', GIT_COMMITTER_NAME: 'e2e', GIT_COMMITTER_EMAIL: 'e2e@x' }
const git = (cmd, cwd = repo) => execSync(`git ${cmd}`, { cwd, env, encoding: 'utf8' }).trim()
git('init -q -b main')
git('add -A')
git('commit -q -m "état initial"')

const requests = []
const sse = (res, delta) => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`)
function end(res, reason = 'stop') {
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reason }] })}\n\n`)
  res.write(`data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 200, completion_tokens: 10 } })}\n\n`)
  res.end('data: [DONE]\n\n')
}
const calls = (list) => list.map(([id, name, args], index) => ({ index, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }))
const fake = http.createServer(async (req, res) => {
  if (req.url === '/api/version') return res.writeHead(404).end()
  if (req.url === '/v1/models') return res.end(JSON.stringify({ data: [{ id: 'fake-model', status: { value: 'loaded' } }] }))
  if (req.url.startsWith('/props')) return res.end(JSON.stringify(req.url === '/props' ? { role: 'router' } : { default_generation_settings: { n_ctx: 32768 } }))
  let body = ''
  for await (const c of req) body += c
  const r = JSON.parse(body)
  requests.push(r)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  const msgs = r.messages
  const last = msgs[msgs.length - 1]
  const goal = Number((msgs[0].content.match(/\(id (\d+)\)/) ?? [])[1])
  if (last.role === 'user')
    return (
      sse(res, {
        tool_calls: calls([
          ['b1', 'bash', { command: "printf 'export\\n' > export.txt && git add -A && git commit -q -m '#1 export' && pwd" }],
          ['b2', 'kanban_link_commit', { hash: 'HEAD' }],
          ['b3', 'kanban_goal', { action: 'check', id: goal }],
          ['b4', 'kanban_move', { status: 'review', test_summary: 'Lire `export.txt`.' }],
        ]),
      }),
      end(res, 'tool_calls')
    )
  sse(res, { content: 'Fait.' })
  end(res)
})

run(async ({ page, ctx }) => {
  await new Promise((r) => fake.listen(0, '127.0.0.1', r))
  page.on('dialog', (d) => d.accept())
  try {
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="Assistant IA"]')
    await page.click('.ai-empty button:has-text("Ajouter un serveur")')
    await page.fill('.ai-servers input[name=url]', `127.0.0.1:${fake.address().port}`)
    await page.click('.ai-servers button:has-text("Ajouter")')
    await page.waitForSelector('.ai-server-row:has-text("127.0.0.1")')
    await page.click('.ai-servers .modal-head button')
    await page.waitForSelector('[data-testid=model-pill]:has-text("fake-model")')

    // Kanban settings: worktree setup command.
    await page.click('.rail-left .rail-btn[title="Kanban"]')
    await page.click('[data-testid=kanban-open-board]')
    await page.click('[data-testid=kanban-settings]')
    await page.fill('[data-testid=kanban-setup]', 'echo installé > setup.log')
    await page.click('[data-testid=kanban-settings-save]')

    await page.click('[data-testid=kanban-new]')
    await page.fill('[data-testid=kanban-title]', 'Export des données')
    await page.click('[data-testid=kanban-create]')
    await page.waitForSelector('[data-testid=ticket-view]')
    await page.fill('[data-testid=ticket-goal-input]', 'export.txt existe')
    await page.keyboard.press('Enter')
    await page.waitForSelector('[data-testid=ticket-goal]')
    await page.click('[data-testid=ticket-to-ready]')
    await page.waitForSelector('[data-testid=ticket-status]:has-text("À développer")')

    // Start: branch + worktree, its window opens and runs the conversation.
    const [win] = await Promise.all([ctx.waitForEvent('page'), page.click('[data-testid=ticket-start]')])
    const wt = repo + '/.ide/worktrees/1-export-des-donnees'
    await win.waitForSelector('[data-testid=worktree-banner]:has-text("#1")', { timeout: 15000 })
    assert(true, 'fenêtre du worktree ouverte avec son bandeau')
    assert(fs.existsSync(wt + '/src/main.go'), 'worktree créé dans .ide/worktrees')
    assert(git('branch --show-current', wt) === 'ticket/1-export-des-donnees', 'branche du ticket')
    assert(git('status --porcelain') === '', 'le dossier principal reste propre (worktrees ignorés)')
    await win.waitForSelector('.ai-msg.assistant:not(.live) .md:has-text("Fait.")', { timeout: 20000 })
    const r0 = requests[0]
    const bashOut = (requests[1].messages.find((m) => m.tool_call_id === 'b1') ?? {}).content ?? ''
    assert(bashOut.includes('/.ide/worktrees/1-export-des-donnees'), 'les commandes tournent dans le worktree')
    assert(r0.messages[0].content.includes('Tu **développes** ce ticket sur la branche ticket/1-export-des-donnees'), 'prompt de développement avec la branche')
    assert(await win.isVisible('[data-testid=ai-ticket-bar]:has-text("#1")'), 'conversation liée au ticket dans la fenêtre du worktree')
    assert(fs.existsSync(wt + '/setup.log'), "commande d'initialisation lancée dans le worktree")

    // The ticket in the main window.
    await page.waitForSelector('[data-testid=ticket-status]:has-text("À tester")', { timeout: 10000 })
    await page.waitForSelector('[data-testid=ticket-git] [data-testid=ticket-branch]:has-text("ticket/1-export-des-donnees")')
    await page.waitForSelector('[data-testid=ticket-diff-file]:has-text("export.txt")', { timeout: 10000 })
    const summary = await page.textContent('[data-testid=ticket-diff-summary]')
    assert(summary.includes("1 commit(s) d'avance") && summary.includes('2 fichier(s)') && !summary.includes('non commitées'), 'résumé des changements : ' + summary)
    fs.writeFileSync(wt + '/brouillon.txt', 'wip\n')
    await page.click('[data-testid=ticket-git] >> xpath=ancestor::section[1] >> button[title="Rafraîchir"]')
    await page.waitForSelector('[data-testid=ticket-diff-summary]:has-text("non commitées")', { timeout: 5000 })
    assert(await page.isVisible('[data-testid=ticket-diff-file]:has-text("brouillon.txt") .tk-fst.s-U'), 'fichier non suivi listé, modifications non commitées signalées')
    await page.click('[data-testid=ticket-diff-file]:has-text("export.txt") .tk-file-head')
    await page.waitForSelector('.tk-patch .add:has-text("+export")')
    assert(true, 'diff du fichier affiché')
    assert((await page.textContent('.tk-side')).includes('#1 export'), 'commit lié par le modèle')
    await page.click('.tk-section-toggle:has-text("Historique")')
    assert((await page.textContent('.tk-events')).includes('Worktree initialisé') || (await page.textContent('.tk-events')).includes('Initialisation du worktree terminée'), 'initialisation dans l’historique')
    await page.screenshot({ path: OUT + '/kanban-git.png' })

    // Close: worktree removed, its window leaves, the change stays readable.
    await page.click('[data-testid=ticket-close]')
    await page.waitForSelector('[data-testid=ticket-status]:has-text("Terminé")', { timeout: 10000 })
    assert(!fs.existsSync(wt), 'worktree supprimé à la fermeture')
    assert(git('branch --list ticket/1-export-des-donnees') !== '', 'branche gardée')
    await win.waitForSelector('.home', { timeout: 5000 }).then(() => assert(true, 'la fenêtre du worktree se ferme'), () => assert(false, 'la fenêtre du worktree se ferme'))
    await page.waitForSelector('[data-testid=ticket-diff-file]:has-text("export.txt")', { timeout: 10000 })
    assert(true, 'changements toujours visibles après la fermeture')
  } finally {
    fake.close()
  }
})
