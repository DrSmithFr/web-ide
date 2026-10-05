// Animated GIFs of a page: Chromium sends a frame each time the page changes (CDP screencast),
// ffmpeg turns them into a GIF. Animations are stopped while recording; a frame lasts until the
// next one divided by `speed`, and a pause longer than idleGap (waiting for a command, a counter
// of seconds) lasts `idle` only, so the waits do not make the GIF longer.
const fs = require('fs')
const path = require('path')
const { execFileSync } = require('child_process')

const still = '*, *::before, *::after { animation: none !important; transition: none !important; caret-color: transparent !important }'

exports.start = async (page, { speed = 1, idleGap = 0.5, idle = 0.15 } = {}) => {
  const style = await page.addStyleTag({ content: still })
  const dir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'shots-'))
  const frames = []
  const cdp = await page.context().newCDPSession(page)
  cdp.on('Page.screencastFrame', async (f) => {
    const file = path.join(dir, `${String(frames.length).padStart(5, '0')}.png`)
    fs.writeFileSync(file, Buffer.from(f.data, 'base64'))
    frames.push({ file, ts: f.metadata.timestamp })
    await cdp.send('Page.screencastFrameAck', { sessionId: f.sessionId }).catch(() => {})
  })
  await cdp.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 })

  /**
   * Stops and writes the GIF: width in pixels, frames per second, length at most `max` seconds
   * (sped up beyond), the last frame stays `tail` seconds.
   */
  return async (out, { width = 1000, fps = 8, max = 45, tail = 3 } = {}) => {
    await page.waitForTimeout(300)
    await cdp.send('Page.stopScreencast').catch(() => {})
    await cdp.detach().catch(() => {})
    await style.evaluate((e) => e.remove()).catch(() => {})
    if (!frames.length) throw new Error('screencast: no frame')
    let d = frames.map((f, i) => {
      const gap = i + 1 < frames.length ? frames[i + 1].ts - f.ts : 0
      return gap > idleGap ? idle : gap / speed
    })
    const total = d.reduce((a, b) => a + b, 0)
    if (total > max) d = d.map((x) => (x * max) / total)
    d[d.length - 1] = tail
    const list = frames.map((f, i) => `file '${f.file}'\nduration ${d[i].toFixed(3)}`)
    // The concat demuxer ignores the duration of the last entry unless the file is repeated.
    list.push(`file '${frames[frames.length - 1].file}'`)
    fs.writeFileSync(path.join(dir, 'list.txt'), list.join('\n') + '\n')
    const filter = `fps=${fps},scale=${width}:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle`
    execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', path.join(dir, 'list.txt'), '-vf', filter, '-loop', '0', out])
    fs.rmSync(dir, { recursive: true, force: true })
  }
}
