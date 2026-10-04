// Reordering of tabs by drag and drop: a tab dropped on another one lands before it, or after
// it when the pointer is on its right half. Used by the file tabs and the console tabs.
import { createSignal } from 'solid-js'

/** Bar under the dragged tab and the index where it would land. */
const [dropAt, setDropAt] = createSignal<{ bar: string; index: number } | null>(null)
export { dropAt, setDropAt }

/** Index where a tab dropped on the tab `i` lands. */
export function dropIndex(e: DragEvent, i: number) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  return e.clientX > r.left + r.width / 2 ? i + 1 : i
}

/** Classes of the tab `i` of a bar of `count` tabs, for the drop marker. */
export function dropClasses(bar: string, i: number, count: number) {
  const d = dropAt()
  return { 'drop-before': d?.bar === bar && d.index === i, 'drop-after': d?.bar === bar && d.index === count && i === count - 1 }
}

/** List with the item at `from` moved before the item at `to` (index taken before the move). */
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  const out = list.slice()
  const [x] = out.splice(from, 1)
  out.splice(from < to ? to - 1 : to, 0, x)
  return out
}
