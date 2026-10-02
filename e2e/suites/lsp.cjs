// Code navigation with gopls (skipped when gopls is not installed).
const { execSync } = require('child_process')
const { run, openProject, open, assert, OUT } = require('../common.cjs')
try {
  execSync('command -v gopls', { env: { ...process.env, PATH: process.env.HOME + '/go/bin:' + process.env.PATH }, stdio: 'ignore' })
} catch {
  console.log('  (gopls absent : suite ignorée)')
  process.exit(0)
}
run(async ({ page }) => {
  await openProject(page)
  await open(page, 'main.go')
  const caretOn = async (needle, delta = 1) => {
    await page.evaluate(([needle, delta]) => {
      const pre = document.querySelector('.pane.active .ed-content')
      pre.focus()
      for (const block of pre.children) {
        const node = block.firstChild
        const i = node.data.indexOf(needle)
        if (i >= 0) { getSelection().setBaseAndExtent(node, i + delta, node, i + delta); break }
      }
    }, [needle, delta])
    await page.waitForTimeout(100)
  }
  const line = () => page.textContent('.cursor-info')
  // gopls starts cold (module loading): Ctrl+B is retried until it answers.
  let jumped = false
  for (let i = 0; i < 20 && !jumped; i++) {
    await caretOn('.Hello())', 2)
    await page.keyboard.press('Control+b')
    jumped = await page.waitForFunction(() => document.querySelector('.cursor-info')?.textContent.startsWith('8:'), null, { timeout: 1500 }).then(() => true, () => false)
  }
  assert(jumped, 'Ctrl+B va à la déclaration (ligne 8) : ' + (await line()))
  // On the declaration: the usages (a single one: jump to it).
  await page.keyboard.press('Control+b')
  const usages = await page.waitForFunction(() => document.querySelector('.cursor-info')?.textContent.startsWith('13:') || document.querySelector('.pick'), null, { timeout: 8000 }).then(() => true, () => false)
  assert(usages, 'Ctrl+B sur la déclaration montre les usages')
  await page.keyboard.press('Escape')
  await page.click('.rail-right .rail-btn[title="Structure"]')
  const syms = await page
    .waitForFunction(() => {
      const names = [...document.querySelectorAll('.side-right .tree-row .tree-name')].map((x) => x.textContent)
      return names.includes('Greeter') && names.some((n) => n.includes('Hello')) && names
    }, null, { timeout: 8000 })
    .then((h) => h.jsonValue(), () => [])
  assert(syms.length > 0, 'la structure liste les symboles ' + JSON.stringify(syms))
  // Diagnostics
  await page.click('.pane.active .ed-content')
  await page.keyboard.press('Control+End')
  await page.keyboard.type('\nfunc broken() { return 1 }\n')
  const diag = await page
    .waitForFunction(() => CSS.highlights.get('diag-error')?.size, null, { timeout: 10000 })
    .then((h) => h.jsonValue(), () => 0)
  assert(diag > 0, `diagnostic souligné dans l'éditeur (${diag})`)
  await page.click('.bottom-toggle').catch(() => {})
  await page.click('.btab:has-text("Problèmes")')
  await page.waitForTimeout(300)
  assert((await page.$$('.problem')).length > 0, 'le panneau Problèmes liste le diagnostic')
  await page.screenshot({ path: OUT + '/s11-lsp.png' })
  for (let i = 0; i < 3; i++) await page.keyboard.press('Control+z')
})
