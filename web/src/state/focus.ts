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
