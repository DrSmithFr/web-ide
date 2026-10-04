// Focused part of the project window: a tool zone or the editor. It is the part last clicked
// or typed in; menus, the palette and dialogs, outside the parts, leave it unchanged. The
// focused part shows its active tab and its rail icon in the accent color, the others in white.
import { createSignal } from 'solid-js'
import type { Zone } from './zones'

export type FocusPart = Zone | 'editor'

const [focusPart, setFocusPart] = createSignal<FocusPart>('editor')
export { focusPart, setFocusPart }

/** Pointer down or focus in the workbench: the part around the target gets the focus. */
export function trackFocus(e: Event) {
  const part = (e.target as HTMLElement).closest?.('[data-focus]')?.getAttribute('data-focus')
  if (part) setFocusPart(part as FocusPart)
}

/** Gives the keyboard to the editor of the active pane. */
export function focusEditor() {
  document.querySelector<HTMLElement>('.pane.active .ed-content')?.focus()
}

/**
 * Remembers the focused element and the selection of the page (a popup opening); the returned
 * function gives them back (the editor of the active pane when the element went away) and
 * returns the element focused.
 */
export function keepFocus(): () => HTMLElement | null {
  const el = document.activeElement as HTMLElement | null
  const sel = getSelection()
  const range = sel?.rangeCount ? sel.getRangeAt(0).cloneRange() : null
  return () => {
    const target = el?.isConnected && el !== document.body ? el : document.querySelector<HTMLElement>('.pane.active .ed-content')
    if (!target) return null
    target.focus({ preventScroll: true })
    // A content editable element puts its caret at the start when it gets the focus back.
    if (range && target.contains(range.startContainer)) {
      const s = getSelection()
      s?.removeAllRanges()
      s?.addRange(range)
    }
    return target
  }
}
