// The edit of the promo: renderAt(t) draws the frame at t seconds, the same every time (the
// noise is seeded by the frame). The times come from out/beats.json (analyze.py): the beat
// grid, the kicks of the low band and the sections of the track.
//
// Whole screens of the IDE, never cropped: each view exists in two themes (High contrast and
// Day) and the flash is the switch between them on the eighths. The frame follows the theme:
// black with white type on High contrast, white with black type on Day. Black and white only
// (the shots in grayscale), punches, shakes, slices and ghosts on the hits.
const W = 1080
const H = 1920
const canvas = document.getElementById('stage')
const ctx = canvas.getContext('2d')
const off = Object.assign(document.createElement('canvas'), { width: W, height: H })
const offCtx = off.getContext('2d')

// ---------- data ----------

let BEATS = null
const img = {}
const VIEWS = ['editor', 'plan-chat', 'cloud', 'orchestrator', 'ask', 'kanban', 'roadmap', 'worktree', 'buffer', 'subagents', 'preview', 'phone-editor', 'phone-assistant', 'phone-kanban']

window.load = async (beats) => {
  BEATS = beats
  const files = VIEWS.flatMap((v) => [`${v}-hc`, `${v}-day`]).concat(['app'])
  await Promise.all(
    files.map(
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
const ink = (dark) => (dark ? '#fff' : '#000')
const paper = (dark) => (dark ? '#000' : '#fff')

/** Giant text centred on y, fitted to the width. */
function word(c, text, y, { size = 300, color = '#fff', font = 'Anton', scale = 1, alpha = 1, width = 0.9 } = {}) {
  c.save()
  c.globalAlpha = alpha
  c.font = `${size}px ${font}`
  c.textAlign = 'center'
  c.textBaseline = 'middle'
  const fit = Math.min(1, (W * width) / c.measureText(text).width) * scale
  c.translate(W / 2, y)
  c.scale(fit, fit)
  c.fillStyle = color
  c.fillText(text, 0, 0)
  c.restore()
}

/** Lines of giant text stacked around y. */
function lines(c, list, y, opts = {}) {
  const gap = (opts.size ?? 300) * 0.92 * Math.min(1, opts.scale ?? 1)
  list.forEach((l, i) => word(c, l, y + (i - (list.length - 1) / 2) * gap, opts))
}

// Where to zoom in each desktop screen (fraction of the image) and how much.
const FOCUS = {
  editor: [0.36, 0.48, 2.3],
  'plan-chat': [0.87, 0.16, 2.6],
  cloud: [0.5, 0.25, 1.9],
  orchestrator: [0.87, 0.38, 2.5],
  ask: [0.87, 0.42, 2.3],
  kanban: [0.22, 0.32, 2.1],
  roadmap: [0.3, 0.26, 2.1],
  worktree: [0.82, 0.16, 2.4],
  buffer: [0.45, 0.3, 2.0],
  subagents: [0.87, 0.4, 2.4],
  preview: [0.87, 0.34, 2.4],
}

/**
 * A whole screen of the IDE in a theme, grayscale: a desktop one across the width (the type
 * goes above and below it), then zoomed in on its subject (z from 0, the whole screen, to 1);
 * a phone one over the whole frame. punch: zoom of the cut.
 */
function screen(c, view, dark, punch = 1, dx = 0, dy = 0, z = 0) {
  const i = img[`${view}-${dark ? 'hc' : 'day'}`] ?? img[view]
  const phone = view.startsWith('phone') || view === 'app'
  const w0 = phone ? W : W * 0.96
  const h0 = (i.height / i.width) * w0
  const [fx, fy, target] = phone ? [0.5, 0.5, 1] : FOCUS[view] ?? [0.5, 0.5, 1]
  const k = ease(z)
  const w = w0 * (1 + (target - 1) * k) * punch
  const h = (i.height / i.width) * w
  // The subject moves from where it is in the whole screen to the centre of the frame.
  const sx = (W - w0) / 2 + fx * w0 + (W / 2 - ((W - w0) / 2 + fx * w0)) * k
  const sy = (H - h0) / 2 + fy * h0 + (H / 2 - ((H - h0) / 2 + fy * h0)) * k
  let x = sx - fx * w
  let y = sy - fy * h
  if (w > W) x = clamp(x, W - w, 0)
  if (h > H) y = clamp(y, H - h, 0)
  c.save()
  c.filter = 'grayscale(1) contrast(1.3)'
  c.drawImage(i, x + dx, y + dy, w, h)
  c.restore()
  if (!phone && k < 0.98) {
    // A thin frame around the window.
    c.save()
    c.strokeStyle = ink(dark)
    c.globalAlpha = 0.5 * (1 - k)
    c.lineWidth = 3
    c.strokeRect(x + dx, y + dy, w, h)
    c.restore()
  }
  return { phone, zoomed: k > 0.3, top: (H - h0) / 2, bottom: (H + h0) / 2 }
}

function invert(c) {
  c.save()
  c.globalCompositeOperation = 'difference'
  c.fillStyle = '#fff'
  c.fillRect(0, 0, W, H)
  c.restore()
}

/** Slices of the frame shifted sideways and a ghost of its edges (amount 0..1). */
function glitch(c, amount, seed) {
  if (amount <= 0) return
  offCtx.clearRect(0, 0, W, H)
  offCtx.drawImage(canvas, 0, 0)
  const r = rng(seed)
  const n = 6 + Math.floor(r() * 10)
  for (let k = 0; k < n; k++) {
    const y = Math.floor(r() * H)
    const h = 10 + Math.floor(r() * 140)
    c.drawImage(off, 0, y, W, h, (r() - 0.5) * 300 * amount, y, W, h)
  }
  c.save()
  c.globalAlpha = 0.45 * amount
  c.globalCompositeOperation = 'difference'
  c.drawImage(off, 22 * amount, -8 * amount)
  c.restore()
}

// Grain and scan lines.
const grain = Array.from({ length: 6 }, (_, k) => {
  const g = Object.assign(document.createElement('canvas'), { width: 540, height: 960 })
  const gc = g.getContext('2d')
  const d = gc.createImageData(540, 960)
  const r = rng(1000 + k)
  for (let i = 0; i < d.data.length; i += 4) {
    d.data[i] = d.data[i + 1] = d.data[i + 2] = r() * 255
    d.data[i + 3] = 255
  }
  gc.putImageData(d, 0, 0)
  return g
})
const scan = (() => {
  const g = Object.assign(document.createElement('canvas'), { width: W, height: H })
  const gc = g.getContext('2d')
  gc.fillStyle = '#000'
  for (let y = 0; y < H; y += 4) gc.fillRect(0, y, W, 1)
  return g
})()

// ---------- the edit ----------

let SCENES = []
let FLASHES = []
let GLITCHES = []

/**
 * A view with its words: the theme switches on every eighth (or sixteenth with fast), the
 * cut punches in, the type sits in the band above or below the screen (over a phone one).
 */
const view = (name, words, { fast = false, start = 0, sub = '', at = 'below', zoom = [0.15, 0.55] } = {}) => (c, p, f, t, t0) => {
  const step = BEATS.period / (fast ? 4 : 2)
  const dark = Math.floor((t - t0) / step + start) % 2 === 0
  c.fillStyle = paper(dark)
  c.fillRect(0, 0, W, H)
  const r = rng(f * 13)
  const kick = Math.max(0, 1 - (t - t0) / 0.18)
  const shake = 10 * kick
  const z = clamp((p - zoom[0]) / (zoom[1] - zoom[0]))
  const box = screen(c, name, dark, 1 + 0.07 * kick, (r() - 0.5) * shake, (r() - 0.5) * shake, z)
  const color = ink(dark)
  const scale = 1.1 - 0.1 * ease(p * 3)
  if (box.phone || box.zoomed) {
    c.save()
    c.globalCompositeOperation = 'difference'
    lines(c, words, H * 0.5, { size: 250, scale, color: '#fff' })
    c.restore()
  } else {
    const y = at === 'above' ? box.top / 2 : (box.bottom + H) / 2
    lines(c, words, y, { size: 230, scale, color })
    if (sub) word(c, sub, at === 'above' ? (box.bottom + H) / 2 : box.top / 2, { size: 58, font: 'JBMono', color })
  }
}

/** A word slammed on the frame, black or white. */
const slam = (words, dark, opts = {}) => (c, p, f) => {
  c.fillStyle = paper(dark)
  c.fillRect(0, 0, W, H)
  const r = rng(f * 7)
  c.save()
  c.translate((r() - 0.5) * 12, (r() - 0.5) * 12)
  lines(c, words, H / 2, { size: 330, scale: 1.2 - 0.2 * ease(p * 3), color: ink(dark), ...opts })
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
  const index = (t) => Math.round((t - BEATS.beats[0]) / P)
  const S = []
  const add = (from, to, draw) => S.push({ from, to, draw })
  const end = BEATS.end

  // 1. Intro: what it is, a phrase per two beats on the hits; the logo on the big one.
  const vStart = beat(index(verse.start))
  const intro = [
    [['OPENS', 'LIKE VIM.'], 'editor'],
    [['THINKS', 'LIKE', 'INTELLIJ.'], 'editor'],
    [['YOUR', 'MACHINE.'], 'worktree'],
  ]
  intro.forEach(([words, name], k) => {
    add(Math.max(0, beat(k * 2)), beat(k * 2 + 1), slam(words, k % 2 === 0))
    add(beat(k * 2 + 1), beat(k * 2 + 2), view(name, [], { fast: true, start: k }))
    GLITCHES.push({ t: beat(k * 2 + 1), dur: 0.1, amount: 0.7 })
  })
  add(beat(6), vStart, (c, p, f) => {
    slam(['WEB IDE'], false, { size: 380 })(c, p, f)
  })
  GLITCHES.push({ t: beat(6), dur: 0.35, amount: 1 })
  FLASHES.push({ t: beat(6), dur: 0.08, kind: 'invert' })

  // 2. Verse: one point per beat, its screens switching theme on the eighths.
  const points = [
    ['phone-editor', ['ACCESS', 'EVERYWHERE.'], { sub: 'over Tailscale' }],
    ['plan-chat', ['LOCAL AI.', 'EVERY DAY.'], { sub: 'your model, your machine' }],
    ['cloud', ['CLOUD AI', 'TO PLAN.'], { sub: 'when it is worth it' }],
    ['orchestrator', ['AI PLANS.'], { at: 'above' }],
    ['orchestrator', ['YOU', 'COMMAND.'], { start: 1 }],
    ['ask', ['IT', 'INTERROGATES', 'YOU.'], { at: 'above' }],
    ['kanban', ['AGILE.'], {}],
    ['roadmap', ['SIMPLIFIED.'], { at: 'above' }],
    ['worktree', ['GIT', 'WORKTREES.'], { sub: 'one per ticket' }],
    ['buffer', ['CODE WITH IT.', 'NOT AFTER IT.'], { at: 'above', sub: 'its edits merge with yours' }],
  ]
  let t = vStart
  for (const [name, words, opts] of points) {
    if (t >= rise.start - 0.05) break
    const to = Math.min(t + P, rise.start)
    // The last point holds until the build.
    add(t, name === 'buffer' ? rise.start : to, view(name, words, opts))
    GLITCHES.push({ t, dur: 0.09, amount: 0.7 })
    t = to
  }
  const clap = BEATS.onsets.filter(([o]) => o > verse.start && o < verse.end).sort((a, b) => b[1] - a[1])[0]
  if (clap) GLITCHES.push({ t: clap[0], dur: 0.2, amount: 1 })

  // 3. Build: the sub-agents multiply, then WALLET IS THE LIMIT. on the eighths.
  const card = (k) => ({ x: 2276, y: [470, 706, 942][k % 3], w: 492, h: 200 })
  const grid = (n) => (c, p, f, tt, t0) => {
    const dark = Math.floor((tt - t0) / (P / 2)) % 2 === 0
    c.fillStyle = paper(dark)
    c.fillRect(0, 0, W, H)
    const cols = Math.max(1, Math.round(Math.sqrt(n / 2.5)))
    const rows = Math.ceil(n / cols)
    const cw = W / cols
    const ch = (H * 0.78) / rows
    c.save()
    c.filter = 'grayscale(1) contrast(1.4)'
    for (let k = 0; k < n; k++) {
      const s = card(k)
      const scale = Math.min(cw / s.w, ch / s.h) * 0.9
      const x = (k % cols) * cw + (cw - s.w * scale) / 2
      const y = H * 0.17 + Math.floor(k / cols) * ch + (ch - s.h * scale) / 2
      c.drawImage(img[`subagents-${dark ? 'hc' : 'day'}`], s.x, s.y, s.w, s.h, x, y, s.w * scale, s.h * scale)
    }
    c.restore()
    word(c, `×${n} AGENTS`, H * 0.08, { size: 170, color: ink(dark), scale: 1.08 - 0.08 * ease(p * 3) })
  }
  const counts = [1, 2, 4, 8, 16, 32]
  const wallet = brk.start - 2 * P
  let k = 0
  for (t = rise.start; t < wallet - 0.05 && k < counts.length; k++) {
    const to = Math.min(t + P, wallet)
    add(t, to, grid(counts[k]))
    GLITCHES.push({ t, dur: 0.08, amount: 0.6 })
    t = to
  }
  const walletWords = [['WALLET'], ['IS THE'], ['LIMIT.']]
  for (let q = 0, tt = t; tt < brk.start - 0.01; q++) {
    const step = tt < brk.start - P ? P / 2 : P / 4
    add(tt, Math.min(tt + step, brk.start), slam(walletWords[q % 3], q % 2 === 1, { size: 380 }))
    tt += step
  }
  GLITCHES.push({ t: brk.start - P, dur: P, amount: 0.8 })

  // 4. Break: everything is done; it waits for you.
  const q = '> 5 agents done. waiting for you'
  add(brk.start, drop.start, (c, p, f) => {
    c.fillStyle = '#000'
    c.fillRect(0, 0, W, H)
    const n = Math.floor(q.length * clamp(p * 1.5))
    const r = rng(f)
    const shake = Math.pow(clamp((p - 0.5) / 0.5), 2) * 34
    c.save()
    c.translate((r() - 0.5) * shake, (r() - 0.5) * shake)
    c.font = '52px JBMono'
    c.textBaseline = 'middle'
    c.fillStyle = '#fff'
    const wdt = c.measureText(q + '_').width
    c.fillText(q.slice(0, n) + (f % 8 < 4 ? '_' : ' '), (W - wdt) / 2, H / 2)
    c.restore()
  })
  FLASHES.push({ t: drop.start - 0.04, dur: 0.2, kind: 'white' })

  // 5. Drop: a screen per eighth, the theme per sixteenth; a word per beat: you are the
  // bottleneck now.
  const dropViews = ['orchestrator', 'phone-kanban', 'kanban', 'phone-assistant', 'buffer', 'roadmap', 'subagents', 'phone-editor', 'worktree', 'preview', 'ask', 'editor']
  const dropWords = [['YOU'], ['ARE'], ['THE'], ['BOTTLE-'], ['NECK.'], ['NOW.']]
  const outro = beat(index(end)) - 2 * P
  let i = 0
  for (t = beat(index(drop.start)); t < outro - 0.01; t += P / 2, i++) {
    const words = dropWords[Math.min(Math.floor(i / 2), dropWords.length - 1)]
    add(Math.max(t, drop.start), Math.min(t + P / 2, outro), view(dropViews[i % dropViews.length], words, { fast: true, start: i, at: i % 2 ? 'above' : 'below', zoom: [-0.6, 0.6] }))
    if (i % 2 === 0) FLASHES.push({ t, dur: 0.06, kind: 'invert' })
    GLITCHES.push({ t, dur: 0.07, amount: i % 2 ? 0.9 : 0.5 })
  }
  // Outro: the name and the line, then black (the loop starts there).
  add(outro, end, (c, p, f) => {
    c.fillStyle = '#000'
    c.fillRect(0, 0, W, H)
    const r = rng(f)
    word(c, 'WEB IDE', H * 0.4 + (r() - 0.5) * 4, { size: 400, scale: 1.12 - 0.12 * ease(p * 2) })
    lines(c, ['YOU ARE THE', 'BOTTLENECK NOW.'], H * 0.57, { size: 110, alpha: clamp(p * 5) })
    word(c, 'self-hosted · local AI · open source', H * 0.7, { size: 46, font: 'JBMono', alpha: clamp(p * 4 - 0.6) })
    word(c, 'github.com/DrSmithFr/web-ide', H * 0.74, { size: 46, font: 'JBMono', alpha: clamp(p * 4 - 0.8) })
  })
  FLASHES.push({ t: outro, dur: 0.12, kind: 'white' })
  GLITCHES.push({ t: outro, dur: 0.25, amount: 1 })
  add(end, 1e9, (c) => {
    c.fillStyle = '#000'
    c.fillRect(0, 0, W, H)
  })

  // The kicks of the intro: short inversions.
  for (const [kt, s] of BEATS.kicks) if (s > 0.35 && kt < verse.start) FLASHES.push({ t: kt, dur: 0.05, kind: 'invert' })
  SCENES = S
}

// ---------- a frame ----------

window.renderAt = (t, fps = 30) => {
  const f = Math.round(t * fps)
  const s = SCENES.find((x) => t >= x.from && t < x.to) ?? SCENES[SCENES.length - 1]
  s.draw(ctx, (t - s.from) / Math.max(0.001, Math.min(s.to, 1e3) - s.from), f, t, s.from)
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
  ctx.globalAlpha = 0.12
  ctx.globalCompositeOperation = 'source-over'
  ctx.drawImage(scan, 0, 0)
  ctx.restore()
}
