import { createSignal } from 'solid-js'

export interface CursorInfo {
  line: number
  col: number
  sel: number
  lang: string
}

/** Position of the caret in the focused editor, shown in the menu bar. */
export const [cursorInfo, setCursorInfo] = createSignal<CursorInfo | null>(null)
