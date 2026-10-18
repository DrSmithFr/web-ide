// Layout of the roadmap (docs/design/kanban.md): one row per lineage not merged yet, its blocks in
// order along a sequence axis (no dates), each block as wide as the estimated size of its
// ticket. Pure: the view only draws it.
import type { Priority, Size, Summary } from './state'

/** Width of a block in units, by size; a ticket not estimated yet gets the default. */
export const sizeUnits: Record<Size, number> = { s: 1, m: 2, l: 3, xl: 5 }
export const DEFAULT_UNITS = 2

export type BlockState = 'ready' | 'blocked' | 'new' | 'active' | 'done'

export interface Block {
  ticket: Summary
  /** Start and width, in units. */
  x: number
  w: number
  state: BlockState
  /** No size estimated yet: default width. */
  estimated: boolean
}

export interface Row {
  root: Summary
  blocks: Block[]
  /** Index of its group: the lineages linked by dependencies, even indirectly. */
  group: number
}

export interface Arrow {
  /** From the end of the row of the dependency to the start of the dependent block. */
  from: { row: number; x: number }
  to: { row: number; x: number }
  /** Ids of the dependency and of the dependent ticket. */
  dep: number
  ticket: number
}

const closed = (s: Summary) => s.status === 'done' || s.status === 'abandoned'

export function blockState(s: Summary): BlockState {
  if (closed(s)) return 'done'
  if (s.status === 'in_progress' || s.status === 'review') return 'active'
  if (s.blockers?.length) return 'blocked'
  return s.status === 'new' ? 'new' : 'ready'
}

const prioOrder: Record<Priority, number> = { critical: 0, high: 1, normal: 2, low: 3 }
const stateOrder: Record<BlockState, number> = { ready: 0, active: 1, blocked: 2, new: 3, done: 4 }

/** Rows and arrows of the roadmap for the tickets of the board. */
export function layoutRoadmap(tickets: Summary[]): { rows: Row[]; arrows: Arrow[] } {
  const byId = new Map(tickets.map((s) => [s.id, s]))
  const children = (id: number) => tickets.filter((s) => s.parent === id).sort((a, b) => (a.pos ?? 0) - (b.pos ?? 0) || a.id - b.id)
  const rows: Row[] = []
  for (const root of tickets) {
    if (root.parent && byId.has(root.parent)) continue
    const kids = children(root.id)
    // A lineage stays until it is closed with all its steps.
    if (closed(root) && kids.every(closed)) continue
    const blocks = [root, ...kids].map((ticket) => ({ ticket, x: 0, w: ticket.size ? sizeUnits[ticket.size] : DEFAULT_UNITS, state: blockState(ticket), estimated: !!ticket.size }))
    rows.push({ root, blocks, group: 0 })
  }
  // Sequence axis: a block starts after the previous one of its row and after the end of the
  // lineages it waits for (no loop between lineages: a few passes settle it).
  const rowEnd = new Map<number, number>()
  const rowOfTicket = new Map<number, Row>()
  for (const r of rows) for (const b of r.blocks) rowOfTicket.set(b.ticket.id, r)
  const end = (r: Row) => r.blocks[r.blocks.length - 1].x + r.blocks[r.blocks.length - 1].w
  for (let pass = 0; pass <= rows.length; pass++) {
    let changed = false
    for (const r of rows) {
      let x = 0
      for (const b of r.blocks) {
        for (const dep of b.ticket.dependsOn ?? []) {
          const d = rowOfTicket.get(dep)
          if (d && d !== r) x = Math.max(x, rowEnd.get(d.root.id) ?? 0)
        }
        if (b.x !== x) changed = true
        b.x = x
        x += b.w
      }
      rowEnd.set(r.root.id, end(r))
    }
    if (!changed) break
  }
  // Groups: the lineages linked by a dependency stay together (union-find over the rows).
  const parentOf = new Map(rows.map((r) => [r, r]))
  const find = (r: Row): Row => (parentOf.get(r) === r ? r : find(parentOf.get(r)!))
  for (const r of rows)
    for (const b of r.blocks)
      for (const dep of b.ticket.dependsOn ?? []) {
        const d = rowOfTicket.get(dep)
        if (d) parentOf.set(find(d), find(r))
      }
  const groups = new Map<Row, Row[]>()
  for (const r of rows) groups.set(find(r), [...(groups.get(find(r)) ?? []), r])
  // Groups: something to start first, then the work in progress, then what waits; by priority.
  // In a group, a staircase: by start, so that the arrows go down and right.
  const rank = (r: Row) => Math.min(...r.blocks.map((b) => stateOrder[b.state]))
  const key = (g: Row[]) => [Math.min(...g.map(rank)), Math.min(...g.map((r) => prioOrder[r.root.priority])), Math.min(...g.map((r) => r.root.id))]
  const sorted = [...groups.values()].map((g) => ({ g, k: key(g) }))
  sorted.sort((a, b) => a.k[0] - b.k[0] || a.k[1] - b.k[1] || a.k[2] - b.k[2])
  rows.length = 0
  sorted.forEach(({ g }, i) => {
    g.sort((a, b) => a.blocks[0].x - b.blocks[0].x || rank(a) - rank(b) || a.root.id - b.root.id)
    for (const r of g) rows.push({ ...r, group: i })
  })
  const rowOf = new Map<number, number>()
  rows.forEach((r, i) => r.blocks.forEach((b) => rowOf.set(b.ticket.id, i)))
  const arrows: Arrow[] = []
  rows.forEach((r, i) => {
    for (const b of r.blocks)
      for (const dep of b.ticket.dependsOn ?? []) {
        const j = rowOf.get(dep)
        if (j === undefined) continue
        const last = rows[j].blocks[rows[j].blocks.length - 1]
        arrows.push({ from: { row: j, x: last.x + last.w }, to: { row: i, x: b.x }, dep, ticket: b.ticket.id })
      }
  })
  return { rows, arrows }
}
