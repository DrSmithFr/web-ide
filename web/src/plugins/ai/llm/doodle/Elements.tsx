// The elements of a doodle as SVG, in the colors of the theme: the canvas of the modal and
// the read-only pages of the board draw them the same way.
import { createMemo, createSignal, For } from 'solid-js'
import { FONT, LINE_HEIGHT, type Element } from './model'
import { baseline, primsOf, type Prim } from './render'

const [dark, setDark] = createSignal(document.documentElement.dataset.theme !== 'light')
new MutationObserver(() => setDark(document.documentElement.dataset.theme !== 'light')).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })

/** Is the theme dark (the colors of the elements follow it)? */
export const darkTheme = dark

export function ElementView(props: { el: Element; dark: boolean }) {
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
          return <path d={v.d} fill={v.fill ? v.color : 'none'} fill-opacity={v.fill} stroke={v.color} stroke-width={v.width} stroke-linecap="round" stroke-linejoin="round" stroke-opacity={v.opacity} />
        return <path d={v.d} fill={v.color} fill-opacity={v.opacity} />
      })()}
    </>
  )
}
