// Git state of the open project, refreshed after file changes and Git actions.
import { createSignal } from 'solid-js'
import { on, request } from '../pod/rpc'
import { project } from './project'

export interface GitFile {
  path: string
  origPath?: string
  index: string
  work: string
  untracked?: boolean
  conflict?: boolean
}

export interface GitStatus {
  repo: boolean
  top?: string
  branch?: string
  upstream?: string
  ahead: number
  behind: number
  files: GitFile[]
}

const [status, setStatus] = createSignal<GitStatus | null>(null)
const [revision, setRevision] = createSignal(0)
export { status as gitStatus }
/** Bumped when HEAD or the index may have changed (diffs and markers are recomputed). */
export const gitRevision = revision

let timer: number | undefined
let running = false
let again = false

export function refreshGit(delay = 400) {
  clearTimeout(timer)
  timer = window.setTimeout(load, delay)
}

async function load() {
  if (!project()) return
  if (running) {
    again = true
    return
  }
  running = true
  try {
    setStatus(await request<GitStatus>('git.status'))
  } catch {
    setStatus(null)
  } finally {
    running = false
    if (again) {
      again = false
      refreshGit(100)
    }
  }
}

on('git.changed', () => {
  setRevision((r) => r + 1)
  refreshGit(50)
})
// A file written, created or removed anywhere in the project.
on('fs.dir', (e: { path: string }) => {
  if (e.path.includes('/.git')) setRevision((r) => r + 1)
  refreshGit()
})
on('fs.changed', () => refreshGit())
window.addEventListener('focus', () => {
  setRevision((r) => r + 1)
  refreshGit(100)
})

/** Status letter of a path in the working tree view (explorer colors). */
export function fileState(path: string): 'modified' | 'added' | 'untracked' | 'conflict' | 'deleted' | null {
  const f = status()?.files.find((x) => x.path === path)
  if (!f) return null
  if (f.conflict) return 'conflict'
  if (f.untracked) return 'untracked'
  if (f.work === 'D' || f.index === 'D') return 'deleted'
  if (f.index === 'A') return 'added'
  return 'modified'
}

/** True when a folder contains changes (explorer). */
export function dirChanged(dir: string): boolean {
  const prefix = dir + '/'
  return !!status()?.files.some((f) => f.path.startsWith(prefix))
}
