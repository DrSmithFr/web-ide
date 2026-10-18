// Scroll mode of a desk (docs/design/desks.md), like niri or PaperWM: the columns of the tile tree
// side by side in a strip wider than the window, each column a stack of tiles. A column is
// as wide as one column in sight (1, 2 or 3 by window width) or wider; the focused column
// scrolls into view. A strip of marks under it shows the columns and the part in sight.
import { createEffect, createSignal, For, on, Show, type JSX } from 'solid-js'
import { columnsOf, firstLeaf, findLeaf, session, setActivePane, setColumnWidth, type LayoutNode } from '../state/project'
import { columnsInSight, columnWidth, focusPane } from '../state/tiles'
import { t } from '../i18n'

export function ScrollArea(props: { cell: (node: LayoutNode) => JSX.Element }) {
  let strip!: HTMLDivElement
  // Scrolled part, in widths of the view.
  const [left, setLeft] = createSignal(0)
  const widths = () => columnsOf().map((c) => columnWidth(c.width))
  const total = () => widths().reduce((a, b) => a + b, 0)

  // The column of the focused tile comes into view (a new column, the keyboard, a click).
  createEffect(
    on([() => session.activePane, () => columnsOf().length, widths, columnsInSight], () =>
      requestAnimationFrame(() => {
        const col = strip.querySelector(`.pane[data-pane="${session.activePane}"]`)?.closest<HTMLElement>('.scroll-col')
        if (!col) return
        const a = col.offsetLeft
        const b = a + col.offsetWidth
        const to = a < strip.scrollLeft ? a : b > strip.scrollLeft + strip.clientWidth ? b - strip.clientWidth : null
        if (to !== null) strip.scrollTo({ left: to, behavior: 'smooth' })
      }),
    ),
  )

  const go = (c: LayoutNode) => {
    const id = findLeaf(c, session.activePane) ? session.activePane : firstLeaf(c).id
    setActivePane(id)
    focusPane(id)
  }

  // The right edge of a column changes its width.
  const drag = (c: LayoutNode, e: PointerEvent) => {
    e.preventDefault()
    const col = (e.currentTarget as HTMLElement).parentElement!
    const start = e.clientX
    const from = col.offsetWidth
    const pane = firstLeaf(c).id
    const move = (ev: PointerEvent) => setColumnWidth(pane, Math.max(1 / columnsInSight(), Math.min(1, (from + ev.clientX - start) / strip.clientWidth)))
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  return (
    <div class="scroll-desk">
      <div class="scroll-strip" ref={strip} onScroll={() => setLeft(strip.scrollLeft / (strip.clientWidth || 1))} data-testid="scroll-strip">
        <For each={columnsOf()}>
          {(c) => (
            <div class="scroll-col" data-col={c.id} style={{ width: `${columnWidth(c.width) * 100}cqw` }}>
              {props.cell(c)}
              <div class="scroll-handle" title={t('Drag to change the width of the column')} onPointerDown={(e) => drag(c, e)} />
            </div>
          )}
        </For>
      </div>
      <Show when={total() > 1.001}>
        <div class="scroll-overview" data-testid="scroll-overview">
          <For each={columnsOf()}>
            {(c, i) => (
              <button
                class="scroll-mark"
                classList={{ active: !!findLeaf(c, session.activePane) }}
                style={{ width: `${(widths()[i()] / total()) * 100}%` }}
                title={t('Column {n}', { n: i() + 1 })}
                onClick={() => go(c)}
              />
            )}
          </For>
          <div class="scroll-sight" style={{ left: `${(left() / total()) * 100}%`, width: `${(1 / total()) * 100}%` }} />
        </div>
      </Show>
    </div>
  )
}
