// Three-way merge (diff3), line by line, between the local buffer, the base (content at
// load) and the remote version pushed by the pod.
import { diff3Merge } from 'node-diff3'

export type Block =
  | { kind: 'ok'; lines: string[] }
  | { kind: 'conflict'; local: string[]; base: string[]; remote: string[] }

export interface MergeOutcome {
  clean: boolean
  text: string
  blocks: Block[]
}

const split = (t: string) => t.split('\n')

export function merge3(local: string, base: string, remote: string): MergeOutcome {
  if (local === base) return { clean: true, text: remote, blocks: [{ kind: 'ok', lines: split(remote) }] }
  if (remote === base || remote === local) return { clean: true, text: local, blocks: [{ kind: 'ok', lines: split(local) }] }
  const regions = diff3Merge<string>(split(local), split(base), split(remote), { excludeFalseConflicts: true })
  const blocks: Block[] = []
  for (const r of regions) {
    if (r.ok) {
      const last = blocks[blocks.length - 1]
      if (last?.kind === 'ok') last.lines.push(...r.ok)
      else blocks.push({ kind: 'ok', lines: [...r.ok] })
    } else if (r.conflict) {
      blocks.push({ kind: 'conflict', local: r.conflict.a, base: r.conflict.o, remote: r.conflict.b })
    }
  }
  const clean = blocks.every((b) => b.kind === 'ok')
  return { clean, text: clean ? blocks.flatMap((b) => (b as { lines: string[] }).lines).join('\n') : '', blocks }
}

/** Text form with markers, for export or as a fallback. */
export function withMarkers(blocks: Block[]): string {
  const out: string[] = []
  for (const b of blocks) {
    if (b.kind === 'ok') out.push(...b.lines)
    else out.push('<<<<<<< modification en cours', ...b.local, '=======', ...b.remote, '>>>>>>> nouvelle version')
  }
  return out.join('\n')
}
