// Lanes of the commit graph (as `git log --graph`), computed from the parents of commits
// listed in graph order. A lane waits for a commit; a free lane is reused.

export interface Segment {
  /** Lane at the top and at the bottom of the row; -1 is the commit node (middle of the row). */
  from: number
  to: number
  color: number
}

export interface GraphRow {
  lane: number
  color: number
  segments: Segment[]
  /** Number of lanes drawn on the row. */
  width: number
}

export function layoutGraph(commits: { hash: string; parents: string[] }[]): GraphRow[] {
  const lanes: (string | null)[] = []
  const colors: number[] = []
  let nextColor = 0
  const free = () => {
    const i = lanes.indexOf(null)
    return i < 0 ? lanes.length : i
  }
  return commits.map((c) => {
    let lane = lanes.indexOf(c.hash)
    // A branch tip (or the top of the list) starts a lane without a line above.
    if (lane < 0) {
      lane = free()
      colors[lane] = nextColor++
    }
    const segments: Segment[] = []
    // Lanes coming from above: into the node when they wait for this commit, straight down otherwise.
    lanes.forEach((h, j) => {
      if (h === null) return
      if (h === c.hash) {
        segments.push({ from: j, to: -1, color: colors[j] })
        lanes[j] = null
      } else segments.push({ from: j, to: j, color: colors[j] })
    })
    let width = Math.max(lanes.length, lane + 1)
    const color = colors[lane]
    if (c.parents.length) {
      lanes[lane] = c.parents[0]
      segments.push({ from: -1, to: lane, color })
    } else lanes[lane] = null
    for (const p of c.parents.slice(1)) {
      let j = lanes.indexOf(p)
      if (j < 0) {
        j = free()
        lanes[j] = p
        colors[j] = nextColor++
      }
      segments.push({ from: -1, to: j, color: colors[j] })
    }
    while (lanes.length && lanes[lanes.length - 1] === null) lanes.pop()
    width = Math.max(width, lanes.length)
    return { lane, color, segments, width }
  })
}
