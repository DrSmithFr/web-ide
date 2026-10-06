// A page of the board, read-only: its frame and elements in the colors of the theme, with
// zoom (wheel, pinch) and pan (drag). Double click or fit() shows the whole frame again.
import { createEffect, createSignal, For, on, Show } from 'solid-js'
import type { DoodleDoc } from '../doodle/model'
import { layered } from '../doodle/render'
import { darkTheme, ElementView } from '../doodle/Elements'

interface Box {
  x: number
  y: number
  w: number
  h: number
}

export function DoodleView(props: { doc: DoodleDoc; ref?: (api: { fit: () => void }) => void }) {
  let svg!: SVGSVGElement
  const framed = (): Box => {
    const f = props.doc.frame
    const m = Math.max(f.w, f.h) * 0.04
    return { x: f.x - m, y: f.y - m, w: f.w + 2 * m, h: f.h + 2 * m }
  }
  const [box, setBox] = createSignal<Box>(framed())
  const fit = () => setBox(framed())
  createEffect(on(() => props.doc, fit, { defer: true }))
  props.ref?.({ fit })

  /** Scale of the view: units of the page per screen pixel (preserveAspectRatio meet). */
  const unit = () => {
    const r = svg.getBoundingClientRect()
    return Math.max(box().w / (r.width || 1), box().h / (r.height || 1))
  }
  /** Point of the page under a screen point. */
  const at = (cx: number, cy: number) => {
    const r = svg.getBoundingClientRect()
    const u = unit()
    const b = box()
    return { x: b.x + b.w / 2 + (cx - r.left - r.width / 2) * u, y: b.y + b.h / 2 + (cy - r.top - r.height / 2) * u }
  }
  const zoom = (k: number, cx: number, cy: number) => {
    const p = at(cx, cy)
    const b = box()
    const f = props.doc.frame
    // From a tenth of the frame to five times it.
    const w = Math.min(Math.max(b.w * k, f.w / 10), f.w * 5)
    const s = w / b.w
    setBox({ x: p.x - (p.x - b.x) * s, y: p.y - (p.y - b.y) * s, w, h: b.h * s })
  }
  const pointers = new Map<number, { x: number; y: number }>()
  let pinch = 0

  return (
    <svg
      ref={svg}
      class="bd-view"
      viewBox={`${box().x} ${box().y} ${box().w} ${box().h}`}
      data-testid="bd-view"
      onWheel={(e) => {
        e.preventDefault()
        zoom(Math.exp(e.deltaY * 0.0015), e.clientX, e.clientY)
      }}
      onPointerDown={(e) => {
        pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
        try {
          e.currentTarget.setPointerCapture(e.pointerId)
        } catch {
          /* synthetic event */
        }
      }}
      onPointerMove={(e) => {
        const prev = pointers.get(e.pointerId)
        if (!prev) return
        if (pointers.size === 2) {
          const [a, b] = [...pointers.values()]
          const d = Math.hypot(a.x - b.x, a.y - b.y)
          pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
          const [a2, b2] = [...pointers.values()]
          const d2 = Math.hypot(a2.x - b2.x, a2.y - b2.y)
          if (pinch && d && d2) zoom(d / d2, (a2.x + b2.x) / 2, (a2.y + b2.y) / 2)
          pinch = d2
          return
        }
        const u = unit()
        const b = box()
        setBox({ ...b, x: b.x - (e.clientX - prev.x) * u, y: b.y - (e.clientY - prev.y) * u })
        pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
      }}
      onPointerUp={(e) => {
        pointers.delete(e.pointerId)
        pinch = 0
      }}
      onPointerCancel={(e) => {
        pointers.delete(e.pointerId)
        pinch = 0
      }}
      onDblClick={fit}
    >
      <rect class="bd-frame" x={props.doc.frame.x} y={props.doc.frame.y} width={props.doc.frame.w} height={props.doc.frame.h} />
      <Show when={props.doc.background}>{(bg) => <image href={bg().src} x={bg().x} y={bg().y} width={bg().w} height={bg().h} preserveAspectRatio="none" />}</Show>
      <For each={layered(props.doc)}>{(el) => <ElementView el={el} dark={darkTheme()} />}</For>
    </svg>
  )
}
