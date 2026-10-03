// Full screen view of a Mermaid diagram: zoom (wheel, buttons), pan (drag), fit, export as
// SVG or PNG.
import { createSignal, onCleanup, onMount, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import { Icon } from '../ui/icons'
import { toast } from '../ui/toast'

const [diagram, setDiagram] = createSignal<{ svg: string; source: string } | null>(null)

export function openDiagram(svg: string, source: string) {
  setDiagram({ svg, source })
}

function download(name: string, blob: Blob) {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(a.href), 1000)
}

function Viewer(props: { svg: string; source: string; onClose: () => void }) {
  let stage!: HTMLDivElement
  let content!: HTMLDivElement
  const [view, setView] = createSignal({ x: 0, y: 0, k: 1 })
  let natural = { w: 800, h: 600 }

  const svgEl = () => content.querySelector('svg') as SVGSVGElement | null

  const fit = () => {
    const pad = 48
    const sw = stage.clientWidth - pad * 2
    const sh = stage.clientHeight - pad * 2
    // A small diagram is not blown up beyond 150 %.
    const k = Math.min(sw / natural.w, sh / natural.h, 1.5)
    setView({ k, x: (stage.clientWidth - natural.w * k) / 2, y: (stage.clientHeight - natural.h * k) / 2 })
  }

  /** Zoom by factor f around the point (px, py) of the stage. */
  const zoom = (f: number, px = stage.clientWidth / 2, py = stage.clientHeight / 2) => {
    const v = view()
    const k = Math.min(Math.max(v.k * f, 0.05), 20)
    const r = k / v.k
    setView({ k, x: px - (px - v.x) * r, y: py - (py - v.y) * r })
  }

  onMount(() => {
    const svg = svgEl()
    if (svg) {
      // The drawn size comes from the viewBox; the SVG is then sized in pixels.
      const vb = svg.viewBox.baseVal
      natural = vb && vb.width ? { w: vb.width, h: vb.height } : { w: svg.clientWidth || 800, h: svg.clientHeight || 600 }
      svg.removeAttribute('style')
      svg.setAttribute('width', String(natural.w))
      svg.setAttribute('height', String(natural.h))
    }
    fit()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') props.onClose()
      else if (e.key === '+' || e.key === '=') zoom(1.25)
      else if (e.key === '-') zoom(0.8)
      else if (e.key === '0') fit()
    }
    window.addEventListener('keydown', onKey)
    onCleanup(() => window.removeEventListener('keydown', onKey))
  })

  const onWheel = (e: WheelEvent) => {
    e.preventDefault()
    const r = stage.getBoundingClientRect()
    zoom(Math.exp(-e.deltaY * 0.0015), e.clientX - r.left, e.clientY - r.top)
  }

  const onDown = (e: PointerEvent) => {
    if (e.button !== 0) return
    const start = { x: e.clientX, y: e.clientY, v: view() }
    stage.setPointerCapture(e.pointerId)
    stage.classList.add('grabbing')
    const move = (ev: PointerEvent) => setView({ ...start.v, x: start.v.x + ev.clientX - start.x, y: start.v.y + ev.clientY - start.y })
    const up = () => {
      stage.classList.remove('grabbing')
      stage.removeEventListener('pointermove', move)
      stage.removeEventListener('pointerup', up)
    }
    stage.addEventListener('pointermove', move)
    stage.addEventListener('pointerup', up)
  }

  const svgText = () => {
    const svg = svgEl()!.cloneNode(true) as SVGSVGElement
    svg.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
    return new XMLSerializer().serializeToString(svg)
  }

  const exportPng = async () => {
    try {
      const scale = 2
      const img = new Image()
      img.src = URL.createObjectURL(new Blob([svgText()], { type: 'image/svg+xml' }))
      await img.decode()
      const c = document.createElement('canvas')
      c.width = Math.ceil(natural.w * scale)
      c.height = Math.ceil(natural.h * scale)
      const ctx = c.getContext('2d')!
      ctx.fillStyle = getComputedStyle(document.body).backgroundColor
      ctx.fillRect(0, 0, c.width, c.height)
      ctx.drawImage(img, 0, 0, c.width, c.height)
      URL.revokeObjectURL(img.src)
      c.toBlob((b) => b && download('diagramme.png', b), 'image/png')
    } catch (e) {
      toast(`Export PNG impossible : ${(e as Error).message}`, 'error')
    }
  }

  return (
    <div class="ai-diagram" role="dialog" aria-label="Diagramme" data-testid="diagram-viewer">
      <div class="ai-diagram-bar">
        <strong>Diagramme</strong>
        <span class="grow" />
        <button class="icon-btn" title="Dézoomer (-)" onClick={() => zoom(0.8)}>
          −
        </button>
        <span class="ai-diagram-zoom">{Math.round(view().k * 100)} %</span>
        <button class="icon-btn" title="Zoomer (+)" onClick={() => zoom(1.25)}>
          +
        </button>
        <button class="btn small" title="Ajuster à la fenêtre (0)" onClick={fit}>
          Ajuster
        </button>
        <button class="btn small" title="Taille réelle" onClick={() => zoom(1 / view().k)}>
          100 %
        </button>
        <span class="sep" />
        <button class="btn small" onClick={() => download('diagramme.svg', new Blob([svgText()], { type: 'image/svg+xml' }))}>
          SVG
        </button>
        <button class="btn small" onClick={exportPng}>
          PNG
        </button>
        <button class="btn small" title="Copier la source Mermaid" onClick={() => navigator.clipboard?.writeText(props.source).then(() => toast('Source copiée', 'ok'))}>
          Source
        </button>
        <button class="icon-btn" title="Fermer (Échap)" onClick={props.onClose}>
          <Icon name="close" size={15} />
        </button>
      </div>
      <div class="ai-diagram-stage" ref={stage} onWheel={onWheel} onPointerDown={onDown} onDblClick={fit}>
        <div class="ai-diagram-content" ref={content} style={{ transform: `translate(${view().x}px, ${view().y}px) scale(${view().k})` }} innerHTML={props.svg} />
      </div>
      <div class="ai-diagram-help">Molette : zoom · glisser : déplacer · double-clic : ajuster</div>
    </div>
  )
}

export function DiagramViewer() {
  return (
    <Show when={diagram()}>
      {(d) => (
        <Portal>
          <Viewer svg={d().svg} source={d().source} onClose={() => setDiagram(null)} />
        </Portal>
      )}
    </Show>
  )
}
