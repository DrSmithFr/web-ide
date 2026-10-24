// Shared services (#78) on the home page: a service shared (services.json), a port shared twice
// refused, turned off and on, renamed, its route refused to the pod's own origin even with the
// token (the pods of the account only), removed; the section in French.
const fs = require('fs')
const path = require('path')
const { run, start, assert, WS } = require('../../../e2e/common.cjs')

const file = path.join(WS, '..', 'data', 'services.json')
const saved = () => (fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : [])
const port = 39417

run(async ({ page }) => {
  await page.waitForSelector('[data-testid=home-services]')
  assert((await page.textContent('[data-testid=home-services]')).includes('Nothing shared'), 'nothing shared by default')

  // Shared: the row, the file.
  await page.click('[data-testid=service-add]')
  await page.fill('[data-testid=service-name]', 'Ollama')
  await page.fill('[data-testid=service-port]', String(port))
  await page.click('[data-testid=service-save]')
  await page.waitForSelector(`[data-testid=service-${port}]`)
  const row = `[data-testid=service-${port}]`
  assert((await page.textContent(row)).includes(`127.0.0.1:${port}`), 'the row shows the address')
  let list = saved()
  assert(list.length === 1 && list[0].name === 'Ollama' && list[0].port === port && list[0].enabled, 'services.json: ' + JSON.stringify(list))
  assert((fs.statSync(file).mode & 0o777) === 0o600, 'services.json is private')
  const id = list[0].id

  // The same port twice: refused.
  await page.click('[data-testid=service-add]')
  await page.fill('[data-testid=service-name]', 'Other')
  await page.fill('[data-testid=service-port]', String(port))
  await page.click('[data-testid=service-save]')
  await page.waitForSelector('.toast-error:has-text("is shared already, as Ollama")')
  await page.click('[data-testid=service-form] button:has-text("Cancel")')

  // Off, then on again.
  await page.uncheck(`[data-testid=service-on-${port}]`)
  await page.waitForSelector(`${row}.off`)
  assert(!saved()[0].enabled, 'off in services.json')
  await page.check(`[data-testid=service-on-${port}]`)
  await page.waitForSelector(`${row}:not(.off)`)

  // Renamed.
  await page.click(`[data-testid=service-edit-${port}]`)
  await page.fill('[data-testid=service-name]', 'Ollama GPU')
  await page.click('[data-testid=service-save]')
  await page.waitForSelector(`${row}:has-text("Ollama GPU")`)
  assert(saved()[0].name === 'Ollama GPU' && saved()[0].id === id, 'renamed, same id')

  // The route of the pods refuses a window of the pod, token and all.
  const status = (await fetch(`${process.env.E2E_URL}/_peer/services/${id}/`, { headers: { Cookie: `webide_token=${process.env.E2E_TOKEN}` } })).status
  assert(status === 403, 'the own origin is refused: ' + status)

  // French.
  const fr = await start({ locale: 'fr-FR' })
  await fr.page.waitForSelector('[data-testid=home-services]')
  const text = await fr.page.textContent('[data-testid=home-services]')
  assert(text.includes('Services partagés') && text.includes('Ollama GPU'), 'in French: ' + text)
  await fr.browser.close()

  // Removed.
  page.once('dialog', (d) => d.accept())
  await page.click(`[data-testid=service-delete-${port}]`)
  await page.waitForSelector(row, { state: 'detached' })
  assert(saved().length === 0, 'removed from services.json')
})
