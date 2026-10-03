// Languages: a French browser gets the French interface; the language setting switches it
// at once (menus, panels) and is kept by the pod; English is the fallback.
const { run, openProject, assert, OUT } = require('../common.cjs')

run(
  async ({ page }) => {
    await page.waitForSelector('.home .home-bar')
    assert((await page.textContent('.home-bar h2')) === 'Projets', 'French browser: French home page')
    await openProject(page)
    assert((await page.textContent('.menubar .menus')).includes('Fichier'), 'French menus')
    assert(await page.isVisible('.rail-left .rail-btn[title="Explorateur"]'), 'French panel names')

    // Settings → Language → English: the interface follows without a reload.
    await page.click('.menubar button[title^="Réglages"]')
    await page.click('.settings-nav button:has-text("Langue")')
    await page.selectOption('[data-testid=settings-language]', 'en')
    await page.waitForFunction(() => document.querySelector('.menubar .menus')?.textContent.includes('File'))
    assert(await page.isVisible('.settings-nav button:has-text("Language")'), 'settings switched to English at once')
    assert(await page.isVisible('.rail-left .rail-btn[title="Explorer"]'), 'panels switched to English')
    assert((await page.getAttribute('html', 'lang')) === 'en', 'lang attribute of the page')
    await page.screenshot({ path: OUT + '/i18n-english.png' })

    // Kept by the pod: still English after a reload, whatever the browser says.
    await page.waitForTimeout(800)
    await page.reload()
    await page.waitForSelector('.menubar')
    await page.waitForFunction(() => document.querySelector('.menubar .menus')?.textContent.includes('File'), null, { timeout: 5000 }).catch(() => {})
    assert((await page.textContent('.menubar .menus')).includes('File'), 'language kept after a reload')
  },
  { locale: 'fr-FR' },
)
