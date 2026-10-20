// Help: the documentation embedded in the pod opens read only in a tab, from the Help menu, the
// palette and the "?" of the interface; relative links, Back, anchors and pictures stay inside the docs.
const { run, openProject, assert, OUT } = require('../common.cjs')

run(async ({ page }) => {
  await openProject(page)
  const doc = () => page.getAttribute('[data-testid=doc-view]', 'data-doc')
  const title = () => page.textContent('.tab.active .tab-title')
  const shown = (path) => page.waitForFunction((p) => document.querySelector('[data-testid=doc-view]')?.dataset.shown === p, path)
  const top = (id) => page.$eval(`[data-testid=doc-view] [id="${id}"]`, (e) => Math.round(e.getBoundingClientRect().top - document.querySelector('.doc-scroll').getBoundingClientRect().top))

  // Help > Documentation: the index in a tab, titled by its first heading.
  await page.click('.menubar .menu-btn:has-text("Help")')
  await page.click('.ctx-item:has-text("Documentation")')
  await shown('docs/README.md')
  assert((await title()) === 'Documentation', 'the tab is titled by the page: ' + (await title()))
  assert(!(await page.$('[data-testid=doc-view] .ed-content')), 'the docs have no editor: read only')
  await page.screenshot({ path: OUT + '/help-index.png' })

  // A relative link stays in the docs, Back returns.
  await page.click('[data-testid=doc-view] a[href="guide.md"]')
  await shown('docs/guide.md')
  assert((await title()) === 'User guide', 'the page opens in the same tab: ' + (await title()))
  assert((await page.$$eval('.tab-kind.kind-doc', (l) => l.length)) === 1, 'one doc tab')
  // A picture of the docs is read through the pod.
  await page.waitForFunction(() => [...document.querySelectorAll('[data-testid=doc-view] img')].some((i) => i.src.startsWith('data:image/')))
  // A link to a heading of the same page scrolls to it.
  await page.click('[data-testid=doc-view] a[href="#where-the-data-lives"]')
  await page.waitForFunction(() => {
    const e = document.querySelector('[data-testid=doc-view] [id="where-the-data-lives"]')
    return e.getBoundingClientRect().top - document.querySelector('.doc-scroll').getBoundingClientRect().top < 60
  })
  await page.click('[data-testid=doc-back]')
  await shown('docs/README.md')

  // A page of another folder, then the pages of the code and the glossary, all from links.
  await page.click('[data-testid=doc-view] a[href="howto/lan-pods.md"]')
  await shown('docs/howto/lan-pods.md')
  await page.click('[data-testid=doc-index]')
  await shown('docs/README.md')
  await page.click('[data-testid=doc-view] a[href="../pod/README.md"]')
  await shown('pod/README.md')
  await page.click('[data-testid=doc-view] a[href="packages.md"]')
  await shown('pod/packages.md')
  await page.click('[data-testid=doc-view] a[href="../GLOSSARY.md"]')
  await shown('GLOSSARY.md')

  // The palette has the action, which shows the page in the same tab.
  await page.keyboard.press('Control+Shift+a')
  await page.waitForSelector('.pick.se')
  await page.keyboard.type('User guide')
  await page.waitForSelector('.pick-item:has-text("User guide")')
  await page.keyboard.press('Enter')
  await shown('docs/guide.md')
  assert((await page.$$eval('.tab-kind.kind-doc', (l) => l.length)) === 1, 'still one doc tab')

  // The "?" of a settings section: the window closes, the page opens at its heading.
  await page.keyboard.press('Control+Alt+s')
  await page.waitForSelector('.settings')
  await page.click('.settings-nav button:has-text("Projects folder and pod")')
  await page.click('[data-testid=help-settings\\.projects]')
  await page.waitForFunction(() => !document.querySelector('.settings'))
  await shown('docs/guide.md#projects')
  const t = await top('projects')
  assert(t >= 0 && t < 60, 'the "?" scrolls to its heading (top ' + t + ')')

  // Remote access: the page of the account and devices.
  await page.keyboard.press('Control+Alt+s')
  await page.waitForSelector('.settings')
  await page.click('.settings-nav button:has-text("Remote access")')
  await page.click('[data-testid=help-settings\\.remote\\.lan]')
  await shown('docs/howto/lan-pods.md')
  assert((await title()) === 'Pods on the local network', 'Local network "?": ' + (await title()))

  // The kanban and the Agents tool have theirs.
  await page.click('.menubar .menu-btn:has-text("Tools")')
  await page.click('.ctx-item:has-text("Agents")')
  await page.waitForSelector('[data-testid=help-agents]')
  await page.click('[data-testid=help-agents]')
  await shown('docs/guide.md#agents-claude-code-opencode-pi')
  const a = await top('agents-claude-code-opencode-pi')
  assert(a >= 0 && a < 60, 'the Agents "?" scrolls to its heading (top ' + a + ')')
  await page.click('.rail-left .rail-btn[title="Kanban"]')
  await page.click('[data-testid=kanban-open-board]')
  await page.waitForSelector('[data-testid=help-kanban]')
  await page.click('[data-testid=help-kanban]')
  await shown('docs/design/kanban.md')

  // The tab comes back after a reload.
  await page.reload()
  await shown('docs/design/kanban.md')
  await page.screenshot({ path: OUT + '/help-kanban.png' })
})
