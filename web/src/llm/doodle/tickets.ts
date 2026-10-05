// Doodles of the conversation joined to a ticket the model creates or updates, as PNG files.
import { request } from '../../pod/rpc'
import { chat } from '../state'
import { cloneDoc } from './model'
import { png } from './export'

/** Attaches the doodles not attached yet (by file name); returns how many were added. */
export async function attachDoodles(ticketId: number, existing: string[]): Promise<number> {
  const seen = new Map<string, number>()
  let added = 0
  for (const m of chat.messages) {
    for (const a of m.attachments ?? []) {
      if (a.kind !== 'doodle' || !a.doodle) continue
      // Doodle 1 of two messages: "Doodle 1.png", "Doodle 1 (2).png".
      const n = (seen.get(a.name) ?? 0) + 1
      seen.set(a.name, n)
      const name = `${a.name}${n > 1 ? ` (${n})` : ''}.png`
      if (existing.includes(name)) continue
      const url = await png(cloneDoc(a.doodle), 1600)
      await request('kanban.attachment.add', { id: ticketId, name, mime: 'image/png', data: url.slice(url.indexOf(',') + 1) })
      added++
    }
  }
  return added
}

export const doodlesNote = (n: number) => (n ? ` ${n} doodle${n > 1 ? 's' : ''} of the conversation attached to it as PNG files.` : '')
