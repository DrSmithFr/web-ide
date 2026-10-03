// Git panel: branch, commit message, staged / unstaged / conflicted files, history.
// Pull, push and fetch run in a terminal (credentials prompts stay interactive).
import { createResource, createSignal, For, onCleanup, onMount, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { basename, openFile, openTab, relPath } from '../state/project'
import { gitRevision, gitStatus, refreshGit, type GitFile } from '../state/git'
import { newConsole } from '../console/consoles'
import { contextMenu, pick, prompt } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { fmtAgo, fmtDate, t, tn } from '../i18n'

const letters: Record<string, string> = { M: 'M', A: 'A', D: 'D', R: 'R', C: 'C', T: 'T', U: 'U', '?': 'U' }
const titles: Record<string, string> = { M: 'modified', A: 'added', D: 'deleted', R: 'renamed', C: 'copied', T: 'type changed', U: 'in conflict', '?': 'untracked' }

export function openDiff(path: string, staged: boolean) {
  openTab({ kind: 'diff', path, staged, title: `${basename(path)} (${staged ? t('staged') : t('changes')})` })
}

async function act(method: string, params: object, ok?: string) {
  try {
    await request(method, params)
    if (ok) toast(ok, 'ok', undefined, 2000)
  } catch (e) {
    errorToast(e)
  }
  refreshGit(50)
}

export function GitPanel() {
  const [message, setMessage] = createSignal('')
  const [amend, setAmend] = createSignal(false)
  const [busy, setBusy] = createSignal(false)
  const [showLog, setShowLog] = createSignal(true)
  // Reloaded on every change of the repository (commit from a terminal included).
  const [log, { refetch: refetchLog }] = createResource(gitRevision, () => request<any[]>('git.log', { n: 30 }).catch(() => []))
  onMount(() => refreshGit(0))
  // Commands typed in a terminal do not produce file events everywhere: poll while visible.
  const poll = setInterval(() => document.visibilityState === 'visible' && refreshGit(0), 8000)
  onCleanup(() => clearInterval(poll))

  const st = () => gitStatus()
  const files = () => st()?.files ?? []
  const conflicts = () => files().filter((f) => f.conflict)
  const staged = () => files().filter((f) => !f.conflict && f.index !== '.' && !f.untracked)
  const unstaged = () => files().filter((f) => !f.conflict && (f.work !== '.' || f.untracked))

  const commit = async () => {
    if (!staged().length && !amend()) {
      toast(t('Nothing to commit: stage files first'), 'info')
      return
    }
    setBusy(true)
    try {
      const out: string = await request('git.commit', { message: message(), amend: amend() })
      setMessage('')
      setAmend(false)
      toast(out.split('\n')[0] || t('Commit created'), 'ok')
      refetchLog()
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
      refreshGit(50)
    }
  }

  const discard = async (list: GitFile[]) => {
    const names = list.map((f) => relPath(f.path))
    const what = names.length > 3 ? tn(names.length, '{n} file', '{n} files') : names.join(', ')
    if (!confirm(`${t('Discard the changes of {files}?', { files: what })} ${list.some((f) => f.untracked) ? t('Untracked files will be deleted.') + ' ' : ''}${t('This cannot be undone.')}`)) return
    await act('git.discard', { paths: list.filter((f) => !f.untracked).map((f) => f.path), untracked: list.filter((f) => f.untracked).map((f) => f.path) })
  }

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
      refetchLog()
    } catch (e) {
      errorToast(e)
    }
  }

  const remote = (cmd: string) => newConsole({ kind: 'task', command: ['git', cmd], title: `git ${cmd}` }).then(() => setTimeout(() => (refreshGit(0), refetchLog()), 3000))

  function Row(props: { f: GitFile; staged: boolean }) {
    const code = () => (props.staged ? props.f.index : props.f.conflict ? 'U' : props.f.work)
    const rel = relPath(props.f.path)
    const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : ''
    return (
      <div
        class="git-row"
        title={`${rel} · ${t(titles[code()] ?? code())}${props.f.origPath ? ` (${t('from {path}', { path: relPath(props.f.origPath) })})` : ''}`}
        onClick={() => (code() === 'D' || props.f.conflict ? openFile(props.f.path) : openDiff(props.f.path, props.staged))}
        onContextMenu={(e) =>
          contextMenu(e, [
            { label: t('Show the differences'), action: () => openDiff(props.f.path, props.staged), disabled: code() === 'D' },
            { label: t('Open the file'), action: () => openFile(props.f.path), disabled: code() === 'D' },
            { separator: true, label: '' },
            props.staged
              ? { label: t('Unstage'), action: () => act('git.unstage', { paths: [props.f.path] }) }
              : { label: t('Stage'), action: () => act('git.stage', { paths: [props.f.path] }) },
            { label: t('Discard the changes'), danger: true, disabled: props.staged, action: () => discard([props.f]) },
            { label: t('Copy the path'), action: () => navigator.clipboard.writeText(props.f.path) },
          ])
        }
      >
        <span class={`git-code code-${code() === '?' ? 'U' : code()}`}>{letters[code()] ?? code()}</span>
        <span class="git-name">{basename(props.f.path)}</span>
        <span class="git-dir">{dir}</span>
        <span class="git-actions" onClick={(e) => e.stopPropagation()}>
          <button class="icon-btn tiny" title={t('Open the file')} onClick={() => openFile(props.f.path)}>
            <Icon name="file" size={11} />
          </button>
          <Show when={!props.staged}>
            <button class="icon-btn tiny" title={t('Discard the changes')} onClick={() => discard([props.f])}>
              <Icon name="undo" size={11} />
            </button>
          </Show>
          <button class="icon-btn tiny" title={props.staged ? t('Unstage') : t('Stage')} onClick={() => act(props.staged ? 'git.unstage' : 'git.stage', { paths: [props.f.path] })}>
            {props.staged ? '−' : '+'}
          </button>
        </span>
      </div>
    )
  }

  function Section(p: { title: string; list: GitFile[]; staged: boolean; actions?: any }) {
    return (
      <Show when={p.list.length}>
        <div class="git-section">
          <div class="git-section-head">
            <span>
              {p.title} <span class="badge">{p.list.length}</span>
            </span>
            <span class="grow" />
            {p.actions}
          </div>
          <For each={p.list}>{(f) => <Row f={f} staged={p.staged} />}</For>
        </div>
      </Show>
    )
  }

  return (
    <div class="panel git-panel">
      <div class="panel-head">
        <span class="panel-title">Git</span>
        <span class="grow" />
        <button class="icon-btn" title={t('Refresh')} onClick={() => (refreshGit(0), refetchLog())}>
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
          <button class="btn small" title={t('git pull (in a terminal)')} onClick={() => remote('pull')}>
            Pull
          </button>
          <button class="btn small" title={t('git push (in a terminal)')} onClick={() => remote('push')}>
            Push
          </button>
          <button class="icon-btn" title={t('git fetch (in a terminal)')} onClick={() => remote('fetch')}>
            ⟳
          </button>
        </div>
        <div class="git-commit">
          <textarea
            class="input"
            rows="3"
            placeholder={t('Commit message (Ctrl+Enter)')}
            value={message()}
            onInput={(e) => setMessage(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                e.preventDefault()
                e.stopPropagation()
                commit()
              }
            }}
          />
          <div class="git-commit-row">
            <label class="check small">
              <input type="checkbox" checked={amend()} onChange={(e) => setAmend(e.currentTarget.checked)} /> Amend
            </label>
            <span class="grow" />
            <button class="btn small primary" disabled={busy() || (!staged().length && !amend())} onClick={commit}>
              Commit{staged().length ? ` (${staged().length})` : ''}
            </button>
          </div>
        </div>
        <div class="panel-body git-files">
          <Section title={t('Conflicts')} list={conflicts()} staged={false} />
          <Section
            title={t('Staged')}
            list={staged()}
            staged={true}
            actions={
              <button class="link small" onClick={() => act('git.unstage', { paths: staged().map((f) => f.path) })}>
                {t('unstage all')}
              </button>
            }
          />
          <Section
            title={t('Changes')}
            list={unstaged()}
            staged={false}
            actions={
              <>
                <button class="link small" onClick={() => discard(unstaged())}>
                  {t('discard all')}
                </button>
                <button class="link small" onClick={() => act('git.stage', { paths: unstaged().map((f) => f.path) })}>
                  {t('stage all')}
                </button>
              </>
            }
          />
          <Show when={!files().length}>
            <p class="muted pad small">{t('No change.')}</p>
          </Show>
          <div class="git-section">
            <div class="git-section-head" onClick={() => setShowLog(!showLog())} style={{ cursor: 'pointer' }}>
              <span class="tree-twist" classList={{ open: showLog() }}>
                <Icon name="chevron" size={12} />
              </span>
              {t('History')}
            </div>
            <Show when={showLog()}>
              <For each={log() ?? []} fallback={<p class="muted small pad">{t('No commit.')}</p>}>
                {(c) => (
                  <div class="git-commit-item" title={`${c.hash}\n${c.author}, ${fmtDate(c.when * 1000)}`} onClick={() => navigator.clipboard.writeText(c.hash).then(() => toast(t('Hash copied'), 'ok', undefined, 1200))}>
                    <span class="mono git-hash">{c.short}</span>
                    <span class="ellipsis">{c.subject}</span>
                    <Show when={c.refs}>
                      <span class="badge">{c.refs.replace('HEAD -> ', '')}</span>
                    </Show>
                    <span class="muted small nowrap">{fmtAgo(c.when * 1000)}</span>
                  </div>
                )}
              </For>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  )
}
