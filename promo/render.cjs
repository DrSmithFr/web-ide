// Renders the promo: stage.html draws each frame (timeline.js), Playwright takes it, ffmpeg
// encodes the video. Writes out/webide-promo.mp4 (muted, for TikTok: the sound is added
// there) and out/webide-promo-preview.mp4 (with the track, to check the sync; private).
//
//   node promo/render.cjs [track.mp3] [--from s --to s] [--sheet]
const fs = require('fs')
const os = require('os')
const path = require('path')
const { spawn, execFileSync } = require('child_process')
const { chromium } = require('playwright-core')

const HERE = __dirname
const OUT = path.join(HERE, 'out')
const FPS = 30
const args = process.argv.slice(2)
const opt = (name) => (args.includes(name) ? Number(args[args.indexOf(name) + 1]) : undefined)
const track = args.find((a) => a.endsWith('.mp3')) ?? fs.readdirSync(HERE).filter((f) => f.endsWith('.mp3')).map((f) => path.join(HERE, f))[0]

function chrome() {
  if (process.env.CHROME) return process.env.CHROME
  const cache = path.join(os.homedir(), '.cache/ms-playwright')
  const dirs = fs.existsSync(cache) ? fs.readdirSync(cache).filter((d) => /^chromium-\d+$/.test(d)).sort().reverse() : []
  for (const d of dirs) if (fs.existsSync(path.join(cache, d, 'chrome-linux64/chrome'))) return path.join(cache, d, 'chrome-linux64/chrome')
  throw new Error('Chromium not found: set CHROME=/path/to/chrome')
}

;(async () => {
  const beats = JSON.parse(fs.readFileSync(path.join(OUT, 'beats.json'), 'utf8'))
  const browser = await chromium.launch({ executablePath: chrome(), headless: true, args: ['--allow-file-access-from-files'] })
  const page = await browser.newPage({ viewport: { width: 1080, height: 1920 } })
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto('file://' + path.join(HERE, 'stage.html'))
  await page.evaluate((b) => window.load(b), beats)
  const length = Math.ceil(beats.duration * FPS)
  const from = Math.round((opt('--from') ?? 0) * FPS)
  const to = Math.min(length, Math.round((opt('--to') ?? beats.duration) * FPS))

  if (args.includes('--sheet')) {
    // A contact sheet of frames, to look at the edit without the video.
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'promo-'))
    const step = Number(opt('--step') ?? 6)
    let n = 0
    for (let f = from; f < to; f += step, n++) {
      await page.evaluate(([t]) => window.renderAt(t), [f / FPS])
      await page.screenshot({ path: path.join(dir, `${String(n).padStart(4, '0')}.jpg`), type: 'jpeg', quality: 70 })
    }
    const sheet = path.join(OUT, `sheet-${from}-${to}.jpg`)
    execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-framerate', '1', '-i', path.join(dir, '%04d.jpg'), '-vf', `scale=216:384,tile=10x${Math.ceil(n / 10)}`, '-frames:v', '1', sheet])
    fs.rmSync(dir, { recursive: true, force: true })
    console.log('sheet: ' + sheet)
  } else {
    const video = path.join(OUT, 'webide-promo.mp4')
    const ff = spawn('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'image2pipe', '-framerate', String(FPS), '-i', '-', '-c:v', 'libx264', '-preset', 'slow', '-crf', '16', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', video], { stdio: ['pipe', 'inherit', 'inherit'] })
    for (let f = from; f < to; f++) {
      await page.evaluate(([t]) => window.renderAt(t), [f / FPS])
      const buf = await page.screenshot({ type: 'jpeg', quality: 94 })
      if (!ff.stdin.write(buf)) await new Promise((ok) => ff.stdin.once('drain', ok))
      if (f % 60 === 0) process.stdout.write(`\r${f}/${to} frames`)
    }
    ff.stdin.end()
    await new Promise((ok, ko) => ff.on('close', (code) => (code ? ko(new Error('ffmpeg ' + code)) : ok())))
    console.log('\nvideo: ' + video)
    if (track && from === 0) {
      const preview = path.join(OUT, 'webide-promo-preview.mp4')
      execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-i', video, '-i', track, '-map', '0:v', '-map', '1:a', '-c:v', 'copy', '-c:a', 'aac', '-b:a', '192k', '-shortest', preview])
      console.log('preview with the track: ' + preview)
    }
  }
  await browser.close()
  if (errors.length) throw new Error(errors.join('\n'))
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
