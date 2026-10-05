// Undo history of a doodle; a gesture (a drag, an erasing stroke) is one step.
import { batch, createSignal } from 'solid-js'
import type { DoodleDoc } from './model'

export type History = ReturnType<typeof createHistory>

export function createHistory(initial: DoodleDoc) {
  const [doc, setDoc] = createSignal(initial)
  const [undos, setUndos] = createSignal<DoodleDoc[]>([])
  const [redos, setRedos] = createSignal<DoodleDoc[]>([])
  let before: DoodleDoc | null = null
  return {
    doc,
    canUndo: () => undos().length > 0,
    canRedo: () => redos().length > 0,
    apply(d: DoodleDoc) {
      batch(() => {
        setUndos([...undos(), doc()])
        setRedos([])
        setDoc(d)
      })
    },
    begin() {
      before = doc()
    },
    /** Change inside a gesture, recorded when it ends. */
    set(d: DoodleDoc) {
      setDoc(d)
    },
    end() {
      if (before && before !== doc()) {
        setUndos([...undos(), before])
        setRedos([])
      }
      before = null
    },
    cancel() {
      if (before) setDoc(before)
      before = null
    },
    undo() {
      const u = undos()
      if (!u.length) return
      batch(() => {
        setRedos([...redos(), doc()])
        setDoc(u[u.length - 1])
        setUndos(u.slice(0, -1))
      })
    },
    redo() {
      const r = redos()
      if (!r.length) return
      batch(() => {
        setUndos([...undos(), doc()])
        setDoc(r[r.length - 1])
        setRedos(r.slice(0, -1))
      })
    },
  }
}
