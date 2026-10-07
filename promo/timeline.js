// The edit of the promo: renderAt(t) draws the frame at t seconds, the same every time (the
// noise is seeded by the frame). The times come from out/beats.json (analyze.py): the beat
// grid, the kicks of the low band and the sections of the track. Black and white only:
// shots in grayscale with contrast, inversions and flashes on the hits, giant type (Anton).
const W = 1080
const H = 1920
const canvas = document.getElementById('stage')
const ctx = canvas.getContext('2d')
const off = Object.assign(document.createElement('canvas'), { width: W, height: H })
const offCtx = off.getContext('2d')

// ---------- data ----------

let BEATS = null
const img = {}
const SHOTS = ['editor', 'kanban', 'orchestrator-panel', 'ask-panel', 'subagents-panel', 'preview-panel', 'app', 'home', 'phone-explorer', 'phone-editor', 'phone-hint', 'phone-kanban']

window.load = async (beats) => {
  BEATS = beats
  await Promise.all(
    SHOTS.map(
      (name) =>
        new Promise((ok, ko) => {
          const i = new Image()
          i.onload = () => ok((img[name] = i))
          i.onerror = () => ko(new Error('shot missing: ' + name))
          i.src = `out/shots/${name}.png`
        }),
    ),
  )
  await document.fonts.load('200px Anton')
  await document.fonts.load('60px JBMono')
  build()
}

// ---------- helpers ----------

