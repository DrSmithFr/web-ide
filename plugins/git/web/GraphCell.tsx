// One row of the commit graph: the lines crossing the row and the commit node.
import { For } from 'solid-js'
import type { GraphRow } from './graph'

const W = 12
const H = 22
const colors = ['var(--accent)', 'var(--ok)', 'var(--warn)', '#c678dd', 'var(--info)', 'var(--danger)', '#56b6c2', '#d19a66']
export const laneColor = (n: number) => colors[n % colors.length]
const x = (lane: number) => lane * W + W / 2 + 1

export function GraphCell(props: { row: GraphRow; lanes: number; merge: boolean }) {
  const path = (from: number, to: number) => {
    const x1 = x(from < 0 ? props.row.lane : from)
    const y1 = from < 0 ? H / 2 : 0
    const x2 = x(to < 0 ? props.row.lane : to)
    const y2 = to < 0 ? H / 2 : H
    if (x1 === x2) return `M${x1} ${y1}V${y2}`
    const ym = (y1 + y2) / 2
    return `M${x1} ${y1}C${x1} ${ym} ${x2} ${ym} ${x2} ${y2}`
  }
  return (
    <svg class="git-graph" width={props.lanes * W + 2} height={H} viewBox={`0 0 ${props.lanes * W + 2} ${H}`}>
      <For each={props.row.segments}>{(s) => <path d={path(s.from, s.to)} stroke={laneColor(s.color)} />}</For>
      <circle cx={x(props.row.lane)} cy={H / 2} r={props.merge ? 3 : 3.5} fill={props.merge ? 'var(--bg)' : laneColor(props.row.color)} stroke={laneColor(props.row.color)} />
    </svg>
  )
}
