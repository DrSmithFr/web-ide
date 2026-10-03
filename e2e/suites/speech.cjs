// Local speech recognition: Whisper (tiny) in the browser, fed by a fake microphone playing
// e2e/audio/jfk.wav. Checks that the page only talks to the pod (the audio stays local) and
// that the model is cached by the pod.
const path = require('path')
const { run, openProject, assert, OUT } = require('../common.cjs')

const wav = path.join(__dirname, '../audio/jfk.wav')
const args = ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', `--use-file-for-fake-audio-capture=${wav}`]

run(
  async ({ page }) => {
    const origin = new URL(process.env.E2E_URL).origin
    const outside = []
    const uploads = []
    page.on('request', (r) => {
      const u = r.url()
      if (!u.startsWith(origin) && !u.startsWith('data:') && !u.startsWith('blob:')) outside.push(u)
      else if (r.method() !== 'GET') uploads.push(`${r.method()} ${u}`)
    })
    await openProject(page)
    await page.click('.rail-right .rail-btn[title="AI assistant"]')
    await page.waitForSelector('.ai-panel')

    // Settings: smallest model, English.
    await page.click('.ai-panel button[title^="Settings"]')
    await page.click('.ai-tab:has-text("Transcription")')
    await page.waitForSelector('[data-testid=speech-settings]')
    await page.selectOption('[data-testid=speech-settings] select[name=whisperModel]', 'tiny')
    await page.selectOption('[data-testid=speech-settings] select[name=whisperLang]', 'en')
    await page.click('.ai-servers .modal-head button')

    // Dictation with the fake microphone.
    await page.click('.ai-mic')
    await page.waitForSelector('[data-testid=ai-speech].rec')
    assert(true, 'recording shown')
    await page.waitForTimeout(11500)
    await page.click('.ai-mic')
    await page.waitForSelector('[data-testid=ai-speech]:not(.rec)', { timeout: 5000 }).catch(() => {})
    await page.screenshot({ path: OUT + '/speech-loading.png' })
    const ok = await page
      .waitForFunction(() => /ask not what.*can do for your country/i.test(document.querySelector('.ai-composer textarea').value), null, { timeout: 180000, polling: 500 })
      .then(() => true, () => false)
    const text = await page.inputValue('.ai-composer textarea')
    assert(ok, 'dictation transcribed into the message box: ' + text)
    await page.waitForSelector('[data-testid=ai-speech]', { state: 'detached', timeout: 5000 }).catch(() => {})
    assert(!(await page.isVisible('[data-testid=ai-speech]')), 'indicator removed after the transcription')

    // Audio file joined: transcribed in the page (the model is already loaded).
    await page.fill('.ai-composer textarea', '')
    await page.setInputFiles('.ai-composer input[type=file]', wav)
    await page.waitForSelector('.ai-composer .ai-att[title*="transcribed locally"]', { timeout: 60000 })
    assert(true, 'attached audio file transcribed locally')

    // The model is in the pod cache, listed in the settings.
    await page.click('.ai-panel button[title^="Settings"]')
    await page.click('.ai-tab:has-text("Transcription")')
    await page.waitForSelector('[data-testid=speech-settings] .ai-server-row:has-text("onnx-community/whisper-tiny")', { timeout: 5000 }).catch(() => {})
    assert(await page.isVisible('[data-testid=speech-settings] .ai-server-row:has-text("onnx-community/whisper-tiny")'), 'model listed in the cache of the pod')
    await page.screenshot({ path: OUT + '/speech-settings.png' })

    assert(outside.length === 0, 'no request outside the pod: ' + JSON.stringify(outside.slice(0, 5)))
    assert(uploads.length === 0, 'no HTTP upload (the sound stays in the page): ' + JSON.stringify(uploads.slice(0, 5)))
  },
  { args, permissions: ['microphone'] },
)
