// The doodle being drawn: one at a time, shown by DoodleHost in the assistant.
import { createSignal } from 'solid-js'
import type { DoodleDoc } from './model'

export interface DoodleSession {
  doc: DoodleDoc
  name: string
  /** Joins the doodle to the draft of the message. */
  onAttach: (doc: DoodleDoc) => Promise<void>
}

const [session, setSession] = createSignal<DoodleSession | null>(null)

export const doodleSession = session

export function openDoodle(s: DoodleSession) {
  setSession(s)
}

export function closeDoodle() {
  setSession(null)
}
