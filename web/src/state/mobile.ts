// Phone layout of the project window (width ≤ 720 px): one bar of three rows, and one view at
// a time, full screen: the editor or a tool. The view follows what the user opens: a tool
// shown (icon, shortcut, the assistant…) comes to the front, a file opened brings the editor
// back. The visible height follows the on-screen keyboard, the caret line staying in sight.
import { createEffect, createRoot, createSignal } from 'solid-js'
import { EditorView } from '../editor/view'

const query = matchMedia('(max-width: 720px)')
const [phone, setPhone] = createSignal(query.matches)
query.addEventListener('change', (e) => setPhone(e.matches))
export { phone }

/** 'editor', or the id of the tool shown full screen. */
export const [mobileView, setMobileView] = createSignal('editor')

// The visual viewport shrinks when the keyboard opens (iOS keeps the layout viewport): the
// page takes its height, and the line of the caret is scrolled back into view.
const vv = window.visualViewport
if (vv) {
  let timer: ReturnType<typeof setTimeout> | undefined
  const fit = () => {
    if (!phone()) {
      document.documentElement.style.removeProperty('--app-h')
      return
    }
    document.documentElement.style.setProperty('--app-h', `${Math.round(vv.height)}px`)
    // iOS scrolls the whole page to show the focused field: the page stays at the top.
    if (vv.offsetTop) window.scrollTo(0, 0)
    clearTimeout(timer)
    timer = setTimeout(revealCaret, 60)
  }
  vv.addEventListener('resize', fit)
  vv.addEventListener('scroll', fit)
  query.addEventListener('change', fit)
  fit()
}

/** Scrolls the line of the caret of the active editor into view. */
export function revealCaret() {
  if (mobileView() !== 'editor') return
  const line = document.querySelector<HTMLElement>('.pane.active .ed-curline')
  if (line && document.activeElement?.closest('.pane.active')) line.scrollIntoView({ block: 'nearest' })
}

// No zoom of the page: pinch and double tap are refused, except on the drawing surfaces
// (doodles, the board), which zoom their own content.
document.addEventListener(
  'touchmove',
  (e) => {
    if (e.touches.length > 1 && !(e.target as HTMLElement).closest?.('[data-pinch]')) e.preventDefault()
  },
  { passive: false },
)
for (const name of ['gesturestart', 'gesturechange']) document.addEventListener(name, (e) => !(e.target as HTMLElement).closest?.('[data-pinch]') && e.preventDefault())

// Editor on a phone: locked (read only) so that a touch neither moves a caret nor opens
// the keyboard; scrolling and selecting (long press) still work. A tap shows a hint, a
// double tap unlocks it with the caret under the finger and the keyboard open; the padlock
// of the file view, or another view or file shown, locks it again.
export const [unlocked, setUnlocked] = createSignal(false)
/** Time of the last tap on a locked editor: the hint "Double-tap to edit" shows a moment. */
export const [hintAt, setHintAt] = createSignal(0)

createRoot(() => createEffect(() => EditorView.lockAll(phone() && !unlocked())))

let last = { at: 0, x: 0, y: 0 }
document.addEventListener(
  'touchend',
  (e) => {
    if (!phone() || unlocked() || e.changedTouches.length !== 1) return
    const ed = (e.target as HTMLElement).closest?.('.ed-content')
    const view = ed && EditorView.of(ed)
    if (!view || view.doc.readOnly) return
    const t = e.changedTouches[0]
    const now = Date.now()
    if (now - last.at < 400 && Math.hypot(t.clientX - last.x, t.clientY - last.y) < 30) {
      // Unlocked and focused during the gesture: iOS opens the keyboard.
      e.preventDefault()
      last = { at: 0, x: 0, y: 0 }
      const off = view.offsetAt(t.clientX, t.clientY)
      EditorView.lockAll(false)
      setUnlocked(true)
      if (off != null) view.setSelection(off, off, false)
      view.focus()
      setTimeout(revealCaret, 400)
      return
    }
    last = { at: now, x: t.clientX, y: t.clientY }
    setHintAt(now)
  },
  { passive: false },
)

/** Locks the editors again (the padlock, another view): the keyboard closes. */
export function lockEditor() {
  if (!unlocked()) return
  setUnlocked(false)
  const el = document.activeElement as HTMLElement | null
  if (el?.closest('.ed-content')) el.blur()
}

/** Gives the focus to the editor (locked on a phone: no keyboard). */
export function focusEditorQuietly() {
  document.querySelector<HTMLElement>('.pane.active .ed-content')?.focus({ preventScroll: true })
}

// Fields that open the keyboard: on a phone they take the focus only when the user touches
// them, never by themselves (a tool or a conversation shown, a search field focused on open):
// the keyboard opens when the user means to type.
const TYPING = 'textarea, input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit]):not([type=range]):not([type=color]):not([type=file])'
export const isTyping = (el: Element | null) => !!el?.matches?.(TYPING)

let down: { target: Node | null; at: number } = { target: null, at: 0 }
document.addEventListener('pointerdown', (e) => (down = { target: e.target as Node, at: Date.now() }), true)
document.addEventListener(
  'focusin',
  (e) => {
    const el = e.target as HTMLElement
    if (!phone() || !isTyping(el)) return
    const t = down.target as HTMLElement | null
    const touched = !!t && Date.now() - down.at < 1500 && (el === t || el.contains(t) || !!t.closest?.('label')?.contains(el) || !!el.closest('.xterm')?.contains(t))
    if (!touched) el.blur()
  },
  true,
)
