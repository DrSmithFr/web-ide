// Commit tab: the changed files as a tree with a check box per row (checked: staged),
// and the commit form at the bottom.
import { createEffect, createMemo, createSignal, Show } from 'solid-js'
import { request } from '../../pod/rpc'
import { mutate, openFile, relPath, session } from '../../state/project'
import { fileState, gitStatus, refreshGit, type GitFile } from '../../state/git'
import { contextMenu } from '../../ui/overlay'
import { errorToast, toast } from '../../ui/toast'
import { Icon } from '../../ui/icons'
import { t, tn } from '../../i18n'
import { discard, openChange, push, pushMenu, stage, unstage, type PushMode } from './actions'
import { FileTree } from './FileTree'
import { Resizer } from './Resizer'
import { buildTree, type TreeNode } from './tree'

// Kept while the tool is hidden.
const [message, setMessage] = createSignal('')
const [amend, setAmend] = createSignal(false)
const [closed, setClosed] = createSignal(new Set<string>())
let amendText = ''

type Check = 'on' | 'off' | 'mixed'

/** Checked when everything is in the index, mixed when part of it is. */
function checkOf(f: GitFile): Check {
  if (f.conflict || f.untracked || f.index === '.') return 'off'
  return f.work === '.' ? 'on' : 'mixed'
}

function checkOfAll(list: GitFile[]): Check {
  const all = list.map(checkOf)
  if (all.every((c) => c === 'on')) return 'on'
  if (all.every((c) => c === 'off')) return 'off'
  return 'mixed'
}

export function ChangesTab() {
  const [busy, setBusy] = createSignal(false)
  const files = () => gitStatus()?.files ?? []
  const tree = createMemo(() => buildTree(files(), gitStatus()?.top ?? '', (f) => f.path))
  const staged = () => files().filter((f) => !f.conflict && f.index !== '.' && !f.untracked)

  const toggle = (n: TreeNode<GitFile>) => (checkOfAll(n.items) === 'on' ? unstage(n.items) : stage(n.items))

  const menu = (e: MouseEvent, n: TreeNode<GitFile>) => {
    const f = n.item
    contextMenu(e, [
      ...(f
        ? [
            { label: t('Show the differences'), action: () => openChange(f) },
            { label: t('Open the file'), action: () => openFile(f.path), disabled: f.work === 'D' || f.index === 'D' },
            { separator: true, label: '' },
          ]
        : []),
      { label: t('Stage'), action: () => stage(n.items), disabled: checkOfAll(n.items) === 'on' },
      { label: t('Unstage'), action: () => unstage(n.items), disabled: checkOfAll(n.items) === 'off' },
      { label: t('Discard the changes'), danger: true, action: () => discard(n.items) },
      { separator: true, label: '' },
      { label: t('Copy the path'), action: () => navigator.clipboard.writeText(n.path) },
    ])
  }

  const commit = async (): Promise<boolean> => {
    if (!staged().length && !amend()) {
      toast(t('Nothing to commit: stage files first'), 'info')
      return false
    }
    setBusy(true)
    try {
      const out: string = await request('git.commit', { message: message(), amend: amend() })
      setMessage('')
      setAmend(false)
      toast(out.split('\n')[0] || t('Commit created'), 'ok')
      return true
    } catch (e) {
      errorToast(e)
      return false
    } finally {
      setBusy(false)
      refreshGit(50)
    }
  }
  const commitAndPush = async (mode: PushMode = '') => {
    if (mode === 'force' && !confirm(t('Force the push? Commits of the remote branch that are not here will be lost.'))) return
    if (await commit()) push(mode, true)
  }

  // Amend starts from the message of the last commit.
  const setAmendOn = async (on: boolean) => {
    setAmend(on)
    if (!on) {
      if (message() === amendText) setMessage('')
      return
    }
    if (message().trim()) return
    try {
      const head = await request<{ message: string }>('git.commitInfo', { rev: 'HEAD' })
      amendText = head.message
      if (amend() && !message().trim()) setMessage(head.message)
    } catch {
      // No commit yet.
    }
  }

  const Check = (p: { node: TreeNode<GitFile> }) => {
    const c = () => checkOfAll(p.node.items)
    let box!: HTMLInputElement
    createEffect(() => {
      box.checked = c() === 'on'
      box.indeterminate = c() === 'mixed'
    })
    return (
      <input
        ref={box}
        type="checkbox"
        class="git-check"
        tabIndex={-1}
        title={c() === 'on' ? t('Unstage') : t('Stage')}
        onClick={(e) => {
          e.preventDefault()
          toggle(p.node)
        }}
      />
    )
  }

  return (
    <div class="git-split">
      <div class="panel-body">
        <Show when={files().length} fallback={<p class="muted pad small">{t('No change.')}</p>}>
          <FileTree
            root={tree()}
            testid="git-changes"
            label={
              <span class="tree-name">
                {t('Changes')} <span class="badge">{files().length}</span>
              </span>
            }
            closed={closed}
            setClosed={setClosed}
            stateOf={(f) => fileState(f.path) ?? 'modified'}
            fromOf={(f) => (f.origPath ? relPath(f.origPath) : undefined)}
            end={(n) => <Check node={n} />}
            toggle={toggle}
            open={openChange}
            openFile={(f) => openFile(f.path)}
            menu={menu}
          />
        </Show>
      </div>
      <Resizer height={session.git.form} set={(h) => mutate((s) => (s.git.form = h))} />
      <div class="git-bottom git-commit" style={{ height: `${session.git.form}px` }}>
        <textarea
          class="input"
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
            <input type="checkbox" checked={amend()} onChange={(e) => setAmendOn(e.currentTarget.checked)} /> Amend
          </label>
          <span class="git-commit-btns">
            <button class="btn small primary" disabled={busy() || (!staged().length && !amend())} title={tn(staged().length, '{n} staged file', '{n} staged files')} onClick={commit}>
              Commit{staged().length ? ` (${staged().length})` : ''}
            </button>
            <span class="btn-group">
              <button class="btn small" disabled={busy() || (!staged().length && !amend())} onClick={() => commitAndPush()}>
                {t('Commit and push')}
              </button>
              <button class="btn small" title={t('Force the push')} disabled={busy() || (!staged().length && !amend())} onClick={(e) => pushMenu(e, commitAndPush, true)}>
                <Icon name="chevron" size={11} />
              </button>
            </span>
          </span>
        </div>
      </div>
    </div>
  )
}
