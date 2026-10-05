// Doodle modal: an infinite canvas on the left (pen, marker, eraser, shapes, text, selection,
// frame), the conversation on the right, so the user draws while reading and writing. Attach
// joins the doodle to the draft; sending from the composer of the modal attaches it first.
import { createEffect, createMemo, createSignal, For, on, onCleanup, onMount, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import { Icon } from '../../ui/icons'
import { errorToast } from '../../ui/toast'
import { chat, live } from '../state'
import { Thread } from '../Thread'
import { Composer, suggest } from '../Composer'
import { t } from '../../i18n'
import {
  addPoint,
  bounds,
  contains,
  erase,
  FONT,
  dividers,
  hit,
  updateZone,
  zoneAt,
  zoneAtPath,
  type Divider,
  type Layout,
  type Zone,
  isEmpty,
  LINE_HEIGHT,
  MARKER_SIZE,
  newId,
  PEN_SIZES,
  penColors,
  presets,
  pressureWidth,
  removeZone,
  rescale,
  TEXT_SIZES,
  textBox,
  translate,
  union,
  zones,
  type DoodleDoc,
  type Element,
  type Frame,
  type PenColor,
  type Preset,
  type Shape,
  type Stroke,
  type Text,
  type TextSize,
} from './model'
import { baseline, layered, primsOf, type Prim } from './render'
import { ZoneMenu } from './ZoneMenu'
import { createHistory } from './history'
import { captureScreen, pictureOf, type Picture } from './background'
import { loadTools, presetLabel, saveTools, Toolbar, toolKeys, type Tools } from './Toolbar'
import { closeDoodle, doodleSession, type DoodleSession } from './session'
import './doodle.css'

const ERASER_RADIUS = 10
const GRID = 20
const MIN_ZOOM = 0.1
const MAX_ZOOM = 8

type Gesture =
  | { kind: 'draw'; id: number; el: Stroke; pressure: boolean }
  | { kind: 'shape'; id: number; el: Shape }
  | { kind: 'erase'; id: number }
  | { kind: 'pan'; id: number; sx: number; sy: number; vx: number; vy: number }
  | { kind: 'pinch'; dist: number; mx: number; my: number; view: View }
  | { kind: 'frame'; id: number; handle: string; start: Frame; sx: number; sy: number }
  | { kind: 'move'; id: number; start: DoodleDoc; ids: Set<string>; box: Frame; sx: number; sy: number }
  | { kind: 'resize'; id: number; start: DoodleDoc; ids: Set<string>; box: Frame; handle: string; sx: number; sy: number }
  | { kind: 'band'; id: number; wx: number; wy: number; keep: string[] }
  | { kind: 'layout'; id: number; el: Layout; sx: number; sy: number }
  | { kind: 'divider'; id: number; start: DoodleDoc; el: Layout; div: Divider; sizes: number[] }

interface View {
  x: number
  y: number
  z: number
}

/** Text being typed: a new one, or an existing one (hidden meanwhile). */
interface Editing {
  id: string | null
  x: number
  y: number
  text: string
  color: PenColor
  size: TextSize
}

/** Elements copied, shared by the doodles of the page. */
let clipboard: Element[] = []

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
  const [selected, setSelected] = createSignal<string[]>([])
  const setTools = (p: Partial<Tools>) => {
    if (p.tool && p.tool !== 'select') setSelected([])
    if (p.tool && p.tool !== 'layout') setZoneSel(null)
    setToolsState({ ...tools(), ...p })
    saveTools(tools())
  }
  const [dark, setDark] = createSignal(document.documentElement.dataset.theme !== 'light')
  const [view, setView] = createSignal<View>({ x: 0, y: 0, z: 1 })
  const [size, setSize] = createSignal({ w: 0, h: 0 })
  const [drawing, setDrawing] = createSignal<Element | null>(null)
  const [band, setBand] = createSignal<Frame | null>(null)
  const [editing, setEditing] = createSignal<Editing | null>(null)
  // Zone of a layout picked with the layout tool, and the zone being named.
  const [zoneSel, setZoneSel] = createSignal<{ id: string; path: number[] } | null>(null)
  const [naming, setNaming] = createSignal<{ id: string; path: number[]; value: string } | null>(null)
  const [hoverDiv, setHoverDiv] = createSignal<'rows' | 'cols' | null>(null)
  const [cursor, setCursor] = createSignal<{ x: number; y: number } | null>(null)
  const [space, setSpace] = createSignal(false)
  const [busy, setBusy] = createSignal(false)
  let wrap!: HTMLDivElement
  let thread!: HTMLDivElement
  let bgInput!: HTMLInputElement
  let gesture: Gesture | null = null
  let pasted = 0
  const touchPts = new Map<number, { x: number; y: number }>()

  // Selection: only the ids still in the document (an undo can remove them).
  const selection = createMemo(() => {
    const ids = new Set(selected())
    return h.doc().elements.filter((el) => ids.has(el.id))
  })
  const selectionBox = createMemo(() => union(selection().map(bounds)))

  // ---------- view ----------

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

  const screen = (b: Frame) => {
    const v = view()
    return { left: (b.x - v.x) * v.z, top: (b.y - v.y) * v.z, width: b.w * v.z, height: b.h * v.z }
  }

  const zoomAt = (cx: number, cy: number, z: number) => {
    const r = wrap.getBoundingClientRect()
    const v = view()
    const nz = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z))
    const wx = (cx - r.left) / v.z + v.x
    const wy = (cy - r.top) / v.z + v.y
    setView({ z: nz, x: wx - (cx - r.left) / nz, y: wy - (cy - r.top) / nz })
  }

  const zoomCenter = (factor: number) => {
    const r = wrap.getBoundingClientRect()
    zoomAt(r.left + r.width / 2, r.top + r.height / 2, view().z * factor)
  }

  const snap = (v: number) => (tools().grid ? Math.round(v / GRID) * GRID : v)

  // A pointer the browser no longer tracks cannot be captured.
  const capture = (id: number) => {
    try {
      wrap.setPointerCapture(id)
    } catch {}
  }

  // ---------- document changes ----------

  const replaceSelected = (f: (el: Element) => Element) => {
    const ids = new Set(selected())
    const d = h.doc()
    h.apply({ ...d, elements: d.elements.map((el) => (ids.has(el.id) ? f(el) : el)) })
  }

  const deleteSelected = () => {
    const ids = new Set(selected())
    if (!ids.size) return
    const d = h.doc()
    h.apply({ ...d, elements: d.elements.filter((el) => !ids.has(el.id)) })
    setSelected([])
  }

  /** Adds copies of elements, moved by an offset, and selects them. */
  const insertCopies = (els: Element[], dx: number, dy: number) => {
    if (!els.length) return
    const copies = els.map((el) => ({ ...translate(el, dx, dy), id: newId() }))
    const d = h.doc()
    h.apply({ ...d, elements: [...d.elements, ...copies] })
    setToolsState({ ...tools(), tool: 'select' })
    setSelected(copies.map((c) => c.id))
  }

  const setColor = (c: PenColor) => {
    setTools({ penColor: c })
    if (tools().tool === 'select' && selection().length) replaceSelected((el) => (el.type === 'marker' ? el : ({ ...el, color: c } as Element)))
  }

  const setTextSize = (s: TextSize) => {
    setTools({ textSize: s })
    if (tools().tool === 'select' && selection().some((el) => el.type === 'text')) replaceSelected((el) => (el.type === 'text' ? { ...el, size: s } : el))
  }

  const eraseAt = (x: number, y: number) => {
    const d = h.doc()
    const r = ERASER_RADIUS / view().z
    const pixel = tools().eraser === 'pixel'
    let changed = false
    const elements = d.elements.flatMap((el) => {
      const rest = erase(el, x, y, r, pixel)
      if (!rest) return [el]
      changed = true
      return rest
    })
    if (changed) h.set({ ...d, elements })
  }

  /** Topmost element under a point. */
  const elementAt = (x: number, y: number) => {
    const list = layered(h.doc())
    const r = 4 / view().z
    for (let i = list.length - 1; i >= 0; i--) if (hit(list[i], x, y, r)) return list[i]
    return null
  }

  /** A divider of a layout under a point (dragged to change the proportions). */
  const dividerAt = (x: number, y: number) => {
    const tol = 5 / view().z
    const list = layered(h.doc())
    for (let i = list.length - 1; i >= 0; i--) {
      const el = list[i]
      if (el.type !== 'layout') continue
      for (const v of dividers(el)) {
        const on = v.dir === 'cols' ? Math.abs(x - v.x1) <= tol && y >= v.y1 && y <= v.y2 : Math.abs(y - v.y1) <= tol && x >= v.x1 && x <= v.x2
        if (on) return { el, div: v }
      }
    }
    return null
  }

  const layoutAt = (x: number, y: number) => {
    const list = layered(h.doc())
    for (let i = list.length - 1; i >= 0; i--) {
      const el = list[i]
      if (el.type === 'layout' && hit(el, x, y, 0)) return el
    }
    return null
  }

  const replaceLayout = (id: string, f: (el: Layout) => Layout) => {
    const d = h.doc()
    h.apply({ ...d, elements: d.elements.map((el) => (el.id === id && el.type === 'layout' ? f(el) : el)) })
  }

  const nameZone = (el: Layout, x: number, y: number) => {
    const z = zoneAt(el, x, y)
    if (z) setNaming({ id: el.id, path: z.path, value: z.zone.name ?? '' })
  }

  const commitName = (keep: boolean) => {
    const n = naming()
    if (!n) return
    setNaming(null)
    const name = n.value.trim() || undefined
    if (keep && name !== zoneAtPath(h.doc().elements.find((el) => el.id === n.id) as Layout, n.path)?.name) replaceLayout(n.id, (el) => updateZone(el, n.path, (z) => ({ ...z, name })))
    wrap.focus({ preventScroll: true })
  }

  // ---------- text ----------

  const editText = (el: Text | null, x: number, y: number) => {
    commitText()
    const tl = tools()
    setEditing(el ? { id: el.id, x: el.x, y: el.y, text: el.text, color: el.color, size: el.size } : { id: null, x, y, text: '', color: tl.penColor, size: tl.textSize })
  }

  const commitText = () => {
    const e = editing()
    if (!e) return
    setEditing(null)
    const d = h.doc()
    const text = e.text.replace(/\s+$/, '')
    if (e.id) {
      const old = d.elements.find((el) => el.id === e.id) as Text | undefined
      if (!old) return
      if (!text) h.apply({ ...d, elements: d.elements.filter((el) => el.id !== e.id) })
      else if (text !== old.text) h.apply({ ...d, elements: d.elements.map((el) => (el.id === e.id ? { ...old, text } : el)) })
    } else if (text) {
      h.apply({ ...d, elements: [...d.elements, { id: newId(), type: 'text', color: e.color, size: e.size, x: e.x, y: e.y, text }] })
    }
    wrap.focus({ preventScroll: true })
  }

  // ---------- pointer ----------

  const widthOf = (el: Stroke, pressure: boolean, p: number) =>
    el.type === 'marker' ? MARKER_SIZE : pressureWidth(PEN_SIZES[tools().penSize], pressure ? p : undefined)

  const onDown = (e: PointerEvent) => {
    if (gesture && gesture.kind !== 'pinch' && e.pointerType !== 'touch') return
    if (editing()) {
      commitText()
      if (tools().tool !== 'text') return
    }
    wrap.focus({ preventScroll: true })
    if (e.pointerType === 'touch') {
      touchPts.set(e.pointerId, { x: e.clientX, y: e.clientY })
      if (touchPts.size === 2) {
        // A second finger: what the first one started becomes a pinch.
        if (gesture?.kind === 'draw' || gesture?.kind === 'shape') setDrawing(null)
        if (gesture?.kind === 'band') setBand(null)
        if (gesture && ['erase', 'move', 'resize', 'frame'].includes(gesture.kind)) h.cancel()
        const [a, b] = [...touchPts.values()]
        gesture = { kind: 'pinch', dist: Math.hypot(a.x - b.x, a.y - b.y), mx: (a.x + b.x) / 2, my: (a.y + b.y) / 2, view: view() }
        return
      }
      if (touchPts.size > 2) return
    }
    const penEraser = e.pointerType === 'pen' && (e.buttons & 32) !== 0
    const tl = tools()
    const p = world(e.clientX, e.clientY)
    if (e.button === 1 || (e.button === 0 && space())) {
      e.preventDefault()
      const v = view()
      gesture = { kind: 'pan', id: e.pointerId, sx: e.clientX, sy: e.clientY, vx: v.x, vy: v.y }
    } else if (penEraser || (e.button === 0 && tl.tool === 'eraser')) {
      h.begin()
      gesture = { kind: 'erase', id: e.pointerId }
      eraseAt(p.x, p.y)
    } else if (e.button !== 0) return
    else if ((tl.tool === 'select' || tl.tool === 'layout') && dividerAt(p.x, p.y)) {
      const { el, div } = dividerAt(p.x, p.y)!
      h.begin()
      gesture = { kind: 'divider', id: e.pointerId, start: h.doc(), el, div, sizes: zoneAtPath(el, div.path)!.split!.sizes }
    } else if (tl.tool === 'layout') {
      const el = layoutAt(p.x, p.y)
      if (el) {
        setZoneSel({ id: el.id, path: zoneAt(el, p.x, p.y)?.path ?? [] })
        return
      }
      setZoneSel(null)
      const x = snap(p.x)
      const y = snap(p.y)
      gesture = { kind: 'layout', id: e.pointerId, sx: x, sy: y, el: { id: newId(), type: 'layout', color: tl.penColor, x, y, w: 0, h: 0, root: {} } }
      setDrawing(gesture.el)
    } else if (tl.tool === 'select') {
      const el = elementAt(p.x, p.y)
      if (!el) {
        gesture = { kind: 'band', id: e.pointerId, wx: p.x, wy: p.y, keep: e.shiftKey ? selected() : [] }
        if (!e.shiftKey) setSelected([])
      } else {
        const sel = selected()
        if (e.shiftKey) setSelected(sel.includes(el.id) ? sel.filter((x) => x !== el.id) : [...sel, el.id])
        else if (!sel.includes(el.id)) setSelected([el.id])
        if (!selected().includes(el.id)) return
        h.begin()
        gesture = { kind: 'move', id: e.pointerId, start: h.doc(), ids: new Set(selected()), box: selectionBox()!, sx: p.x, sy: p.y }
      }
    } else if (tl.tool === 'text') {
      e.preventDefault()
      const el = elementAt(p.x, p.y)
      editText(el?.type === 'text' ? el : null, snap(p.x), snap(p.y))
      return
    } else if (tl.tool === 'pen' || tl.tool === 'marker') {
      const marker = tl.tool === 'marker'
      const el: Stroke = { id: newId(), type: marker ? 'marker' : 'pen', color: marker ? tl.fluoColor : tl.penColor, pts: [] }
      const pressure = !marker && tl.pressure && e.pointerType === 'pen'
      gesture = { kind: 'draw', id: e.pointerId, el, pressure }
      addPoint(el.pts, p.x, p.y, widthOf(el, pressure, e.pressure), 0)
      setDrawing({ ...el })
    } else {
      const x = snap(p.x)
      const y = snap(p.y)
      const el: Shape = { id: newId(), type: tl.tool as Shape['type'], color: tl.penColor, width: PEN_SIZES[tl.penSize], x1: x, y1: y, x2: x, y2: y }
      gesture = { kind: 'shape', id: e.pointerId, el }
      setDrawing(el)
    }
    capture(e.pointerId)
  }

  const onMove = (e: PointerEvent) => {
    if (e.pointerType === 'touch' && touchPts.has(e.pointerId)) touchPts.set(e.pointerId, { x: e.clientX, y: e.clientY })
    const g = gesture
    if (tools().tool === 'eraser' || (e.pointerType === 'pen' && (e.buttons & 32) !== 0)) setCursor(world(e.clientX, e.clientY))
    else if (cursor()) setCursor(null)
    if (!g && (tools().tool === 'select' || tools().tool === 'layout')) {
      const q = world(e.clientX, e.clientY)
      setHoverDiv(dividerAt(q.x, q.y)?.div.dir ?? null)
    }
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
    if (g.id !== e.pointerId) return
    const coalesced = e.getCoalescedEvents?.() ?? []
    const events = coalesced.length ? coalesced : [e]
    const p = world(e.clientX, e.clientY)
    if (g.kind === 'draw') {
      for (const ev of events) {
        const q = world(ev.clientX, ev.clientY)
        addPoint(g.el.pts, q.x, q.y, widthOf(g.el, g.pressure, ev.pressure), 1 / view().z)
      }
      setDrawing({ ...g.el })
    } else if (g.kind === 'erase') {
      for (const ev of events) {
        const q = world(ev.clientX, ev.clientY)
        eraseAt(q.x, q.y)
      }
    } else if (g.kind === 'shape') {
      let x = snap(p.x)
      let y = snap(p.y)
      const dx = x - g.el.x1
      const dy = y - g.el.y1
      if (e.shiftKey) {
        if (g.el.type === 'rect' || g.el.type === 'ellipse') {
          // Square, circle.
          const s = Math.max(Math.abs(dx), Math.abs(dy))
          x = g.el.x1 + Math.sign(dx || 1) * s
          y = g.el.y1 + Math.sign(dy || 1) * s
        } else {
          // Steps of 45°.
          const a = Math.round(Math.atan2(dy, dx) / (Math.PI / 4)) * (Math.PI / 4)
          const l = Math.hypot(dx, dy)
          x = g.el.x1 + Math.cos(a) * l
          y = g.el.y1 + Math.sin(a) * l
        }
      }
      g.el = { ...g.el, x2: x, y2: y }
      setDrawing(g.el)
    } else if (g.kind === 'pan') {
      const z = view().z
      setView({ z, x: g.vx - (e.clientX - g.sx) / z, y: g.vy - (e.clientY - g.sy) / z })
    } else if (g.kind === 'frame') {
      const z = view().z
      h.set({ ...h.doc(), frame: resizeBox(g.start, g.handle, (e.clientX - g.sx) / z, (e.clientY - g.sy) / z, !['free', 'image'].includes(h.doc().preset), 40) })
    } else if (g.kind === 'move') {
      // The box of the selection lands on the grid when it is on.
      const dx = snap(g.box.x + p.x - g.sx) - g.box.x
      const dy = snap(g.box.y + p.y - g.sy) - g.box.y
      h.set({ ...g.start, elements: g.start.elements.map((el) => (g.ids.has(el.id) ? translate(el, dx, dy) : el)) })
    } else if (g.kind === 'resize') {
      const to = resizeBox(g.box, g.handle, p.x - g.sx, p.y - g.sy, e.shiftKey, 4)
      h.set({ ...g.start, elements: g.start.elements.map((el) => (g.ids.has(el.id) ? rescale(el, g.box, to) : el)) })
    } else if (g.kind === 'layout') {
      const x = snap(p.x)
      const y = snap(p.y)
      g.el = { ...g.el, x: Math.min(g.sx, x), y: Math.min(g.sy, y), w: Math.abs(x - g.sx), h: Math.abs(y - g.sy) }
      setDrawing(g.el)
    } else if (g.kind === 'divider') {
      // The divider between the children i and i + 1 moves within their two sizes.
      const { div, sizes } = g
      const pos = div.dir === 'cols' ? (snap(p.x) - div.box.x) / div.box.w : (snap(p.y) - div.box.y) / div.box.h
      const before = sizes.slice(0, div.i).reduce((a, b) => a + b, 0)
      const pair = sizes[div.i] + sizes[div.i + 1]
      const first = Math.min(pair - 0.05, Math.max(0.05, pos - before))
      const next = sizes.map((v, i) => (i === div.i ? first : i === div.i + 1 ? pair - first : v))
      const el = updateZone(g.el, div.path, (z) => ({ ...z, split: { ...z.split!, sizes: next } }))
      h.set({ ...g.start, elements: g.start.elements.map((x) => (x.id === el.id ? el : x)) })
    } else if (g.kind === 'band') {
      setBand({ x: Math.min(g.wx, p.x), y: Math.min(g.wy, p.y), w: Math.abs(p.x - g.wx), h: Math.abs(p.y - g.wy) })
    }
  }

  /** Box dragged by a corner handle (or moved whole); the opposite corner stays in place. */
  const resizeBox = (s: Frame, handle: string, dx: number, dy: number, keepRatio: boolean, min: number): Frame => {
    if (handle === 'move') return { ...s, x: snap(s.x + dx), y: snap(s.y + dy) }
    const ax = handle.includes('w') ? s.x + s.w : s.x
    const ay = handle.includes('n') ? s.y + s.h : s.y
    const px = snap((handle.includes('w') ? s.x : s.x + s.w) + dx)
    const py = snap((handle.includes('n') ? s.y : s.y + s.h) + dy)
    let w = Math.max(min, Math.abs(px - ax))
    let hh = Math.max(min, Math.abs(py - ay))
    if (keepRatio && s.w && s.h) {
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
    const cancel = e.type === 'pointercancel'
    if (g.kind === 'draw') {
      setDrawing(null)
      if (!cancel && g.el.pts.length) h.apply({ ...h.doc(), elements: [...h.doc().elements, g.el] })
    } else if (g.kind === 'shape') {
      setDrawing(null)
      // A click without drag draws nothing.
      if (!cancel && Math.hypot(g.el.x2 - g.el.x1, g.el.y2 - g.el.y1) * view().z >= 4) h.apply({ ...h.doc(), elements: [...h.doc().elements, g.el] })
    } else if (g.kind === 'layout') {
      setDrawing(null)
      if (!cancel && g.el.w * view().z >= 8 && g.el.h * view().z >= 8) {
        h.apply({ ...h.doc(), elements: [...h.doc().elements, g.el] })
        setZoneSel({ id: g.el.id, path: [] })
      }
    } else if (g.kind === 'band') {
      const b = band()
      setBand(null)
      const inside = b ? h.doc().elements.filter((el) => contains(b, bounds(el))).map((el) => el.id) : []
      setSelected([...new Set([...g.keep, ...inside])])
    } else if (g.kind !== 'pan') h.end()
  }

  const onDblClick = (e: MouseEvent) => {
    const tool = tools().tool
    if (tool !== 'select' && tool !== 'layout') return
    const p = world(e.clientX, e.clientY)
    const el = tool === 'layout' ? layoutAt(p.x, p.y) : elementAt(p.x, p.y)
    if (el?.type === 'text') editText(el, el.x, el.y)
    else if (el?.type === 'layout') nameZone(el, p.x, p.y)
  }

  const startFrame = (e: PointerEvent, handle: string) => {
    if (e.button !== 0) return
    e.stopPropagation()
    commitText()
    h.begin()
    gesture = { kind: 'frame', id: e.pointerId, handle, start: h.doc().frame, sx: e.clientX, sy: e.clientY }
    capture(e.pointerId)
  }

  const startResize = (e: PointerEvent, handle: string) => {
    if (e.button !== 0 || !selectionBox()) return
    e.stopPropagation()
    const p = world(e.clientX, e.clientY)
    h.begin()
    gesture = { kind: 'resize', id: e.pointerId, start: h.doc(), ids: new Set(selected()), box: selectionBox()!, handle, sx: p.x, sy: p.y }
    capture(e.pointerId)
  }

  // ---------- background ----------

  /** Puts an image under the drawing, at the corner of the frame, which takes its size. */
  const setBackground = (pic: Picture) => {
    const d = h.doc()
    const background = { src: pic.src, x: d.frame.x, y: d.frame.y, w: pic.w, h: pic.h }
    h.apply({ ...d, background, preset: 'image', frame: { x: background.x, y: background.y, w: pic.w, h: pic.h } })
    requestAnimationFrame(fit)
  }

  const addBackground = (file: Blob) => pictureOf(file).then(setBackground).catch(errorToast)

  const removeBackground = () => {
    const d = h.doc()
    h.apply({ ...d, background: undefined, preset: d.preset === 'image' ? 'free' : d.preset })
  }

  const screenshot = async () => {
    try {
      setBackground(await captureScreen())
    } catch (e) {
      // The user cancelled the choice of the screen.
      if ((e as Error).name !== 'NotAllowedError' && (e as Error).name !== 'AbortError') errorToast(e)
    }
  }

  /** Ctrl+V: an image becomes the background, else the copied elements are pasted. */
  const onPaste = (e: ClipboardEvent) => {
    if (isTyping(e.target) || document.querySelector('.dd-modal .ai-pop')) return
    const file = [...(e.clipboardData?.files ?? [])].find((f) => f.type.startsWith('image/'))
    if (file) {
      e.preventDefault()
      void addBackground(file)
    } else if (clipboard.length) {
      e.preventDefault()
      pasted++
      insertCopies(clipboard, 20 * pasted, 20 * pasted)
    }
  }

  const setPreset = (id: Preset) => {
    const d = h.doc()
    const p = presets.find((x) => x.id === id)!
    const f = d.frame
    const bg = d.background
    const frame = id === 'free' ? f : id === 'image' ? (bg ? { x: bg.x, y: bg.y, w: bg.w, h: bg.h } : f) : { x: f.x + f.w / 2 - p.w / 2, y: f.y + f.h / 2 - p.h / 2, w: p.w, h: p.h }
    h.apply({ ...d, preset: id, frame })
    requestAnimationFrame(fit)
  }

  // ---------- attach, close ----------

  const dirty = () => h.doc() !== initial && !(isEmpty(h.doc()) && isEmpty(initial))

  const attach = async () => {
    if (busy()) return
    commitText()
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
    commitText()
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
      if (zoneSel()) setZoneSel(null)
      else if (selected().length) setSelected([])
      else close()
      return
    }
    if (typing) return
    const k = e.key.toLowerCase()
    const sel = selection()
    let done = true
    if (mod && k === 'z' && !e.shiftKey) h.undo()
    else if (mod && (k === 'y' || (k === 'z' && e.shiftKey))) h.redo()
    else if (mod && k === 'a') {
      setToolsState({ ...tools(), tool: 'select' })
      setSelected(h.doc().elements.map((el) => el.id))
    } else if (mod && k === 'd') insertCopies(sel, 20, 20)
    else if (mod && (k === 'c' || k === 'x') && sel.length) {
      clipboard = structuredClone(sel)
      pasted = 0
      if (k === 'x') deleteSelected()
    } else if (mod || e.altKey) done = false
    else if ((e.key === 'Delete' || e.key === 'Backspace') && sel.length) deleteSelected()
    else if (e.key.startsWith('Arrow') && sel.length) {
      const step = tools().grid ? GRID : e.shiftKey ? 10 : 1
      const dx = e.key === 'ArrowLeft' ? -step : e.key === 'ArrowRight' ? step : 0
      const dy = e.key === 'ArrowUp' ? -step : e.key === 'ArrowDown' ? step : 0
      replaceSelected((el) => translate(el, dx, dy))
    } else if (toolKeys[k]) setTools({ tool: toolKeys[k] })
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
    window.addEventListener('paste', onPaste, true)
    wrap.focus({ preventScroll: true })
    onCleanup(() => {
      ro.disconnect()
      mo.disconnect()
      wrap.removeEventListener('wheel', wheel)
      window.removeEventListener('keydown', onKey, true)
      window.removeEventListener('keyup', onKeyUp, true)
      window.removeEventListener('paste', onPaste, true)
    })
  })

  // The conversation of the modal follows the answer.
  createEffect(on(() => [chat.messages.length, live.content, live.tool], () => requestAnimationFrame(() => thread && (thread.scrollTop = thread.scrollHeight))))

  // ---------- rendering ----------

  const transform = () => {
    const v = view()
    return `matrix(${v.z} 0 0 ${v.z} ${-v.x * v.z} ${-v.y * v.z})`
  }
  const frameBox = createMemo(() => screen(h.doc().frame))
  const gridPattern = () => {
    const v = view()
    return { s: GRID * v.z, ox: -v.x * v.z, oy: -v.y * v.z }
  }
  const shown = createMemo(() => {
    const id = editing()?.id
    return layered(h.doc()).filter((el) => el.id !== id)
  })
  const corners = ['nw', 'ne', 'sw', 'se']
  const cornerStyle = (b: { left: number; top: number; width: number; height: number }, c: string) => ({
    left: `${b.left + (c.includes('e') ? b.width : 0)}px`,
    top: `${b.top + (c.includes('s') ? b.height : 0)}px`,
  })

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
              <input
                ref={bgInput}
                type="file"
                accept="image/*"
                hidden
                data-testid="dd-bg-input"
                onChange={(e) => {
                  const f = e.currentTarget.files?.[0]
                  if (f) void addBackground(f)
                  e.currentTarget.value = ''
                }}
              />
              <Toolbar
                tools={tools()}
                setTools={setTools}
                dark={dark()}
                h={h}
                selection={selection().length > 0}
                onColor={setColor}
                onTextSize={setTextSize}
                onDelete={deleteSelected}
                onPreset={setPreset}
                onPickBackground={() => bgInput.click()}
                onRemoveBackground={removeBackground}
                onScreenshot={() => void screenshot()}
                zoom={view().z}
                onZoom={zoomCenter}
                onFit={fit}
              />
              <div
                class={`dd-canvas tool-${tools().tool}`}
                classList={{ panning: space(), 'col-resize': hoverDiv() === 'cols', 'row-resize': hoverDiv() === 'rows' }}
                ref={wrap}
                tabIndex={0}
                onPointerDown={onDown}
                onPointerMove={onMove}
                onPointerUp={onUp}
                onPointerCancel={onUp}
                onPointerLeave={() => setCursor(null)}
                onDblClick={onDblClick}
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
                    <Show when={h.doc().background}>{(bg) => <image class="dd-bg" href={bg().src} x={bg().x} y={bg().y} width={bg().w} height={bg().h} preserveAspectRatio="none" />}</Show>
                    <For each={shown()}>{(el) => <ElementView el={el} dark={dark()} />}</For>
                    <Show when={drawing()}>{(el) => <ElementView el={el()} dark={dark()} />}</Show>
                  </g>
                  <rect class="dd-frame" x={frameBox().left} y={frameBox().top} width={frameBox().width} height={frameBox().height} />
                  <For each={selection()}>
                    {(el) => {
                      const b = () => screen(bounds(el))
                      return <rect class="dd-sel" x={b().left - 3} y={b().top - 3} width={b().width + 6} height={b().height + 6} data-testid="dd-sel" />
                    }}
                  </For>
                  <Show when={band()}>{(b) => <rect class="dd-band" x={screen(b()).left} y={screen(b()).top} width={screen(b()).width} height={screen(b()).height} />}</Show>
                  <Show when={cursor()}>
                    {(c) => <circle class="dd-eraser-cursor" cx={(c().x - view().x) * view().z} cy={(c().y - view().y) * view().z} r={ERASER_RADIUS} />}
                  </Show>
                </svg>
                <div class="dd-frame-label" style={{ left: `${frameBox().left}px`, top: `${frameBox().top - 24}px` }} onPointerDown={(e) => startFrame(e, 'move')} title={t('Drag to move the frame')}>
                  {presetLabel(h.doc().preset)} · {Math.round(h.doc().frame.w)}×{Math.round(h.doc().frame.h)}
                </div>
                <For each={corners}>
                  {(c) => <div class={`dd-handle ${c}`} style={cornerStyle(frameBox(), c)} onPointerDown={(e) => startFrame(e, c)} data-testid={`dd-handle-${c}`} />}
                </For>
                <Show when={selection().length > 0 && !selection().every((el) => el.type === 'text') && selectionBox()}>
                  {(box) => (
                    <For each={corners}>
                      {(c) => <div class={`dd-handle sel ${c}`} style={cornerStyle(screen(box()), c)} onPointerDown={(e) => startResize(e, c)} data-testid={`dd-sel-${c}`} />}
                    </For>
                  )}
                </Show>
                <Show when={zoneSel()}>
                  {(zs) => {
                    const el = () => h.doc().elements.find((x) => x.id === zs().id) as Layout | undefined
                    const box = () => {
                      const l = el()
                      return l ? (zones(l).find((z) => z.path.join() === zs().path.join())?.box ?? null) : null
                    }
                    return (
                      <Show when={el() && box()}>
                        <div class="dd-zone-hl" style={{ left: `${screen(box()!).left}px`, top: `${screen(box()!).top}px`, width: `${screen(box()!).width}px`, height: `${screen(box()!).height}px` }} />
                        <ZoneMenu
                          at={screen(box()!)}
                          zone={zoneAtPath(el()!, zs().path)!}
                          root={zs().path.length === 0}
                          onChange={(f: (z: Zone) => Zone) => replaceLayout(zs().id, (l) => updateZone(l, zs().path, f))}
                          onName={() => setNaming({ id: zs().id, path: zs().path, value: zoneAtPath(el()!, zs().path)?.name ?? '' })}
                          onRemove={() => {
                            const path = zs().path
                            replaceLayout(zs().id, (l) => removeZone(l, path))
                            setZoneSel({ id: zs().id, path: path.slice(0, -1) })
                          }}
                          onDelete={() => {
                            const d = h.doc()
                            h.apply({ ...d, elements: d.elements.filter((x) => x.id !== zs().id) })
                            setZoneSel(null)
                          }}
                        />
                      </Show>
                    )
                  }}
                </Show>
                <Show when={naming()}>
                  {(n) => {
                    const box = () => {
                      const l = h.doc().elements.find((x) => x.id === n().id) as Layout | undefined
                      return l ? zones(l).find((z) => z.path.join() === n().path.join())?.box : undefined
                    }
                    return (
                      <Show when={box()}>
                        <input
                          class="dd-zone-name"
                          ref={(el) => queueMicrotask(() => (el.focus(), el.select()))}
                          value={n().value}
                          placeholder={t('Name of the zone')}
                          style={{ left: `${screen(box()!).left + screen(box()!).width / 2}px`, top: `${screen(box()!).top + screen(box()!).height / 2}px` }}
                          onInput={(e) => setNaming({ ...n(), value: e.currentTarget.value })}
                          onBlur={() => commitName(true)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter' || e.key === 'Escape') {
                              e.preventDefault()
                              e.stopPropagation()
                              commitName(e.key === 'Enter')
                            }
                          }}
                          data-testid="dd-zone-name"
                        />
                      </Show>
                    )
                  }}
                </Show>
                <Show when={editing()}>{(ed) => <TextEditor ed={ed()} view={view()} dark={dark()} onInput={(text) => setEditing({ ...ed(), text })} onDone={commitText} />}</Show>
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
                sendable={() => !isEmpty(h.doc()) || !!editing()?.text.trim()}
                beforeSend={async () => {
                  commitText()
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

/** Text typed on the canvas, at the place and size it will have. */
function TextEditor(props: { ed: Editing; view: View; dark: boolean; onInput: (t: string) => void; onDone: () => void }) {
  let el!: HTMLTextAreaElement
  onMount(() => {
    el.focus()
    el.setSelectionRange(el.value.length, el.value.length)
  })
  const fs = () => TEXT_SIZES[props.ed.size] * props.view.z
  // Grows with the text, measured like the canvas measures it.
  const box = () => textBox({ id: '', type: 'text', color: props.ed.color, size: props.ed.size, x: 0, y: 0, text: props.ed.text })
  return (
    <textarea
      ref={el}
      class="dd-text-edit"
      value={props.ed.text}
      rows={1}
      spellcheck={false}
      style={{
        left: `${(props.ed.x - props.view.x) * props.view.z}px`,
        // Half the line spacing above the first line, as the canvas draws it.
        top: `${(props.ed.y - props.view.y) * props.view.z - (fs() * (LINE_HEIGHT - 1)) / 2}px`,
        width: `${box().w * props.view.z + fs()}px`,
        height: `${box().h * props.view.z}px`,
        'font-size': `${fs()}px`,
        'line-height': String(LINE_HEIGHT),
        'font-family': FONT,
        color: penColors[props.ed.color][props.dark ? 'dark' : 'light'],
      }}
      onInput={(e) => props.onInput(e.currentTarget.value)}
      onBlur={() => props.onDone()}
      onKeyDown={(e) => {
        if (e.key === 'Escape' || (e.key === 'Enter' && (e.ctrlKey || e.metaKey))) {
          e.preventDefault()
          e.stopPropagation()
          props.onDone()
        }
      }}
      data-testid="dd-text-edit"
    />
  )
}

function ElementView(props: { el: Element; dark: boolean }) {
  const prims = createMemo(() => primsOf(props.el, props.dark))
  return <For each={prims()}>{(v) => <PrimView v={v} />}</For>
}

function PrimView(props: { v: Prim }) {
  return (
    <>
      {(() => {
        const v = props.v
        if (v.kind === 'text')
          return (
            <text x={v.x} y={v.y + baseline(v.size)} text-anchor={v.anchor} font-size={String(v.size)} font-family={FONT} fill={v.color} style={{ 'white-space': 'pre' }}>
              <For each={v.lines}>
                {(l, i) => (
                  <tspan x={v.x} dy={i() ? v.size * LINE_HEIGHT : 0}>
                    {l || ' '}
                  </tspan>
                )}
              </For>
            </text>
          )
        if (v.width !== undefined)
          return <path d={v.d} fill="none" stroke={v.color} stroke-width={v.width} stroke-linecap="round" stroke-linejoin="round" stroke-opacity={v.opacity} />
        return <path d={v.d} fill={v.color} fill-opacity={v.opacity} />
      })()}
    </>
  )
}
