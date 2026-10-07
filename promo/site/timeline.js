// The site video (1920×1080, silent): the scenes recorded by capture.cjs played back shot by
// shot, with a camera (centre and zoom, eased between keyframes), a drawn cursor on the clicks
// and one caption per step. renderAt(t) draws the time t the same way every time.
const W = 1920
const H = 1080
const cv = document.getElementById('stage')
const g = cv.getContext('2d')
let M // the manifest of the scenes

// A shot plays a range [from, to] of a scene (in seconds of the recording) over `dur` seconds;
// cam: keyframes [u, cx, cy, zoom] with u from 0 to 1 over the shot, in CSS px of the 1920×1080
// recording. `fade`: a cross-fade from the previous shot.
const SHOTS = [
  { scene: 'idea', from: 0, to: 1.2, dur: 4, cam: [[0, 960, 540, 1], [1, 1010, 560, 1.06]], caption: 'A full IDE, in your browser. Your machine, your AI.' },
  { scene: 'idea', from: 1.2, to: 8.45, dur: 6.5, cam: [[0, 1010, 560, 1.06], [0.12, 1420, 800, 1.9], [0.3, 1420, 800, 1.9], [0.45, 1420, 430, 1.9], [1, 1420, 470, 1.9]], caption: 'Tell it the idea. It questions you.' },
  { scene: 'idea', from: 8.45, to: 11.86, dur: 3.5, cam: [[0, 1420, 470, 1.9], [0.35, 860, 420, 1.45], [1, 760, 360, 1.6]], caption: 'The ticket lands on the board.' },
  { scene: 'plan', from: 0, to: 3.55, dur: 6.5, fade: true, cam: [[0, 1100, 540, 1.05], [0.25, 940, 360, 1.5], [0.6, 900, 700, 1.5], [1, 900, 760, 1.55]], caption: 'It plans: the need, the steps, the goals.' },
  { scene: 'go', from: 0, to: 2.6, dur: 4, fade: true, cam: [[0, 1420, 820, 2], [0.6, 1420, 760, 2], [1, 1420, 700, 1.9]], caption: "You say go." },
  { scene: 'dev', from: 0, to: 2.35, dur: 4.5, fade: true, cam: [[0, 960, 540, 1], [0.35, 1250, 420, 1.35], [1, 1300, 440, 1.4]], caption: 'An agent codes it, in its own worktree.' },
  { scene: 'go', from: 2.6, to: 5.88, dur: 4, fade: true, cam: [[0, 820, 320, 1.7], [0.35, 820, 300, 1.8], [0.55, 960, 540, 1.15], [1, 960, 540, 1.15]], caption: 'Follow it on the roadmap.' },
  { later: true, dur: 1.6 },
  { scene: 'dev', from: 2.35, to: 2.67, dur: 3, fade: true, cam: [[0, 1440, 560, 1.7], [1, 1500, 540, 2]], caption: 'Try it: a preview link on your tailnet.' },
  { app: true, dur: 2.6, caption: 'Try it: a preview link on your tailnet.' },
  { end: true, dur: 5 },
]
let start = 0
for (const s of SHOTS) (s.start = start), (start += s.dur)
window.DURATION = start

const ease = (x) => (x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2)
const clamp = (x, a, b) => Math.max(a, Math.min(b, x))

function camAt(cam, u) {
  let i = 0
  while (i < cam.length - 1 && cam[i + 1][0] <= u) i++
  const a = cam[i]
  const b = cam[Math.min(i + 1, cam.length - 1)]
  const k = b[0] > a[0] ? ease(clamp((u - a[0]) / (b[0] - a[0]), 0, 1)) : 0
  const z = a[3] + (b[3] - a[3]) * k
  // The view stays inside the recording.
  const hw = W / z / 2
  const hh = H / z / 2
  return { x: clamp(a[1] + (b[1] - a[1]) * k, hw, W - hw), y: clamp(a[2] + (b[2] - a[2]) * k, hh, H - hh), z }
}

// ---------- images, loaded on demand ----------

const cache = new Map()
function img(src) {
  if (!cache.has(src)) {
    const im = new Image()
    const p = new Promise((ok, ko) => ((im.onload = () => ok(im)), (im.onerror = () => ko(new Error('image ' + src)))))
    im.src = src
    cache.set(src, p)
    if (cache.size > 24) cache.delete(cache.keys().next().value)
  }
  return cache.get(src)
}
const frameAt = (scene, ts) => {
  const fr = M.scenes[scene].frames
  let i = 0
  while (i < fr.length - 1 && fr[i + 1].t <= ts) i++
  return '../out/site/' + fr[i].f
}

