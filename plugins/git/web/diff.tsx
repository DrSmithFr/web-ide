// The git source of the diff view: the working tree against the index ("changes"), the index
// against HEAD ("staged"), the working tree against HEAD ("head"), a commit against its first
// parent ("rev", read only); Stage / Unstage in its toolbar.
import type { DiffSource, TabState } from '@ide/api'
import { api, errorToast, t } from './plugin'
import { gitRevision, gitStatus, refreshGit } from './state'

const show = (path: string, rev: string) => api.rpc<{ content: string; exists: boolean }>('git.show', { path, rev })

export const gitDiff: DiffSource = {
  id: 'git',
  live: (tab) => !tab.staged && !tab.rev,
  revision: gitRevision,
  label: (tab) =>
    tab.rev ? t('commit {hash} ↔ its parent', { hash: tab.rev.slice(0, 7) }) : tab.staged ? t('index ↔ HEAD') : tab.head ? t('working tree ↔ HEAD') : t('working tree ↔ index'),
  async load(tab, work) {
    const path = tab.path!
    const from = tab.from ?? path
    const { staged, head, rev } = tab
    const old = rev ? await show(from, rev + '^') : await show(staged || head ? from : path, staged || head ? 'HEAD' : '')
    let neu: string
    if (rev) neu = (await show(path, rev)).content
    else if (staged) neu = (await show(path, '')).content
    else if (work !== null) neu = work
    else {
      try {
        neu = (await api.files.read(path)).content ?? ''
      } catch {
        neu = ''
      }
    }
    return { old: old.content, neu }
  },
  Actions: (props: { tab: TabState }) => {
    const path = props.tab.path!
    const file = () => gitStatus()?.files.find((f) => f.path === path)
    const isStagedNow = () => !!file() && file()!.index !== '.' && !file()!.untracked
    // HEAD ↔ working tree: stage what is left, or unstage when everything is staged.
    const unstages = () => props.tab.staged || (props.tab.head && !!file() && file()!.work === '.')
    const toggle = async () => {
      try {
        await api.rpc(unstages() ? 'git.unstage' : 'git.stage', { paths: [path] })
      } catch (e) {
        errorToast(e)
      }
      refreshGit(0)
    }
    return (
      <>
        {!props.tab.rev && (
          <button class="btn small" onClick={toggle}>
            {unstages() ? t('Unstage') : isStagedNow() && !props.tab.head ? t('Stage again') : t('Stage')}
          </button>
        )}
      </>
    )
  },
}
