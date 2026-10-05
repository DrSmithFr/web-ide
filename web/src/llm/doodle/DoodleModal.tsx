// Doodle modal: an infinite canvas on the left (pen, marker, eraser, frame), the conversation
// on the right, so the user draws while reading and writing. Attach joins the doodle to the
// draft; sending from the composer of the modal attaches it first.
import { batch, createEffect, createMemo, createSignal, For, on, onCleanup, onMount, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import { Icon } from '../../ui/icons'
import { errorToast } from '../../ui/toast'
import { chat, live } from '../state'
import { Thread } from '../Thread'
import { Composer, suggest } from '../Composer'
import { t } from '../../i18n'
import {
  addPoint,
  erasePoints,
  fluoColors,
  isEmpty,
  MARKER_SIZE,
  newId,
  PEN_SIZES,
  penColors,
  presets,
  pressureWidth,
  touches as touchesElement,
  type DoodleDoc,
  type FluoColor,
  type Frame,
  type PenColor,
  type Preset,
  type Stroke,
} from './model'
import { layered, shapeOf } from './render'
import { closeDoodle, doodleSession, type DoodleSession } from './session'
import './doodle.css'

type Tool = 'pen' | 'marker' | 'eraser'

interface Tools {
  tool: Tool
  penSize: keyof typeof PEN_SIZES
  penColor: PenColor
  fluoColor: FluoColor
  eraser: 'pixel' | 'object'
  pressure: boolean
  grid: boolean
}

const TOOLS_KEY = 'doodle.tools'
const defaults: Tools = { tool: 'pen', penSize: 'normal', penColor: 'ink', fluoColor: 'yellow', eraser: 'pixel', pressure: true, grid: false }

function loadTools(): Tools {
  try {
    return { ...defaults, ...JSON.parse(localStorage.getItem(TOOLS_KEY) ?? '{}') }
  } catch {
    return defaults
  }
}

/** Undo history of the document; a gesture (an erasing stroke, a frame drag) is one step. */
function createHistory(initial: DoodleDoc) {
  const [doc, setDoc] = createSignal(initial)
  const [undos, setUndos] = createSignal<DoodleDoc[]>([])
  const [redos, setRedos] = createSignal<DoodleDoc[]>([])
  let before: DoodleDoc | null = null
  return {
    doc,
    canUndo: () => undos().length > 0,
    canRedo: () => redos().length > 0,
    apply(d: DoodleDoc) {
      batch(() => {
        setUndos([...undos(), doc()])
        setRedos([])
        setDoc(d)
      })
    },
    begin() {
      before = doc()
    },
    /** Change inside a gesture, recorded when it ends. */
    set(d: DoodleDoc) {
      setDoc(d)
    },
    end() {
      if (before && before !== doc()) {
        setUndos([...undos(), before])
        setRedos([])
      }
      before = null
    },
    cancel() {
      if (before) setDoc(before)
      before = null
    },
    undo() {
      const u = undos()
      if (!u.length) return
      batch(() => {
        setRedos([...redos(), doc()])
        setDoc(u[u.length - 1])
        setUndos(u.slice(0, -1))
      })
    },
    redo() {
      const r = redos()
      if (!r.length) return
      batch(() => {
        setUndos([...undos(), doc()])
        setDoc(r[r.length - 1])
        setRedos(r.slice(0, -1))
      })
    },
  }
}

const ERASER_RADIUS = 10
const GRID = 20
const MIN_ZOOM = 0.1
const MAX_ZOOM = 8

type Gesture =
  | { kind: 'draw'; id: number; el: Stroke; pressure: boolean }
  | { kind: 'erase'; id: number }
  | { kind: 'pan'; id: number; sx: number; sy: number; vx: number; vy: number }
  | { kind: 'pinch'; dist: number; mx: number; my: number; view: View }
  | { kind: 'frame'; id: number; handle: string; start: Frame; sx: number; sy: number }

interface View {
  x: number
  y: number
  z: number
}

const colorLabel = (c: PenColor | FluoColor) =>
  ({ ink: t('Black'), red: t('Red'), blue: t('Blue'), green: t('Green'), yellow: t('Yellow'), lime: t('Green'), pink: t('Pink'), cyan: t('Cyan') })[c]

const isTyping = (el: EventTarget | null) => {
  const e = el as HTMLElement | null
  return !!e && (e.tagName === 'TEXTAREA' || e.tagName === 'INPUT' || e.tagName === 'SELECT' || e.isContentEditable)
}

export function DoodleHost(props: { onSettings: () => void }) {
  return <Show when={doodleSession()}>{(s) => <DoodleModal session={s()} onSettings={props.onSettings} />}</Show>
}

function DoodleModal(props: { session: DoodleSession; onSettings: () => void }) {
  const initial = props.session.doc
  const h = createHistory(initial)
  const [tools, setToolsState] = createSignal<Tools>(loadTools())
  const setTools = (p: Partial<Tools>) => {
    setToolsState({ ...tools(), ...p })
    try {
      localStorage.setItem(TOOLS_KEY, JSON.stringify(tools()))
    } catch {}
  }
  const [dark, setDark] = createSignal(document.documentElement.dataset.theme !== 'light')
  const [view, setView] = createSignal<View>({ x: 0, y: 0, z: 1 })
  const [size, setSize] = createSignal({ w: 0, h: 0 })
  const [drawing, setDrawing] = createSignal<Stroke | null>(null)
  const [cursor, setCursor] = createSignal<{ x: number; y: number } | null>(null)
  const [space, setSpace] = createSignal(false)
  const [busy, setBusy] = createSignal(false)
  let wrap!: HTMLDivElement
  let thread!: HTMLDivElement
  let gesture: Gesture | null = null
  const touchPts = new Map<number, { x: number; y: number }>()

  const fit = () => {
    const { w, h: sh } = size()
    const f = h.doc().frame
    if (!w || !sh) return
    const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Math.min((w - 80) / f.w, (sh - 80) / f.h, 2)))
    setView({ z, x: f.x + f.w / 2 - w / 2 / z, y: f.y + f.h / 2 - sh / 2 / z })
  }

  const world = (cx: number, cy: number) => {
    const r = wrap.getBoundingClientRect()
    const v = view()
    return { x: (cx - r.left) / v.z + v.x, y: (cy - r.top) / v.z + v.y }
  }

  const zoomAt = (cx: number, cy: number, z: number) => {
    const r = wrap.getBoundingClientRect()
    const v = view()
    const nz = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z))
    const wx = (cx - r.left) / v.z + v.x
    const wy = (cy - r.top) / v.z + v.y
    setView({ z: nz, x: wx - (cx - r.left) / nz, y: wy - (cy - r.top) / nz })
  }

  const eraseAt = (x: number, y: number) => {
    const d = h.doc()
    const r = ERASER_RADIUS / view().z
    let changed = false
    const elements =
      tools().eraser === 'object'
        ? d.elements.filter((el) => {
            const hit = touchesElement(el, x, y, r)
            if (hit) changed = true
            return !hit
          })
        : d.elements.flatMap((el) => {
            const parts = erasePoints(el, x, y, r)
            if (!parts) return [el]
            changed = true
            return parts
          })
    if (changed) h.set({ ...d, elements })
  }

  // A pointer the browser no longer tracks cannot be captured.
  const capture = (id: number) => {
    try {
      wrap.setPointerCapture(id)
    } catch {}
  }

  const snap = (v: number) => (tools().grid ? Math.round(v / GRID) * GRID : v)

  // ---------- pointer ----------

  const onDown = (e: PointerEvent) => {
    if (gesture?.kind === 'draw' || gesture?.kind === 'erase') {
      if (e.pointerType !== 'touch') return
    }
    wrap.focus({ preventScroll: true })
    if (e.pointerType === 'touch') {
      touchPts.set(e.pointerId, { x: e.clientX, y: e.clientY })
      if (touchPts.size === 2) {
        // A second finger: the stroke started by the first one becomes a pinch.
        if (gesture?.kind === 'draw') setDrawing(null)
        if (gesture?.kind === 'erase') h.cancel()
        const [a, b] = [...touchPts.values()]
        gesture = { kind: 'pinch', dist: Math.hypot(a.x - b.x, a.y - b.y), mx: (a.x + b.x) / 2, my: (a.y + b.y) / 2, view: view() }
        return
      }
      if (touchPts.size > 2) return
    }
    const penEraser = e.pointerType === 'pen' && (e.buttons & 32) !== 0
    if (e.button === 1 || (e.button === 0 && space())) {
      e.preventDefault()
      const v = view()
      gesture = { kind: 'pan', id: e.pointerId, sx: e.clientX, sy: e.clientY, vx: v.x, vy: v.y }
    } else if (penEraser || (e.button === 0 && tools().tool === 'eraser')) {
      h.begin()
      gesture = { kind: 'erase', id: e.pointerId }
      const p = world(e.clientX, e.clientY)
      eraseAt(p.x, p.y)
    } else if (e.button === 0) {
      const tl = tools()
      const marker = tl.tool === 'marker'
      const el: Stroke = { id: newId(), type: marker ? 'marker' : 'pen', color: marker ? tl.fluoColor : tl.penColor, pts: [] }
      const pressure = !marker && tl.pressure && e.pointerType === 'pen'
      gesture = { kind: 'draw', id: e.pointerId, el, pressure }
      const p = world(e.clientX, e.clientY)
      addPoint(el.pts, p.x, p.y, widthOf(el, pressure, e.pressure), 0)
      setDrawing({ ...el })
    } else return
    capture(e.pointerId)
  }

  const widthOf = (el: Stroke, pressure: boolean, p: number) =>
    el.type === 'marker' ? MARKER_SIZE : pressureWidth(PEN_SIZES[tools().penSize], pressure ? p : undefined)

  const onMove = (e: PointerEvent) => {
    if (e.pointerType === 'touch' && touchPts.has(e.pointerId)) touchPts.set(e.pointerId, { x: e.clientX, y: e.clientY })
    const g = gesture
    if (tools().tool === 'eraser' || (e.pointerType === 'pen' && (e.buttons & 32) !== 0)) setCursor(world(e.clientX, e.clientY))
    else if (cursor()) setCursor(null)
    if (!g) return
    if (g.kind === 'pinch') {
      if (touchPts.size < 2) return
      const [a, b] = [...touchPts.values()]
      const dist = Math.hypot(a.x - b.x, a.y - b.y)
      const mx = (a.x + b.x) / 2
      const my = (a.y + b.y) / 2
      const r = wrap.getBoundingClientRect()
      const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, (g.view.z * dist) / (g.dist || 1)))
      // The world point under the starting middle follows the fingers.
      const wx = (g.mx - r.left) / g.view.z + g.view.x
      const wy = (g.my - r.top) / g.view.z + g.view.y
      setView({ z, x: wx - (mx - r.left) / z, y: wy - (my - r.top) / z })
      return
    }
    if ('id' in g && g.id !== e.pointerId) return
    const events = e.getCoalescedEvents?.() ?? [e]
    if (g.kind === 'draw') {
      for (const ev of events.length ? events : [e]) {
        const p = world(ev.clientX, ev.clientY)
        addPoint(g.el.pts, p.x, p.y, widthOf(g.el, g.pressure, ev.pressure), 1 / view().z)
      }
      setDrawing({ ...g.el })
    } else if (g.kind === 'erase') {
      for (const ev of events.length ? events : [e]) {
        const p = world(ev.clientX, ev.clientY)
        eraseAt(p.x, p.y)
      }
    } else if (g.kind === 'pan') {
      const z = view().z
      setView({ z, x: g.vx - (e.clientX - g.sx) / z, y: g.vy - (e.clientY - g.sy) / z })
    } else if (g.kind === 'frame') {
      const z = view().z
      const dx = (e.clientX - g.sx) / z
      const dy = (e.clientY - g.sy) / z
      h.set({ ...h.doc(), frame: moveFrame(g.start, g.handle, dx, dy) })
    }
  }

  const moveFrame = (s: Frame, handle: string, dx: number, dy: number): Frame => {
    if (handle === 'move') return { ...s, x: snap(s.x + dx), y: snap(s.y + dy) }
    const preset = h.doc().preset
    // The corner opposite to the handle stays in place.
    const ax = handle.includes('w') ? s.x + s.w : s.x
    const ay = handle.includes('n') ? s.y + s.h : s.y
    const px = snap((handle.includes('w') ? s.x : s.x + s.w) + dx)
    const py = snap((handle.includes('n') ? s.y : s.y + s.h) + dy)
    let w = Math.max(40, Math.abs(px - ax))
    let hh = Math.max(40, Math.abs(py - ay))
    if (preset !== 'free') {
      const ratio = s.w / s.h
      w = Math.max(w, hh * ratio)
      hh = w / ratio
    }
    return { x: handle.includes('w') ? ax - w : ax, y: handle.includes('n') ? ay - hh : ay, w, h: hh }
  }

  const onUp = (e: PointerEvent) => {
    if (e.pointerType === 'touch') touchPts.delete(e.pointerId)
    const g = gesture
    if (!g) return
    if (g.kind === 'pinch') {
      if (touchPts.size < 2) gesture = null
      return
    }
    if (g.id !== e.pointerId) return
    gesture = null
    if (tools().tool !== 'eraser') setCursor(null)
    if (g.kind === 'draw') {
      setDrawing(null)
      if (e.type !== 'pointercancel' && g.el.pts.length) h.apply({ ...h.doc(), elements: [...h.doc().elements, g.el] })
    } else if (g.kind === 'erase' || g.kind === 'frame') h.end()
  }

  const startFrame = (e: PointerEvent, handle: string) => {
    if (e.button !== 0) return
    e.stopPropagation()
    h.begin()
    gesture = { kind: 'frame', id: e.pointerId, handle, start: h.doc().frame, sx: e.clientX, sy: e.clientY }
    capture(e.pointerId)
  }

  const setPreset = (id: Preset) => {
    const d = h.doc()
    const p = presets.find((x) => x.id === id)!
    const f = d.frame
    const frame = id === 'free' ? f : { x: f.x + f.w / 2 - p.w / 2, y: f.y + f.h / 2 - p.h / 2, w: p.w, h: p.h }
    h.apply({ ...d, preset: id, frame })
    requestAnimationFrame(fit)
  }

  // ---------- attach, close ----------

  const dirty = () => h.doc() !== initial && !(isEmpty(h.doc()) && isEmpty(initial))

  const attach = async () => {
    if (busy()) return
    setBusy(true)
    try {
      await props.session.onAttach(h.doc())
      closeDoodle()
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
    }
  }

  const close = () => {
    if (dirty() && !confirm(t('Discard this doodle?'))) return
    closeDoodle()
  }

  // ---------- keyboard ----------

  const onKey = (e: KeyboardEvent) => {
    if (e.defaultPrevented || document.querySelector('.dd-modal .ai-pop')) return
    const typing = isTyping(e.target)
    const mod = e.ctrlKey || e.metaKey
    if (e.key === 'Escape' && !typing) {
      e.preventDefault()
      e.stopPropagation()
      close()
      return
    }
    if (typing) return
    const k = e.key.toLowerCase()
    let done = true
    if (mod && k === 'z' && !e.shiftKey) h.undo()
    else if (mod && (k === 'y' || (k === 'z' && e.shiftKey))) h.redo()
    else if (mod || e.altKey) done = false
    else if (k === 'p') setTools({ tool: 'pen' })
    else if (k === 'm') setTools({ tool: 'marker' })
    else if (k === 'e') setTools({ tool: 'eraser' })
    else if (k === 'g') setTools({ grid: !tools().grid })
    else if (k === '0') fit()
    else if (e.key === ' ') {
      if (!e.repeat) setSpace(true)
    } else done = false
    if (done) {
      e.preventDefault()
      e.stopPropagation()
    }
  }
  const onKeyUp = (e: KeyboardEvent) => {
    if (e.key === ' ') setSpace(false)
  }

  onMount(() => {
    const ro = new ResizeObserver(() => {
      const first = !size().w
      setSize({ w: wrap.clientWidth, h: wrap.clientHeight })
      if (first) fit()
    })
    ro.observe(wrap)
    const mo = new MutationObserver(() => setDark(document.documentElement.dataset.theme !== 'light'))
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    const wheel = (e: WheelEvent) => {
      e.preventDefault()
      const unit = e.deltaMode === 1 ? 16 : 1
      if (e.ctrlKey || e.metaKey) zoomAt(e.clientX, e.clientY, view().z * Math.exp((-e.deltaY * unit) / 300))
      else {
        const v = view()
        setView({ ...v, x: v.x + (e.deltaX * unit) / v.z, y: v.y + (e.deltaY * unit) / v.z })
      }
    }
    wrap.addEventListener('wheel', wheel, { passive: false })
    window.addEventListener('keydown', onKey, true)
    window.addEventListener('keyup', onKeyUp, true)
    wrap.focus({ preventScroll: true })
    onCleanup(() => {
      ro.disconnect()
      mo.disconnect()
      wrap.removeEventListener('wheel', wheel)
      window.removeEventListener('keydown', onKey, true)
      window.removeEventListener('keyup', onKeyUp, true)
    })
  })

  // The conversation of the modal follows the answer.
  createEffect(on(() => [chat.messages.length, live.content, live.tool], () => requestAnimationFrame(() => thread && (thread.scrollTop = thread.scrollHeight))))

  // ---------- rendering ----------

  const transform = () => {
    const v = view()
    return `matrix(${v.z} 0 0 ${v.z} ${-v.x * v.z} ${-v.y * v.z})`
  }
  const frameBox = createMemo(() => {
    const f = h.doc().frame
    const v = view()
    return { left: (f.x - v.x) * v.z, top: (f.y - v.y) * v.z, width: f.w * v.z, height: f.h * v.z }
  })
  const gridPattern = () => {
    const v = view()
    const s = GRID * v.z
    return { s, ox: -v.x * v.z, oy: -v.y * v.z }
  }
  const presetLabel = (id: Preset) => (id === 'square' ? t('Square') : id === 'mobile' ? t('Mobile') : id === 'free' ? t('Free') : id)
  const toolButton = (id: Tool, icon: string, label: string, key: string) => (
    <button class="dd-btn" classList={{ on: tools().tool === id }} title={`${label} (${key})`} aria-pressed={tools().tool === id} onClick={() => setTools({ tool: id })} data-testid={`dd-${id}`}>
      <Icon name={icon} size={16} />
    </button>
  )

  return (
    <Portal>
      <div class="modal-backdrop dd-backdrop">
        <div class="dd-modal" role="dialog" aria-modal="true" aria-label={t('Doodle')} data-testid="doodle">
          <header class="dd-head">
            <h2>{props.session.name}</h2>
            <span class="muted small">{t('The frame is what the model sees.')}</span>
            <span class="grow" />
            <button class="btn primary small" disabled={busy()} onClick={() => void attach()} data-testid="dd-attach">
              <Icon name="paperclip" size={14} /> {t('Attach')}
            </button>
            <button class="icon-btn" title={t('Close (Esc)')} onClick={close} data-testid="dd-close">
              ✕
            </button>
          </header>
          <div class="dd-body">
            <div class="dd-canvas-col">
              <div class="dd-toolbar" role="toolbar" aria-label={t('Drawing tools')}>
                {toolButton('pen', 'pen', t('Pen'), 'P')}
                {toolButton('marker', 'marker', t('Marker'), 'M')}
                {toolButton('eraser', 'eraser', t('Eraser'), 'E')}
                <span class="sep" />
                <Show when={tools().tool === 'pen'}>
                  <For each={['thin', 'normal'] as const}>
                    {(s) => (
                      <button class="dd-btn dd-size" classList={{ on: tools().penSize === s }} title={s === 'thin' ? t('Thin pen') : t('Normal pen')} onClick={() => setTools({ penSize: s })}>
                        <span style={{ width: `${PEN_SIZES[s] * 2}px`, height: `${PEN_SIZES[s] * 2}px` }} />
                      </button>
                    )}
                  </For>
                  <span class="sep" />
                  <For each={Object.keys(penColors) as PenColor[]}>
                    {(c) => (
                      <button class="dd-swatch" classList={{ on: tools().penColor === c }} title={colorLabel(c)} style={{ background: penColors[c][dark() ? 'dark' : 'light'] }} onClick={() => setTools({ penColor: c })} data-testid={`dd-color-${c}`} />
                    )}
                  </For>
                  <span class="sep" />
                  <label class="dd-check small" title={t('The width follows the pressure of the stylus')}>
                    <input type="checkbox" checked={tools().pressure} onChange={(e) => setTools({ pressure: e.currentTarget.checked })} /> {t('Pressure')}
                  </label>
                </Show>
                <Show when={tools().tool === 'marker'}>
                  <For each={Object.keys(fluoColors) as FluoColor[]}>
                    {(c) => (
                      <button class="dd-swatch fluo" classList={{ on: tools().fluoColor === c }} title={colorLabel(c)} style={{ background: fluoColors[c][dark() ? 'dark' : 'light'] }} onClick={() => setTools({ fluoColor: c })} data-testid={`dd-fluo-${c}`} />
                    )}
                  </For>
                </Show>
                <Show when={tools().tool === 'eraser'}>
                  <div class="dd-seg" role="group">
                    <button classList={{ on: tools().eraser === 'pixel' }} title={t('Erases what it touches')} onClick={() => setTools({ eraser: 'pixel' })} data-testid="dd-eraser-pixel">
                      {t('Pixel')}
                    </button>
                    <button classList={{ on: tools().eraser === 'object' }} title={t('Removes a whole stroke')} onClick={() => setTools({ eraser: 'object' })} data-testid="dd-eraser-object">
                      {t('Object')}
                    </button>
                  </div>
                </Show>
                <span class="sep" />
                <button class="dd-btn" title={t('Undo (Ctrl+Z)')} disabled={!h.canUndo()} onClick={() => h.undo()} data-testid="dd-undo">
                  <Icon name="undo" size={16} />
                </button>
                <button class="dd-btn" title={t('Redo (Ctrl+Shift+Z)')} disabled={!h.canRedo()} onClick={() => h.redo()} data-testid="dd-redo">
                  <Icon name="redo" size={16} />
                </button>
                <span class="grow" />
                <button class="dd-btn" classList={{ on: tools().grid }} title={t('Magnetic grid (G)')} aria-pressed={tools().grid} onClick={() => setTools({ grid: !tools().grid })}>
                  <Icon name="grid" size={16} />
                </button>
                <select class="dd-select" title={t('Frame')} value={h.doc().preset} onChange={(e) => setPreset(e.currentTarget.value as Preset)} data-testid="dd-preset">
                  <For each={presets}>{(p) => <option value={p.id}>{presetLabel(p.id)}</option>}</For>
                </select>
                <div class="dd-zoom">
                  <button title={t('Zoom out')} onClick={() => zoomAt(wrap.getBoundingClientRect().left + size().w / 2, wrap.getBoundingClientRect().top + size().h / 2, view().z / 1.25)}>
                    −
                  </button>
                  <button title={t('Fit the frame (0)')} onClick={fit}>
                    {Math.round(view().z * 100)} %
                  </button>
                  <button title={t('Zoom in')} onClick={() => zoomAt(wrap.getBoundingClientRect().left + size().w / 2, wrap.getBoundingClientRect().top + size().h / 2, view().z * 1.25)}>
                    +
                  </button>
                </div>
              </div>
              <div
                class="dd-canvas"
                classList={{ panning: space(), eraser: tools().tool === 'eraser' }}
                ref={wrap}
                tabIndex={0}
                onPointerDown={onDown}
                onPointerMove={onMove}
                onPointerUp={onUp}
                onPointerCancel={onUp}
                onPointerLeave={() => setCursor(null)}
                onContextMenu={(e) => e.preventDefault()}
                data-testid="dd-canvas"
              >
                <svg class="dd-svg" width={size().w} height={size().h}>
                  <Show when={tools().grid}>
                    <defs>
                      <pattern id="dd-grid" width={gridPattern().s} height={gridPattern().s} x={gridPattern().ox} y={gridPattern().oy} patternUnits="userSpaceOnUse">
                        <circle cx="0.5" cy="0.5" r="1" class="dd-grid-dot" />
                      </pattern>
                    </defs>
                  </Show>
                  <rect class="dd-frame-bg" x={frameBox().left} y={frameBox().top} width={frameBox().width} height={frameBox().height} />
                  <Show when={tools().grid}>
                    <rect width="100%" height="100%" fill="url(#dd-grid)" />
                  </Show>
                  <g transform={transform()}>
                    <For each={layered(h.doc())}>{(el) => <ElementView el={el} dark={dark()} />}</For>
                    <Show when={drawing()}>{(el) => <ElementView el={el()} dark={dark()} />}</Show>
                  </g>
                  <rect class="dd-frame" x={frameBox().left} y={frameBox().top} width={frameBox().width} height={frameBox().height} />
                  <Show when={cursor()}>
                    {(c) => <circle class="dd-eraser-cursor" cx={(c().x - view().x) * view().z} cy={(c().y - view().y) * view().z} r={ERASER_RADIUS} />}
                  </Show>
                </svg>
                <div class="dd-frame-label" style={{ left: `${frameBox().left}px`, top: `${frameBox().top - 24}px` }} onPointerDown={(e) => startFrame(e, 'move')} title={t('Drag to move the frame')}>
                  {presetLabel(h.doc().preset)} · {Math.round(h.doc().frame.w)}×{Math.round(h.doc().frame.h)}
                </div>
                <For each={['nw', 'ne', 'sw', 'se']}>
                  {(c) => (
                    <div
                      class={`dd-handle ${c}`}
                      style={{ left: `${frameBox().left + (c.includes('e') ? frameBox().width : 0)}px`, top: `${frameBox().top + (c.includes('s') ? frameBox().height : 0)}px` }}
                      onPointerDown={(e) => startFrame(e, c)}
                      data-testid={`dd-handle-${c}`}
                    />
                  )}
                </For>
              </div>
            </div>
            <div class="dd-chat">
              <div class="ai-messages dd-thread" ref={thread}>
                <Thread onSuggest={suggest} onSettings={props.onSettings} />
              </div>
              <Composer
                onSettings={props.onSettings}
                onSent={() => {}}
                inDoodle
                sendable={() => !isEmpty(h.doc())}
                beforeSend={async () => {
                  if (!isEmpty(h.doc())) await attach()
                  else closeDoodle()
                }}
              />
            </div>
          </div>
        </div>
      </div>
    </Portal>
  )
}

function ElementView(props: { el: Stroke; dark: boolean }) {
  const s = createMemo(() => shapeOf(props.el, props.dark))
  return (
    <Show
      when={s().width !== undefined}
      fallback={<path d={s().d} fill={s().color} fill-opacity={s().opacity} />}
    >
      <path d={s().d} fill="none" stroke={s().color} stroke-width={s().width} stroke-linecap="round" stroke-linejoin="round" stroke-opacity={s().opacity} />
    </Show>
  )
}