/** Seeded random numbers (mulberry32). */
function rng(seed) {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
const clamp = (x, a = 0, b = 1) => Math.max(a, Math.min(b, x))
const ease = (x) => 1 - Math.pow(1 - clamp(x), 3)
const beat = (i) => BEATS.beats[0] + i * BEATS.period

/**
 * A shot, cropped to the frame: src is the region of the image shown (x, y, w and the
 * height of the frame ratio), zoomed from z0 to z1 and panned by dy (fraction of the height)
 * over p in [0, 1].
 */
function shot(c, name, src, p, { z0 = 1, z1 = 1.1, dy = 0, contrast = 1.45, bright = 1 } = {}) {
  const i = img[name]
  const sw = src.w / (z0 + (z1 - z0) * ease(p))
  const sh = sw * (H / W)
  const cx = src.x + src.w / 2
  const cy = src.y + (src.w * H) / W / 2 + dy * p * i.height
  c.save()
  c.filter = `grayscale(1) contrast(${contrast}) brightness(${bright})`
  c.drawImage(i, cx - sw / 2, cy - sh / 2, sw, sh, 0, 0, W, H)
  c.restore()
}

/** Giant text centred on y, fitted to the width (max size px), white or black. */
function word(c, text, y, { size = 300, color = '#fff', stroke = 0, font = 'Anton', track = 0.02, scale = 1, alpha = 1 } = {}) {
  c.save()
  c.globalAlpha = alpha
  c.font = `${size}px ${font}`
  c.textAlign = 'center'
  c.textBaseline = 'middle'
  const wdt = c.measureText(text).width
  const fit = Math.min(1, (W * 0.9) / wdt) * scale
  c.translate(W / 2, y)
  c.scale(fit, fit)
  if (stroke) {
    c.lineWidth = stroke / fit
    c.strokeStyle = color === '#fff' ? '#000' : '#fff'
    c.strokeText(text, 0, 0)
  }
  c.fillStyle = color
  c.fillText(text, 0, 0)
  c.restore()
}

function invert(c) {
  c.save()
  c.globalCompositeOperation = 'difference'
  c.fillStyle = '#fff'
  c.fillRect(0, 0, W, H)
  c.restore()
}

/** Slices of the frame shifted sideways (amount 0..1). */
function glitch(c, amount, seed) {
  if (amount <= 0) return
  offCtx.clearRect(0, 0, W, H)
  offCtx.drawImage(canvas, 0, 0)
  const r = rng(seed)
  const n = 6 + Math.floor(r() * 8)
  for (let k = 0; k < n; k++) {
    const y = Math.floor(r() * H)
    const h = 20 + Math.floor(r() * 160)
    const dx = (r() - 0.5) * 260 * amount
    c.drawImage(off, 0, y, W, h, dx, y, W, h)
  }
  // A white ghost of the edges (the colour split, in black and white).
  c.save()
  c.globalAlpha = 0.35 * amount
  c.globalCompositeOperation = 'difference'
  c.drawImage(off, 14 * amount, 0)
  c.restore()
}

// Grain: a few noise tiles, one per frame.
const grain = Array.from({ length: 6 }, (_, k) => {
  const g = Object.assign(document.createElement('canvas'), { width: 540, height: 960 })
  const gc = g.getContext('2d')
  const d = gc.createImageData(540, 960)
  const r = rng(1000 + k)
  for (let i = 0; i < d.data.length; i += 4) {
    const v = r() * 255
    d.data[i] = d.data[i + 1] = d.data[i + 2] = v
    d.data[i + 3] = 255
  }
  gc.putImageData(d, 0, 0)
  return g
})

// ---------- the edit ----------

let SCENES = []
let FLASHES = []
let GLITCHES = []

/** A word slammed on a black frame: punch in, a little shake. */
const slam = (text, opts = {}) => (c, p, f) => {
  c.fillStyle = '#000'
  c.fillRect(0, 0, W, H)
  const r = rng(f * 7)
  c.save()
  c.translate((r() - 0.5) * 10, (r() - 0.5) * 10)
  word(c, text, H / 2, { size: 330, scale: 1.18 - 0.18 * ease(p * 3), ...opts })
  c.restore()
}

/** A UI shot with its giant word. */
const ui = (name, src, label, opts = {}) => (c, p) => {
  shot(c, name, src, p, opts)
  // The word in difference: white on dark, black on light.
  c.save()
  c.globalCompositeOperation = 'difference'
  word(c, label, opts.labelY ?? H * 0.8, { size: 260, scale: 1.08 - 0.08 * ease(p * 2) })
  c.restore()
}

function build() {
  const sec = BEATS.sections
  const at = (bass, k) => sec.filter((s) => s.bass === bass)[k]
  const verse = at(false, 0)
  const rise = at(true, 1)
  const brk = at(false, 1)
  const drop = at(true, 2)
  const P = BEATS.period
  const nearest = (t) => beat(Math.round((t - BEATS.beats[0]) / P))
  const vStart = nearest(verse.start)
  const kicks = BEATS.kicks

  // Crops of the shots (source pixels: the desktop shots are 4320×2700, the panels 900 wide,
  // the phone 1170×2532).
  const DESK = { x: 930, y: 40, w: 1480 }
  const PANEL = { x: 0, y: 80, w: 900 }
  const PHONE = { x: 0, y: 200, w: 1170 }
  const S = []
  const add = (from, to, draw) => S.push({ from, to, draw })

  // 1. Intro: a word per beat, black on white every other one; the logo on the big hit.
  const intro = ['YOUR', 'IDE.', 'YOUR', 'MODEL.', 'YOUR', 'MACHINE.', 'NO CLOUD.']
  for (let k = 0; k < intro.length; k++) {
    const inv = k % 2 === 1
    add(Math.max(0, beat(k)), beat(k + 1), (c, p, f) => {
      slam(intro[k])(c, p, f)
      if (inv) invert(c)
    })
  }
  add(beat(intro.length), vStart, (c, p, f) => {
    c.fillStyle = '#fff'
    c.fillRect(0, 0, W, H)
    word(c, 'WEB IDE', H / 2, { size: 380, color: '#000', scale: 1.25 - 0.25 * ease(p * 2) })
  })
  GLITCHES.push({ t: beat(intro.length), dur: 0.3, amount: 1 })

  // 2. Verse (no bass): one shot per beat, its word.
  const verseShots = [
    ['editor', DESK, 'CODE.', { z1: 1.18 }],
    ['kanban', { x: 960, y: 140, w: 1150 }, 'PLAN.'],
    ['orchestrator-panel', PANEL, 'STEER.', { labelY: H * 0.86 }],
    ['ask-panel', { x: 0, y: 400, w: 900 }, 'DECIDE.', { labelY: H * 0.86 }],
    ['subagents-panel', { x: 0, y: 250, w: 900 }, 'DELEGATE.', { labelY: H * 0.88 }],
    ['preview-panel', { x: 0, y: 300, w: 900 }, 'PREVIEW.'],
    ['app', PHONE, 'SHIP.', { labelY: H * 0.18 }],
    ['home', { x: 1500, y: 20, w: 1480 }, 'ANY PROJECT.'],
    ['phone-explorer', PHONE, 'ANYWHERE.'],
    ['phone-editor', PHONE, 'ON YOUR PHONE.', { labelY: H * 0.84 }],
  ]
  let t = vStart
  for (const [name, src, label, opts] of verseShots) {
    const next = t + P
    if (t >= rise.start) break
    add(t, Math.min(next, rise.start), ui(name, src, label, opts))
    FLASHES.push({ t, dur: 0.07, kind: 'white' })
    GLITCHES.push({ t, dur: 0.08, amount: 0.6 })
    t = next
  }
  // The clap of the verse: an inversion.
  const clap = BEATS.onsets.filter(([o]) => o > verse.start && o < verse.end).sort((a, b) => b[1] - a[1])[0]
  if (clap) FLASHES.push({ t: clap[0], dur: P * 0.9, kind: 'invert' })

  // 3. Build: the sub-agents multiply, FASTER on every eighth at the end.
  const cards = [80, 590, 945, 1300].map((y) => ({ x: 100, y, w: 790, h: 300 }))
  const bStart = rise.start
  const bEnd = brk.start
  const gridAt = (n) => (c, p, f) => {
    c.fillStyle = '#000'
    c.fillRect(0, 0, W, H)
    const cols = Math.ceil(Math.sqrt(n * 0.6))
    const rows = Math.ceil(n / cols)
    const cw = W / cols
    const ch = H / rows
    c.save()
    c.filter = 'grayscale(1) contrast(1.6)'
    for (let k = 0; k < n; k++) {
      const s = cards[k % 3 === 0 ? 0 : k % 3 === 1 ? 1 : 2]
      const x = (k % cols) * cw
      const y = Math.floor(k / cols) * ch
      const scale = Math.min(cw / s.w, ch / s.h) * 0.92
      c.drawImage(img['subagents-panel'], s.x, s.y + 60 + 250, s.w, s.h, x + (cw - s.w * scale) / 2, y + (ch - s.h * scale) / 2, s.w * scale, s.h * scale)
    }
    c.restore()
    c.save()
    c.globalCompositeOperation = 'difference'
    word(c, n === 1 ? '1 AGENT' : `${n} AGENTS`, H / 2, { size: 300, scale: 1.1 - 0.1 * ease(p * 3) })
    c.restore()
  }
  const counts = [1, 2, 4, 8, 16, 32]
  let k = 0
  for (t = bStart; t < bEnd - 2 * P && k < counts.length; t += P, k++) {
    add(t, Math.min(t + P, bEnd - 2 * P), gridAt(counts[k]))
    FLASHES.push({ t, dur: 0.06, kind: 'white' })
  }
  // FASTER: every eighth, then every sixteenth, inverting each time.
  for (let q = 0, tt = t; tt < bEnd; q++) {
    const step = tt < bEnd - P ? P / 2 : P / 4
    const label = ['SHIP', 'FASTER.', 'FASTER.', 'FASTER.'][q % 4]
    const inv = q % 2 === 1
    add(tt, Math.min(tt + step, bEnd), (c, p) => {
      c.fillStyle = inv ? '#fff' : '#000'
      c.fillRect(0, 0, W, H)
      word(c, label, H / 2, { size: 360, color: inv ? '#000' : '#fff', scale: 1.15 - 0.15 * ease(p) })
    })
    tt += step
  }
  GLITCHES.push({ t: bEnd - P, dur: P, amount: 0.7 })

  // 4. Break: the Orchestrator asks, typed in mono; the riser shakes the frame.
  const q = '> what do we work on today?'
  add(bEnd, drop.start, (c, p, f) => {
    c.fillStyle = '#000'
    c.fillRect(0, 0, W, H)
    const n = Math.floor(q.length * clamp(p * 1.6))
    const r = rng(f)
    const shake = Math.pow(clamp((p - 0.45) / 0.55), 2) * 30
    c.save()
    c.translate((r() - 0.5) * shake, (r() - 0.5) * shake)
    c.font = '64px JBMono'
    c.textAlign = 'left'
    c.textBaseline = 'middle'
    c.fillStyle = '#fff'
    const shown = q.slice(0, n) + (f % 8 < 4 ? '_' : ' ')
    const wdt = c.measureText(q + '_').width
    c.fillText(shown, (W - wdt) / 2, H / 2)
    c.restore()
  })
  FLASHES.push({ t: drop.start - 0.04, dur: 0.18, kind: 'white' })

  // 5. Drop: a cut on every eighth, an inversion on every beat, the words of the end.
  const dropShots = [
    ['phone-kanban', PHONE],
    ['orchestrator-panel', PANEL],
    ['app', PHONE],
    ['phone-hint', PHONE],
    ['editor', DESK],
    ['subagents-panel', { x: 0, y: 250, w: 900 }],
    ['phone-editor', PHONE],
    ['preview-panel', { x: 0, y: 300, w: 900 }],
  ]
  const dropWords = ['LOCAL AI.', 'YOUR MODEL.', 'ON YOUR PHONE.', 'OPEN SOURCE.']
  const end = BEATS.end
  const outro = nearest(end) - 2 * P
  let i = 0
  for (t = drop.start; t < outro - 0.01; t += P / 2, i++) {
    const [name, src] = dropShots[i % dropShots.length]
    const label = dropWords[Math.floor(i / 2) % dropWords.length]
    add(t, Math.min(t + P / 2, outro), ui(name, src, label, name.startsWith('phone') || name === 'app' ? { z0: 1, z1: 1.06 } : { z0: 1.05, z1: 1.2 }))
    if (i % 2 === 0) FLASHES.push({ t, dur: 0.1, kind: 'invert' })
    else GLITCHES.push({ t, dur: 0.07, amount: 0.8 })
  }
  // Outro: the name and the address, then black (the loop starts on black).
  add(outro, end, (c, p) => {
    c.fillStyle = '#000'
    c.fillRect(0, 0, W, H)
    word(c, 'WEB IDE', H * 0.44, { size: 380, scale: 1.12 - 0.12 * ease(p * 2) })
    word(c, 'SELF-HOSTED · LOCAL AI · OPEN SOURCE', H * 0.56, { size: 64, font: 'JBMono', alpha: clamp(p * 4) })
    word(c, 'github.com/DrSmithFr/web-ide', H * 0.62, { size: 54, font: 'JBMono', alpha: clamp(p * 4 - 0.5) })
  })
  FLASHES.push({ t: outro, dur: 0.12, kind: 'white' })
  GLITCHES.push({ t: outro, dur: 0.2, amount: 1 })
  add(end, 1e9, (c) => {
    c.fillStyle = '#000'
    c.fillRect(0, 0, W, H)
  })

  // The kicks of the intro and of the drop: short inversions.
  for (const [kt, s] of kicks) {
    if (s > 0.35 && kt < verse.start) FLASHES.push({ t: kt, dur: 0.05, kind: 'invert' })
  }
  SCENES = S
}

// ---------- a frame ----------

window.renderAt = (t, fps = 30) => {
  const f = Math.round(t * fps)
  const s = SCENES.find((x) => t >= x.from && t < x.to) ?? SCENES[SCENES.length - 1]
  s.draw(ctx, (t - s.from) / Math.max(0.001, Math.min(s.to, 1e3) - s.from), f)
  for (const g of GLITCHES) if (t >= g.t && t < g.t + g.dur) glitch(ctx, g.amount * (1 - (t - g.t) / g.dur), f)
  for (const fl of FLASHES) {
    if (t < fl.t || t >= fl.t + fl.dur) continue
    if (fl.kind === 'invert') invert(ctx)
    else {
      ctx.save()
      ctx.globalAlpha = 1 - (t - fl.t) / fl.dur
      ctx.fillStyle = '#fff'
      ctx.fillRect(0, 0, W, H)
      ctx.restore()
    }
  }
  ctx.save()
  ctx.globalAlpha = 0.07
  ctx.globalCompositeOperation = 'overlay'
  ctx.drawImage(grain[f % grain.length], 0, 0, W, H)
  ctx.restore()
}
