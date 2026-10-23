// Languages: a French browser gets the French interface; the language setting switches it
// at once (menus, panels) and is kept by the pod; English is the fallback; Spanish is
// selectable. A plugin's texts come from its own catalog.
const { run, openProject, assert, OUT } = require('../common.cjs')

run(
  async ({ page }) => {
    await page.waitForSelector('.home .home-bar')
    assert((await page.textContent('.home-bar h2')) === 'Projets', 'French browser: French home page')
    await openProject(page)
    assert((await page.textContent('.menubar .menus')).includes('Fichier'), 'French menus')
    assert(await page.isVisible('.rail-left .rail-btn[title="Explorateur"]'), 'French panel names')
    // The texts of a plugin come from its own catalog (plugins/files/i18n).
    await page.waitForSelector('[data-testid=explorer-collapse]')
    assert((await page.getAttribute('[data-testid=explorer-collapse]', 'title')) === 'Tout replier', 'French texts of a plugin, from its catalog')

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

    // Spanish: listed by its native name, the interface and the lang attribute follow.
    await page.click('.menubar button[title^="Settings"]')
    await page.click('.settings-nav button:has-text("Language")')
    await page.selectOption('[data-testid=settings-language]', 'es')
    await page.waitForFunction(() => document.querySelector('.menubar .menus')?.textContent.includes('Archivo'))
    assert(await page.isVisible('.settings-nav button:has-text("Idioma")'), 'settings switched to Spanish at once')
    assert(await page.isVisible('.rail-left .rail-btn[title="Explorador"]'), 'panels switched to Spanish')
    assert((await page.getAttribute('html', 'lang')) === 'es', 'lang attribute is es')
    assert((await page.textContent('[data-testid=settings-language]')).includes('Español'), 'Español in the list')
  },
  { locale: 'fr-FR' },
)
