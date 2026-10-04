// Detail of a commit of the history: actions, author, message, parents and changed files.
import { createMemo, createResource, createSignal, For, Show } from 'solid-js'
import { request } from '../../pod/rpc'
import { openFile, relPath } from '../../state/project'
import { gitRevision, gitStatus } from '../../state/git'
import { contextMenu } from '../../ui/overlay'
import { Icon } from '../../ui/icons'
import { fmtAgo, fmtDate, t, tn } from '../../i18n'
import { branchAt, copyHash, openCommitFile, reset, revert, type CommitFile, type LogCommit } from './actions'
import { FileTree } from './FileTree'
import { buildTree } from './tree'

interface Info extends LogCommit {
  message: string
  committer: string
  committed: number
  files: CommitFile[]
}

const colorOf: Record<string, string> = { A: 'added', D: 'deleted', '?': 'untracked' }

export function CommitDetail(props: { hash: string; select: (hash: string) => void; close: () => void }) {
  const [info] = createResource(
    () => ({ hash: props.hash, rev: gitRevision() }),
    ({ hash }) => request<Info>('git.commitInfo', { rev: hash }),
  )
  const [closed, setClosed] = createSignal(new Set<string>())
  const tree = createMemo(() => buildTree(info()?.files ?? [], gitStatus()?.top ?? '', (f) => f.path))

  const resetMenu = (e: MouseEvent, c: LogCommit) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.bottom + 2 }), [
      { label: t('Soft: keep the index and the files'), action: () => reset(c, 'soft') },
      { label: t('Mixed: keep the files'), action: () => reset(c, 'mixed') },
      { label: t('Hard: drop every change'), danger: true, action: () => reset(c, 'hard') },
    ])
  }

  return (
    <div class="git-detail" data-testid="git-detail">
      <Show when={info()} fallback={<p class={info.error ? 'danger pad small' : 'muted pad small'}>{info.error ? String(info.error.message ?? info.error) : t('Loading…')}</p>}>
        {(c) => (
          <>
            <div class="git-detail-bar">
              <button class="link mono git-hash" title={t('Copy the hash')} onClick={() => copyHash(c().hash)}>
                {c().short}
              </button>
              <span class="grow" />
              <button class="icon-btn" title={t('Copy the hash')} onClick={() => copyHash(c().hash)}>
                <Icon name="copy" size={13} />
              </button>
              <button class="icon-btn" title={t('New branch here…')} onClick={() => branchAt(c())}>
                <Icon name="branch" size={13} />
              </button>
              <button class="icon-btn" title={t('Revert…')} onClick={() => revert(c())}>
                <Icon name="undo" size={13} />
              </button>
              <button class="btn small" title={t('Reset the branch to this commit')} onClick={(e) => resetMenu(e, c())}>
                Reset <Icon name="chevron" size={10} />
              </button>
              <button class="icon-btn" title={t('Close')} onClick={props.close}>
                <Icon name="close" size={13} />
              </button>
            </div>
            <div class="git-detail-body">
              <div class="git-detail-meta small">
                <span class="git-meta-author">
                  {c().author} &lt;{c().email}&gt;
                </span>{' '}
                <span class="muted" title={fmtDate(c().when * 1000)}>
                  {fmtDate(c().when * 1000)} ({fmtAgo(c().when * 1000)})
                </span>
                <Show when={c().committer !== c().author}>
                  <div class="muted">{t('Committed by {name}', { name: c().committer })}</div>
                </Show>
                <Show when={c().parents.length}>
                  <div class="muted">
                    {tn(c().parents.length, 'Parent', 'Parents')}:{' '}
                    <For each={c().parents}>
                      {(p) => (
                        <button class="link mono" onClick={() => props.select(p)}>
                          {p.slice(0, 7)}
                        </button>
                      )}
                    </For>
                  </div>
                </Show>
              </div>
              <pre class="git-message">{c().message}</pre>
              <Show when={c().files.length} fallback={<p class="muted small pad">{t('No file changed.')}</p>}>
                <FileTree
                  root={tree()}
                  testid="git-commit-files"
                  label={<span class="tree-name">{tn(c().files.length, '{n} file changed', '{n} files changed')}</span>}
                  closed={closed}
                  setClosed={setClosed}
                  stateOf={(f) => colorOf[f.status] ?? 'modified'}
                  fromOf={(f) => (f.origPath ? relPath(f.origPath) : undefined)}
                  open={(f) => openCommitFile(c().hash, f)}
                  openFile={(f) => f.status !== 'D' && openFile(f.path)}
                  menu={(e, n) =>
                    n.item &&
                    contextMenu(e, [
                      { label: t('Show the differences'), action: () => openCommitFile(c().hash, n.item!) },
                      { label: t('Open the file'), action: () => openFile(n.item!.path), disabled: n.item.status === 'D' },
                      { label: t('Copy the path'), action: () => navigator.clipboard.writeText(n.item!.path) },
                    ])
                  }
                />
              </Show>
            </div>
          </>
        )}
      </Show>
    </div>
  )
}
