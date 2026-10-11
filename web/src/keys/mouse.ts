// Side buttons of the mouse (back, forward): they move in the history of the focused part of
// the project window instead of the history of the browser, which would leave the project.
// Elsewhere (home page, detached panels) they do nothing.

let handler: ((dir: -1 | 1) => void) | null = null

/** Sets what the side buttons do; the returned function removes it. */
export function onMouseNav(h: (dir: -1 | 1) => void): () => void {
  handler = h
  return () => {
    if (handler === h) handler = null
  }
}

const side = (e: MouseEvent) => e.button === 3 || e.button === 4

export function installMouseNav() {
  // The browser goes back on the release of the button unless it is cancelled.
  const cancel = (e: MouseEvent) => side(e) && e.preventDefault()
  window.addEventListener('mousedown', cancel, true)
  window.addEventListener('auxclick', cancel, true)
  window.addEventListener(
    'mouseup',
    (e) => {
      if (!side(e)) return
      e.preventDefault()
      handler?.(e.button === 3 ? -1 : 1)
    },
    true,
  )
}
