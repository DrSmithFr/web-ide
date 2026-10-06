// Phone layout of the project window (width ≤ 720 px): one bar of three rows, and one view at
// a time, full screen: the editor or a tool. The view follows what the user opens: a tool
// shown (icon, shortcut, the assistant…) comes to the front, a file opened brings the editor
// back. The visible height follows the on-screen keyboard, the caret line staying in sight.
import { createSignal } from 'solid-js'

const query = matchMedia('(max-width: 720px)')
const [phone, setPhone] = createSignal(query.matches)
query.addEventListener('change', (e) => setPhone(e.matches))
export { phone }

/** 'editor', or the id of the tool shown full screen. */
export const [mobileView, setMobileView] = createSignal('editor')

/** The on-screen keyboard is open (the visible area much shorter than the window). */
export const [keyboard, setKeyboard] = createSignal(false)

// The visual viewport shrinks when the keyboard opens, and iOS scrolls the whole page to show
// the focused field (it keeps the layout viewport): the page is fixed on the visible area
// (its height and its offset), and the line of the caret is scrolled back into view.
const vv = window.visualViewport
if (vv) {
  let timer: ReturnType<typeof setTimeout> | undefined
  const style = document.documentElement.style
  // Full height of the visible area for the current width (rotation resets it).
  let full = 0
  let width = 0
  const fit = () => {
    if (vv.width !== width) [full, width] = [0, vv.width]
    full = Math.max(full, vv.height)
    if (!phone()) {
      style.removeProperty('--app-h')
      style.removeProperty('--app-top')
      setKeyboard(false)
      return
    }
    style.setProperty('--app-h', `${Math.round(vv.height)}px`)
    style.setProperty('--app-top', `${Math.round(vv.offsetTop)}px`)
    setKeyboard(vv.height < full * 0.8)
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
