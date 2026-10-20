// Horizontal handle above a bottom area of the Git tool: dragging it up makes the area taller.
export function Resizer(props: { height: number; set: (h: number) => void; min?: number }) {
  const down = (e: PointerEvent) => {
    e.preventDefault()
    const start = e.clientY
    const h = props.height
    const box = (e.currentTarget as HTMLElement).parentElement!.getBoundingClientRect().height
    const move = (ev: PointerEvent) => props.set(Math.round(Math.min(box - 80, Math.max(props.min ?? 90, h + start - ev.clientY))))
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }
  return <div class="git-resizer" onPointerDown={down} />
}
