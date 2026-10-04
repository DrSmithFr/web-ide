// Keyboard navigation inside the toolbars and the tab bars: each bar is one Tab stop, the
// arrows move between its buttons and tabs (up and down for a vertical bar), Home and End go
// to its ends, Enter or Space activates a tab and Delete closes it. The Tab stop is the item
// last focused, else the active one. Text fields of a bar keep their own Tab stop and arrows.

const groupSel = '[role=toolbar], [role=tablist], .toolbar, .panel-head, .tabbar'
const itemSel = 'button:not(:disabled), [role=tab]'

function items(g: Element): HTMLElement[] {
  return [...g.querySelectorAll<HTMLElement>(itemSel)].filter((el) => {
    if (el.closest(groupSel) !== g || el.offsetParent === null) return false
    // The close button of a tab belongs to its tab.
    const tab = el.parentElement?.closest('[role=tab]')
    return !tab || !g.contains(tab)
  })
}

const isActive = (el: HTMLElement) => el.getAttribute('aria-selected') === 'true' || el.getAttribute('aria-pressed') === 'true' || el.classList.contains('active')

// Item of each bar focused last.
const last = new WeakMap<Element, HTMLElement>()

function normalize(g: Element, current?: HTMLElement) {
  const list = items(g)
  if (!list.length) return
  if (current) last.set(g, current)
  const prev = last.get(g)
  const cur = (prev && list.includes(prev) ? prev : null) ?? list.find(isActive) ?? list[0]
  for (const el of list) el.tabIndex = el === cur ? 0 : -1
}

let pending = false
function normalizeAll() {
  if (pending) return
  pending = true
  requestAnimationFrame(() => {
    pending = false
    document.querySelectorAll(groupSel).forEach((g) => normalize(g))
  })
}

export function installRoving() {
  // New buttons and tabs are given their Tab stop; the editor content is left alone.
  new MutationObserver((records) => {
    if (records.some((r) => !(r.target as Element).closest?.('.ed, .xterm'))) normalizeAll()
  }).observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['disabled'] })
  normalizeAll()

  document.addEventListener('focusin', (e) => {
    const el = e.target as HTMLElement
    const g = el.closest?.(groupSel)
    if (g && items(g).includes(el)) normalize(g, el)
  })

  document.addEventListener('keydown', (e) => {
    if (e.defaultPrevented || e.ctrlKey || e.altKey || e.metaKey) return
    const el = e.target as HTMLElement
    const g = el.closest?.(groupSel)
    if (!g) return
    const list = items(g)
    const i = list.indexOf(el)
    if (i < 0) return
    const vertical = g.getAttribute('aria-orientation') === 'vertical'
    const next = vertical ? 'ArrowDown' : 'ArrowRight'
    const prev = vertical ? 'ArrowUp' : 'ArrowLeft'
    let to = -1
    if (e.key === next) to = (i + 1) % list.length
    else if (e.key === prev) to = (i - 1 + list.length) % list.length
    else if (e.key === 'Home') to = 0
    else if (e.key === 'End') to = list.length - 1
    else if ((e.key === 'Enter' || e.key === ' ') && el.getAttribute('role') === 'tab' && el.tagName !== 'BUTTON') el.click()
    else if (e.key === 'Delete' && el.getAttribute('role') === 'tab') el.querySelector<HTMLElement>('.tab-close')?.click()
    else return
    e.preventDefault()
    if (to >= 0) list[to].focus()
  })
}
