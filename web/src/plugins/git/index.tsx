// The Git plugin (pod/internal/plugins/git): the Git tool, the git source of the diff view, and
// the git service (the state of the repository) the explorer, the project bar and the editor use.
import type { PluginModule } from '../types'
import { bindApi } from './plugin'
import { dirChanged, dirState, fileState, gitRevision, gitStatus, refreshGit, watchGit } from './state'
import { GitPanel } from './GitPanel'
import { gitDiff } from './diff'

export const git: PluginModule = {
  activate(api) {
    bindApi(api)
    watchGit()
    api.provide('git', {
      status: gitStatus,
      revision: gitRevision,
      refresh: refreshGit,
      fileState,
      dirState,
      dirChanged,
      ignored: (paths) => api.rpc<string[]>('git.ignored', { paths }),
      branches: () => api.rpc('git.branches'),
      show: (path, rev) => api.rpc('git.show', { path, rev }),
    })
    return {
      tools: { git: { component: GitPanel } },
      diffSources: [gitDiff],
      fileDecorations: {
        ignored: (paths) => api.rpc<string[]>('git.ignored', { paths }),
        state: (path, dir) => (dir ? dirState(path) : fileState(path)),
      },
      opened: () => refreshGit(0),
    }
  },
}
