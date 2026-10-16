// Desks of the project of the window (docs/desks.md): each desk is a session of its own (its
// layout, tabs and tools), as the virtual desktops of a desktop environment. The window shows
// one; the list and what the desks share (the Recent Files) come with project.open and are
// saved by desks.update. The desk shown is also remembered per device: /project/:id opens it.
import { createSignal } from 'solid-js'
import { createStore, produce, reconcile, unwrap } from 'solid-js/store'
import { notify, on, request } from '../pod/rpc'

export interface Desk {
  n: number
  name?: string
  mode: 'full'
}

export interface DeskList {
  desks: Desk[]
  recent: string[]
}

const emptyList = (): DeskList => ({ desks: [{ n: 1, mode: 'full' }], recent: [] })

const [desk, setDesk] = createSignal(1)
const [desks, setDesks] = createStore<DeskList>(emptyList())
export { desk, desks }

/** Project whose desks are loaded (events of another project are ignored). */
let owner = ''

/** Sets the desks of the project opened in the window. */
export function loadDesks(project: string, list: DeskList | null | undefined, shown: number) {
  owner = project
  setDesks(reconcile(list?.desks?.length ? { desks: list.desks, recent: list.recent ?? [] } : emptyList()))
  showDesk(shown)
}

/** Records the desk the window shows. */
export function showDesk(n: number) {
  setDesk(n)
  try {
    localStorage.setItem(`desk:${owner}`, String(n))
  } catch {
    /* no storage: the window opens desk 1 */
  }
}

/** The desk last shown on this device for a project (1 when none). */
export function lastDesk(project: string): number {
  try {
    return Number(localStorage.getItem(`desk:${project}`)) || 1
  } catch {
    return 1
  }
}

export function closeDesks() {
  owner = ''
  setDesks(reconcile(emptyList()))
  setDesk(1)
}

export const deskLabel = (d: Desk) => d.name || String(d.n)

let pushTimer: number | undefined

function changeDesks(fn: (d: DeskList) => void, now = false) {
  setDesks(produce(fn))
  clearTimeout(pushTimer)
  pushTimer = undefined
  if (now) void request('desks.update', { desks: unwrap(desks) })
  else
    pushTimer = window.setTimeout(() => {
      pushTimer = undefined
      notify('desks.update', { desks: unwrap(desks) })
    }, 400)
}

// A reload or a closed window sends the change still waiting (a recent file).
window.addEventListener('pagehide', () => {
  if (pushTimer === undefined) return
  clearTimeout(pushTimer)
  pushTimer = undefined
  notify('desks.update', { desks: unwrap(desks) })
})

/** Adds a desk after the last one; answers its number. */
export async function addDesk(): Promise<number> {
  const n = Math.max(...desks.desks.map((d) => d.n)) + 1
  clearTimeout(pushTimer)
  pushTimer = undefined
  setDesks(produce((d) => d.desks.push({ n, mode: 'full' })))
  await request('desks.update', { desks: unwrap(desks) })
  return n
}

export function renameDesk(n: number, name: string) {
  changeDesks((d) => {
    const x = d.desks.find((x) => x.n === n)
    if (x) x.name = name.trim() || undefined
  }, true)
}

/** Removes a desk (not desk 1): its session goes with it, the files stay open elsewhere. */
export function removeDesk(n: number) {
  if (n === 1) return
  changeDesks((d) => {
    d.desks = d.desks.filter((x) => x.n !== n)
  }, true)
}

// ---------- recent files ----------

const maxRecent = 50

/** Puts a file at the top of the recent files. */
export function touchRecent(path: string) {
  if (desks.recent[0] === path) return
  changeDesks((d) => {
    d.recent = [path, ...d.recent.filter((p) => p !== path)].slice(0, maxRecent)
  })
}

export function forgetRecent(path: string) {
  changeDesks((d) => {
    d.recent = d.recent.filter((p) => p !== path)
  })
}

/** Desk list changed by another window; a window on a removed desk is told by `onDeskGone`. */
let gone: (() => void) | undefined
export function onDeskGone(f: () => void) {
  gone = f
}

on('desks.changed', (list: DeskList, from?: string) => {
  if (!owner || (from && from !== owner)) return
  setDesks(reconcile({ desks: list.desks, recent: list.recent ?? [] }))
  if (!desks.desks.some((d) => d.n === desk())) gone?.()
})
