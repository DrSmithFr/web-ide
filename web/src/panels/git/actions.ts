// Git actions shared by the tabs of the Git tool: requests, diffs, remote commands, commit actions.
import { request } from '../../pod/rpc'
import { basename, openFile, openTab, relPath } from '../../state/project'
import { gitStatus, refreshGit, type GitFile } from '../../state/git'
import { newConsole } from '../../console/consoles'
import { contextMenu, prompt, type MenuItem } from '../../ui/overlay'
import { errorToast, toast } from '../../ui/toast'
import { t, tn } from '../../i18n'
import { copyText } from '../../ui/clipboard'

export async function act(method: string, params: object, ok?: string): Promise<boolean> {
  try {
    await request(method, params)
    if (ok) toast(ok, 'ok', undefined, 2000)
    return true
  } catch (e) {
    errorToast(e)
    return false
  } finally {
    refreshGit(50)
  }
}

/** Diff of a changed file against HEAD (a conflicted file opens in the editor). */
export function openChange(f: GitFile) {
  if (f.conflict) return openFile(f.path)
  openTab({ kind: 'diff', path: f.path, head: true, from: f.origPath, title: `${basename(f.path)} (${t('changes')})` })
}

export interface CommitFile {
  path: string
  origPath?: string
  status: string
}

/** Diff of a file in a commit, against the first parent. */
export function openCommitFile(hash: string, f: CommitFile) {
  openTab({ kind: 'diff', path: f.path, rev: hash, from: f.origPath, title: `${basename(f.path)} (${hash.slice(0, 7)})` })
}

export async function discard(list: GitFile[]) {
  const names = list.map((f) => relPath(f.path))
  const what = names.length > 3 ? tn(names.length, '{n} file', '{n} files') : names.join(', ')
  if (!confirm(`${t('Discard the changes of {files}?', { files: what })} ${list.some((f) => f.untracked) ? t('Untracked files will be deleted.') + ' ' : ''}${t('This cannot be undone.')}`)) return
  await act('git.discard', { paths: list.filter((f) => !f.untracked).map((f) => f.path), untracked: list.filter((f) => f.untracked).map((f) => f.path) })
}

/** Stages files; conflicted files still holding markers are confirmed first. */
export async function stage(list: GitFile[]) {
  const marked: string[] = []
  for (const f of list.filter((f) => f.conflict)) {
    const content: string = await request<any>('fs.read', { path: f.path }).then((r) => r.content ?? '', () => '')
    if (/^<{7}( |$)/m.test(content)) marked.push(relPath(f.path))
  }
  if (marked.length && !confirm(t('{files} still contain conflict markers. Mark them as resolved?', { files: marked.join(', ') }))) return
  await act('git.stage', { paths: list.map((f) => f.path) })
}

export const unstage = (list: GitFile[]) => act('git.unstage', { paths: list.map((f) => f.path) })

/** Runs pull, push or fetch in a terminal (credential prompts stay interactive). */
export function remote(args: string[]) {
  return newConsole({ kind: 'task', command: ['git', ...args], title: `git ${args.join(' ')}` }).then(() => setTimeout(() => refreshGit(0), 3000))
}

export type PushMode = '' | 'lease' | 'force'

/** Pushes the current branch; without upstream, to origin with tracking. */
export function push(mode: PushMode = '', confirmed = false) {
  if (mode === 'force' && !confirmed && !confirm(t('Force the push? Commits of the remote branch that are not here will be lost.'))) return
  const st = gitStatus()
  const args = ['push']
  if (mode === 'lease') args.push('--force-with-lease')
  if (mode === 'force') args.push('--force')
  if (st && !st.upstream && st.branch && st.branch !== '(detached)') args.push('-u', 'origin', st.branch)
  return remote(args)
}

/** Menu of the push variants, under the button clicked; then runs `run` with the chosen mode. */
export function pushMenu(ev: MouseEvent, run: (mode: PushMode) => void, commit = false) {
  const r = (ev.currentTarget as HTMLElement).getBoundingClientRect()
  contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.bottom + 2 }), [
    { label: commit ? t('Commit and push (--force-with-lease)') : t('Push (--force-with-lease)'), action: () => run('lease') },
    { label: commit ? t('Commit and push (--force)') : t('Push (--force)'), danger: true, action: () => run('force') },
  ])
}

export interface LogCommit {
  hash: string
  short: string
  parents: string[]
  author: string
  email: string
  when: number
  subject: string
  refs?: string
}

export function copyHash(hash: string) {
  copyText(hash).then((ok) => ok && toast(t('Hash copied'), 'ok', undefined, 1200))
}

export async function branchAt(c: LogCommit) {
  const name = await prompt({ title: t('New branch'), label: t('Created from {commit}', { commit: `${c.short} ${c.subject}` }) })
  if (name) await act('git.switch', { name: name.trim(), create: true, from: c.hash }, t('Branch {name} created', { name: name.trim() }))
}

export async function revert(c: LogCommit) {
  if (!confirm(t('Create a commit undoing {commit}?', { commit: `${c.short} ${c.subject}` }))) return
  await act('git.revert', { rev: c.hash }, t('Commit reverted'))
}

export async function reset(c: LogCommit, mode: 'soft' | 'mixed' | 'hard') {
  const what = `${c.short} ${c.subject}`
  const ask =
    mode === 'hard'
      ? t('Reset the branch to {commit} and drop every uncommitted change? This cannot be undone.', { commit: what })
      : t('Reset the branch to {commit}? The later changes stay in the files.', { commit: what })
  if (!confirm(ask)) return
  await act('git.reset', { rev: c.hash, mode }, t('Branch reset to {commit}', { commit: c.short }))
}

export function commitMenuItems(c: LogCommit): MenuItem[] {
  return [
    { label: t('Copy the hash'), action: () => copyHash(c.hash) },
    { label: t('New branch here…'), action: () => branchAt(c) },
    { separator: true, label: '' },
    { label: t('Revert…'), action: () => revert(c) },
    { label: t('Reset here (soft)…'), action: () => reset(c, 'soft') },
    { label: t('Reset here (mixed)…'), action: () => reset(c, 'mixed') },
    { label: t('Reset here (hard)…'), danger: true, action: () => reset(c, 'hard') },
  ]
}
