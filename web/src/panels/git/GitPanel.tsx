// Git tool: branch bar (switch, create, pull, push, fetch), then a Commit tab (changed
// files, commit form) and a History tab (graph of the current branch, commit detail).
// Pull, push and fetch run in a terminal (credentials prompts stay interactive).
import { onCleanup, onMount, Show } from 'solid-js'
import { request } from '../../pod/rpc'
import { mutate, session } from '../../state/project'
import { gitStatus, refreshGit } from '../../state/git'
import { pick, prompt } from '../../ui/overlay'
import { errorToast } from '../../ui/toast'
import { Icon } from '../../ui/icons'
import { t } from '../../i18n'
import { act, push, pushMenu, remote } from './actions'
import { ChangesTab } from './ChangesTab'
import { HistoryTab } from './HistoryTab'

export function GitPanel() {
  onMount(() => refreshGit(0))
  // Commands typed in a terminal do not produce file events everywhere: poll while visible.
  const poll = setInterval(() => document.visibilityState === 'visible' && refreshGit(0), 8000)
  onCleanup(() => clearInterval(poll))

  const st = () => gitStatus()
  const tab = () => session.git.tab
  const setTab = (id: 'commit' | 'history') => mutate((s) => (s.git.tab = id))

  const branches = async () => {
    try {
      const list: any[] = await request('git.branches')
      const choice = await pick<string>({
        placeholder: t('Switch branch'),
        items: [
          { label: t('+ New branch…'), value: '\0new' },
          ...list.filter((b) => !b.current).map((b) => ({ label: b.name, detail: b.remote ? t('remote') : b.upstream ? `→ ${b.upstream}` : '', value: b.name })),
        ],
      })
      if (!choice) return
      if (choice === '\0new') {
        const name = await prompt({ title: t('New branch'), label: t('Created from {branch}', { branch: st()?.branch ?? '' }) })
        if (name) await act('git.switch', { name: name.trim(), create: true }, t('Branch {name} created', { name }))
      } else await act('git.switch', { name: choice.replace(/^origin\//, ''), create: false }, t('On {branch}', { branch: choice }))
    } catch (e) {
      errorToast(e)
    }
  }

  return (
    <div class="panel git-panel">
      <div class="panel-head">
        <span class="panel-title">Git</span>
        <span class="grow" />
        <button class="icon-btn" title={t('Refresh')} onClick={() => refreshGit(0)}>
          <Icon name="refresh" />
        </button>
      </div>
      <Show
        when={st()?.repo}
        fallback={
          <div class="pad">
            <p class="muted">{st() ? t('The project is not in a Git repository.') : t('Loading…')}</p>
            <Show when={st()}>
              <button class="btn" onClick={() => act('git.init', {}, t('Repository initialized'))}>
                {t('Initialize a repository')}
              </button>
            </Show>
          </div>
        }
      >
        <div class="git-branch">
          <button class="btn small" title={t('Switch branch')} onClick={branches}>
            <Icon name="branch" size={12} /> {st()!.branch === '(detached)' ? t('detached HEAD') : st()!.branch}
          </button>
          <Show when={st()!.upstream}>
            <span class="muted small" title={t('Tracking: {branch}', { branch: st()!.upstream! })}>
              ↑{st()!.ahead} ↓{st()!.behind}
            </span>
          </Show>
          <span class="grow" />
          <button class="btn small" title={t('git pull (in a terminal)')} onClick={() => remote(['pull'])}>
            Pull
          </button>
          <span class="btn-group">
            <button class="btn small" title={t('git push (in a terminal)')} onClick={() => push()}>
              Push
            </button>
            <button class="btn small" title={t('Force the push')} onClick={(e) => pushMenu(e, push)}>
              <Icon name="chevron" size={11} />
            </button>
          </span>
          <button class="icon-btn" title={t('git fetch (in a terminal)')} onClick={() => remote(['fetch'])}>
            ⟳
          </button>
        </div>
        <div class="tabbar tool-tabbar" role="tablist">
          <div class="tab" role="tab" data-testid="git-tab-commit" aria-selected={tab() === 'commit'} classList={{ active: tab() === 'commit' }} onClick={() => setTab('commit')}>
            <span class="tab-title">Commit</span>
            <Show when={st()!.files.length}>
              <span class="badge">{st()!.files.length}</span>
            </Show>
          </div>
          <div class="tab" role="tab" data-testid="git-tab-history" aria-selected={tab() === 'history'} classList={{ active: tab() === 'history' }} onClick={() => setTab('history')}>
            <Icon name="history" size={13} />
            <span class="tab-title">{t('History')}</span>
          </div>
        </div>
        <Show when={tab() === 'history'} fallback={<ChangesTab />}>
          <HistoryTab />
        </Show>
      </Show>
    </div>
  )
}
