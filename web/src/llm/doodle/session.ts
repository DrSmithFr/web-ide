// The doodle being drawn: one at a time, shown by DoodleHost in the assistant, or in the
// project window when an agent asked for it (request_doodle of the MCP).
import { createSignal } from 'solid-js'
import type { DoodleDoc } from './model'

export interface DoodleSession {
  doc: DoodleDoc
  name: string
  /** Joins the doodle to the draft of the message (or sends it to the agent that asked). */
  onAttach: (doc: DoodleDoc) => Promise<void>
  /** Called when the doodle closes, attached or not. */
  onClose?: () => void
  /** Asked by an agent: shown by the host of the project window, not the assistant's. */
  external?: boolean
  /** Label of the attach button (Attach by default). */
  attachLabel?: string
}

const [session, setSession] = createSignal<DoodleSession | null>(null)

export const doodleSession = session

export function openDoodle(s: DoodleSession) {
  setSession(s)
}

export function closeDoodle() {
  const s = session()
  setSession(null)
  s?.onClose?.()
}