// ---------- drawing ----------

const font = (size, f = 'Anton') => `${size}px ${f}`

async function drawScene(s, u) {
  const ts = s.from + (s.to - s.from) * u
  const im = await img(frameAt(s.scene, ts))
  const c = camAt(s.cam, u)
  g.save()
  g.fillStyle = '#000'
  g.fillRect(0, 0, W, H)
  g.imageSmoothingQuality = 'high'
  // Source: the frame at the scale of the recording (2×), the view of the camera in CSS px.
  const k = im.width / W
  const vw = W / c.z
  const vh = H / c.z
  g.drawImage(im, (c.x - vw / 2) * k, (c.y - vh / 2) * k, vw * k, vh * k, 0, 0, W, H)
  g.restore()
  drawCursor(s, ts, c)
}

/** The cursor: it glides to each click of the shot, presses, a ring spreads. */
function drawCursor(s, ts, c) {
  const clicks = M.scenes[s.scene].events.filter((e) => e.kind === 'click' && e.t > s.from - 0.6 && e.t < s.to + 0.6)
  if (!clicks.length) return
  // In the time of the recording: 0.6 s to glide to the click, the ring for 0.45 s after.
  const target = clicks.find((e) => e.t >= ts - 0.45) ?? clicks[clicks.length - 1]
  const before = clicks[clicks.indexOf(target) - 1]
  const from = before ?? { x: target.x + 160, y: target.y + 120 }
  // The first cursor of a shot shows up when it starts to glide.
  if (!before && ts < target.t - 0.6) return
  const glide = clamp((ts - (target.t - 0.6)) / 0.6, 0, 1)
  const x = from.x + (target.x - from.x) * ease(glide)
  const y = from.y + (target.y - from.y) * ease(glide)
  const sx = (x - c.x) * c.z + W / 2
  const sy = (y - c.y) * c.z + H / 2
  const since = ts - target.t
  if (since >= 0 && since < 0.45) {
    const r = 14 + since * 120
    g.strokeStyle = `rgba(120,170,255,${1 - since / 0.45})`
    g.lineWidth = 4
    g.beginPath()
    g.arc(sx, sy, r, 0, Math.PI * 2)
    g.stroke()
  }
  const press = since >= -0.05 && since < 0.12 ? 0.85 : 1
  g.save()
  g.translate(sx, sy)
  g.scale(1.5 * press, 1.5 * press)
  g.beginPath()
  g.moveTo(0, 0)
  g.lineTo(0, 22)
  g.lineTo(5.5, 17)
  g.lineTo(9.5, 26)
  g.lineTo(13, 24.5)
  g.lineTo(9, 15.5)
  g.lineTo(16, 15.5)
  g.closePath()
  g.fillStyle = '#fff'
  g.strokeStyle = '#000'
  g.lineWidth = 1.4
  g.shadowColor = 'rgba(0,0,0,.45)'
  g.shadowBlur = 6
  g.fill()
  g.shadowBlur = 0
  g.stroke()
  g.restore()
}

/** The caption of the step: a band at the bottom, it slides in and out. */
function drawCaption(text, local, dur, keep) {
  if (!text) return
  const inK = keep?.in ? 1 : ease(clamp(local / 0.35, 0, 1))
  const outK = keep?.out ? 1 : ease(clamp((dur - local) / 0.3, 0, 1))
  const k = Math.min(inK, outK)
  g.save()
  g.globalAlpha = k
  g.font = font(30, 'JBMono')
  const w = g.measureText(text).width + 72
  const x = (W - w) / 2
  const y = H - 112 + (1 - k) * 24
  g.fillStyle = 'rgba(8,10,14,.86)'
  g.beginPath()
  g.roundRect(x, y, w, 66, 33)
  g.fill()
  g.strokeStyle = 'rgba(255,255,255,.12)'
  g.lineWidth = 1.5
  g.stroke()
  g.fillStyle = '#fff'
  g.textBaseline = 'middle'
  g.fillText(text, x + 36, y + 34)
  g.restore()
}

function center(text, y, size, f, color = '#fff', spacing = 0) {
  g.font = font(size, f)
  g.fillStyle = color
  g.textBaseline = 'middle'
  if ('letterSpacing' in g) g.letterSpacing = spacing + 'px'
  g.fillText(text, (W - g.measureText(text).width) / 2, y)
  if ('letterSpacing' in g) g.letterSpacing = '0px'
}

