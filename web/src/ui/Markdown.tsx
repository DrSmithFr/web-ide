import { createEffect, onCleanup } from 'solid-js'
import { onMarkdownClick, renderMarkdown, renderMermaid } from './markdown'
import './markdown.css'

/** Markdown rendered at most once per frame while it streams; diagrams drawn as they close. */
export function Markdown(props: { text: string; final: boolean }) {
  let el!: HTMLDivElement
  let frame = 0
  createEffect(() => {
    const text = props.text
    const final = props.final
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => {
      el.innerHTML = renderMarkdown(text)
      renderMermaid(el, final).catch(() => {})
    })
  })
  onCleanup(() => cancelAnimationFrame(frame))
  return <div class="md" ref={el} onClick={onMarkdownClick} />
}