function drawLater(local, dur) {
  g.fillStyle = '#000'
  g.fillRect(0, 0, W, H)
  const k = Math.min(ease(clamp(local / 0.3, 0, 1)), ease(clamp((dur - local) / 0.3, 0, 1)))
  g.globalAlpha = k
  const dots = '.'.repeat(1 + Math.min(2, Math.floor(local / 0.25)))
  g.font = font(120)
  const w = g.measureText('Later...').width
  g.fillStyle = '#fff'
  g.textBaseline = 'middle'
  g.fillText('Later' + dots, (W - w) / 2, H / 2)
  g.globalAlpha = 1
}

async function drawApp(local, dur) {
  // The app, in a window that comes forward over the IDE.
  const ide = await img(frameAt('dev', 2.67))
  g.drawImage(ide, 0, 0, W, H)
  g.fillStyle = 'rgba(0,0,0,.55)'
  g.fillRect(0, 0, W, H)
  const app = await img('../out/site/app.jpg')
  const k = ease(clamp(local / 0.5, 0, 1))
  const s = 0.62 + 0.14 * k + 0.03 * (local / dur)
  const w = W * s
  const h = H * s
  const x = (W - w) / 2
  const y = (H - h) / 2 - 30 + (1 - k) * 80
  g.save()
  g.globalAlpha = k
  g.shadowColor = 'rgba(0,0,0,.6)'
  g.shadowBlur = 60
  g.fillStyle = '#e9e9ee'
  g.beginPath()
  g.roundRect(x, y - 44, w, h + 44, 14)
  g.fill()
  g.shadowBlur = 0
  g.save()
  g.beginPath()
  g.roundRect(x, y - 44, w, h + 44, 14)
  g.clip()
  g.drawImage(app, x, y, w, h)
  g.restore()
  for (const [i, col] of ['#ff5f57', '#febc2e', '#28c840'].entries()) {
    g.fillStyle = col
    g.beginPath()
    g.arc(x + 26 + i * 24, y - 22, 7, 0, Math.PI * 2)
    g.fill()
  }
  g.fillStyle = '#fff'
  g.beginPath()
  g.roundRect(x + w * 0.25, y - 34, w * 0.5, 24, 12)
  g.fill()
  g.font = font(15, 'JBMono')
  g.fillStyle = '#555'
  g.textBaseline = 'middle'
  const url = 'https://astra.your-tailnet.ts.net:8401'
  g.fillText(url, x + w / 2 - g.measureText(url).width / 2, y - 21)
  g.restore()
}

function drawEnd(local) {
  g.fillStyle = '#000'
  g.fillRect(0, 0, W, H)
  const a = (d) => ease(clamp((local - d) / 0.5, 0, 1))
  g.globalAlpha = a(0)
  center('WEB IDE', 330, 190, 'Anton', '#fff', 6)
  g.globalAlpha = a(0.5)
  center('You are the bottleneck now.', 520, 64, 'Anton')
  g.globalAlpha = a(1.1)
  center('self-hosted · local AI · open source', 650, 30, 'JBMono', '#9aa3b2')
  g.globalAlpha = a(1.5)
  center('github.com/DrSmithFr/web-ide', 760, 34, 'JBMono', '#7aa7ff')
  g.globalAlpha = 1
}

window.load = (manifest) => (M = manifest)

window.renderAt = async (t) => {
  let i = SHOTS.findIndex((s) => t < s.start + s.dur)
  if (i < 0) i = SHOTS.length - 1
  const s = SHOTS[i]
  const local = t - s.start
  const draw = async (shot, l) => {
    if (shot.later) return drawLater(l, shot.dur)
    if (shot.app) return drawApp(l, shot.dur)
    if (shot.end) return drawEnd(l)
    return drawScene(shot, clamp(l / shot.dur, 0, 1))
  }
  await draw(s, local)
  // A cross-fade from the last image of the previous shot.
  const FADE = 0.4
  if (s.fade && i > 0 && local < FADE) {
    const prev = g.getImageData(0, 0, W, H)
    await draw(SHOTS[i - 1], SHOTS[i - 1].dur)
    const old = g.getImageData(0, 0, W, H)
    g.putImageData(prev, 0, 0)
    const off = new OffscreenCanvas(W, H)
    off.getContext('2d').putImageData(old, 0, 0)
    g.globalAlpha = 1 - ease(local / FADE)
    g.drawImage(off, 0, 0)
    g.globalAlpha = 1
  }
  // The caption runs on while the next shot keeps it.
  const same = (a, b) => a && b && a.caption && a.caption === b.caption
  drawCaption(s.caption, same(SHOTS[i - 1], s) ? local + 1 : local, same(s, SHOTS[i + 1]) ? s.dur + 1 : s.dur, { in: same(SHOTS[i - 1], s), out: same(s, SHOTS[i + 1]) })
}
