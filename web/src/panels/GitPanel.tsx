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

const letters: Record<string, string> = { M: 'M', A: 'A', D: 'D', R: 'R', C: 'C', T: 'T', U: 'U', '?': 'U' }
const titles: Record<string, string> = { M: 'modifié', A: 'ajouté', D: 'supprimé', R: 'renommé', C: 'copié', T: 'type changé', U: 'en conflit', '?': 'non suivi' }

export function openDiff(path: string, staged: boolean) {
  openTab({ kind: 'diff', path, staged, title: `${basename(path)} (${staged ? 'indexé' : 'modifications'})` })
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
      toast('Rien à committer : indexer des fichiers d’abord', 'info')
      return
    }
    setBusy(true)
    try {
      const out: string = await request('git.commit', { message: message(), amend: amend() })
      setMessage('')
      setAmend(false)
      toast(out.split('\n')[0] || 'Commit créé', 'ok')
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
    if (!confirm(`Annuler les modifications de ${names.length > 3 ? names.length + ' fichiers' : names.join(', ')} ? ${list.some((f) => f.untracked) ? 'Les fichiers non suivis seront supprimés. ' : ''}Action définitive.`)) return
    await act('git.discard', { paths: list.filter((f) => !f.untracked).map((f) => f.path), untracked: list.filter((f) => f.untracked).map((f) => f.path) })
  }

  const branches = async () => {
    try {
      const list: any[] = await request('git.branches')
      const choice = await pick<string>({
        placeholder: 'Changer de branche',
        items: [
          { label: '+ Nouvelle branche…', value: '\0new' },
          ...list.filter((b) => !b.current).map((b) => ({ label: b.name, detail: b.remote ? 'distante' : b.upstream ? `→ ${b.upstream}` : '', value: b.name })),
        ],
      })
      if (!choice) return
      if (choice === '\0new') {
        const name = await prompt({ title: 'Nouvelle branche', label: `Créée depuis ${st()?.branch}` })
        if (name) await act('git.switch', { name: name.trim(), create: true }, `Branche ${name} créée`)
      } else await act('git.switch', { name: choice.replace(/^origin\//, ''), create: false }, `Sur ${choice}`)
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
        title={`${rel} · ${titles[code()] ?? code()}${props.f.origPath ? ` (depuis ${relPath(props.f.origPath)})` : ''}`}
        onClick={() => (code() === 'D' || props.f.conflict ? openFile(props.f.path) : openDiff(props.f.path, props.staged))}
        onContextMenu={(e) =>
          contextMenu(e, [
            { label: 'Voir les différences', action: () => openDiff(props.f.path, props.staged), disabled: code() === 'D' },
            { label: 'Ouvrir le fichier', action: () => openFile(props.f.path), disabled: code() === 'D' },
            { separator: true, label: '' },
            props.staged
              ? { label: 'Désindexer', action: () => act('git.unstage', { paths: [props.f.path] }) }
              : { label: 'Indexer', action: () => act('git.stage', { paths: [props.f.path] }) },
            { label: 'Annuler les modifications', danger: true, disabled: props.staged, action: () => discard([props.f]) },
            { label: 'Copier le chemin', action: () => navigator.clipboard.writeText(props.f.path) },
          ])
        }
      >
        <span class={`git-code code-${code() === '?' ? 'U' : code()}`}>{letters[code()] ?? code()}</span>
        <span class="git-name">{basename(props.f.path)}</span>
        <span class="git-dir">{dir}</span>
        <span class="git-actions" onClick={(e) => e.stopPropagation()}>
          <button class="icon-btn tiny" title="Ouvrir le fichier" onClick={() => openFile(props.f.path)}>
            <Icon name="file" size={11} />
          </button>
          <Show when={!props.staged}>
            <button class="icon-btn tiny" title="Annuler les modifications" onClick={() => discard([props.f])}>
              <Icon name="undo" size={11} />
            </button>
          </Show>
          <button class="icon-btn tiny" title={props.staged ? 'Désindexer' : 'Indexer'} onClick={() => act(props.staged ? 'git.unstage' : 'git.stage', { paths: [props.f.path] })}>
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
        <button class="icon-btn" title="Rafraîchir" onClick={() => (refreshGit(0), refetchLog())}>
          <Icon name="refresh" />
        </button>
      </div>
      <Show
        when={st()?.repo}
        fallback={
          <div class="pad">
            <p class="muted">{st() ? "Le projet n'est pas dans un dépôt Git." : 'Chargement…'}</p>
            <Show when={st()}>
              <button class="btn" onClick={() => act('git.init', {}, 'Dépôt initialisé')}>
                Initialiser un dépôt
              </button>
            </Show>
          </div>
        }
      >
        <div class="git-branch">
          <button class="btn small" title="Changer de branche" onClick={branches}>
            <Icon name="branch" size={12} /> {st()!.branch === '(detached)' ? 'HEAD détachée' : st()!.branch}
          </button>
          <Show when={st()!.upstream}>
            <span class="muted small" title={`Suivie : ${st()!.upstream}`}>
              ↑{st()!.ahead} ↓{st()!.behind}
            </span>
          </Show>
          <span class="grow" />
          <button class="btn small" title="git pull (dans un terminal)" onClick={() => remote('pull')}>
            Pull
          </button>
          <button class="btn small" title="git push (dans un terminal)" onClick={() => remote('push')}>
            Push
          </button>
          <button class="icon-btn" title="git fetch (dans un terminal)" onClick={() => remote('fetch')}>
            ⟳
          </button>
        </div>
        <div class="git-commit">
          <textarea
            class="input"
            rows="3"
            placeholder={`Message de commit (Ctrl+Entrée)`}
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
          <Section title="Conflits" list={conflicts()} staged={false} />
          <Section
            title="Indexés"
            list={staged()}
            staged={true}
            actions={
              <button class="link small" onClick={() => act('git.unstage', { paths: staged().map((f) => f.path) })}>
                tout désindexer
              </button>
            }
          />
          <Section
            title="Modifications"
            list={unstaged()}
            staged={false}
            actions={
              <>
                <button class="link small" onClick={() => discard(unstaged())}>
                  tout annuler
                </button>
                <button class="link small" onClick={() => act('git.stage', { paths: unstaged().map((f) => f.path) })}>
                  tout indexer
                </button>
              </>
            }
          />
          <Show when={!files().length}>
            <p class="muted pad small">Aucune modification.</p>
          </Show>
          <div class="git-section">
            <div class="git-section-head" onClick={() => setShowLog(!showLog())} style={{ cursor: 'pointer' }}>
              <span class="tree-twist" classList={{ open: showLog() }}>
                <Icon name="chevron" size={12} />
              </span>
              Historique
            </div>
            <Show when={showLog()}>
              <For each={log() ?? []} fallback={<p class="muted small pad">Aucun commit.</p>}>
                {(c) => (
                  <div class="git-commit-item" title={`${c.hash}\n${c.author}, ${c.when}`} onClick={() => navigator.clipboard.writeText(c.hash).then(() => toast('Hash copié', 'ok', undefined, 1200))}>
                    <span class="mono git-hash">{c.short}</span>
                    <span class="ellipsis">{c.subject}</span>
                    <Show when={c.refs}>
                      <span class="badge">{c.refs.replace('HEAD -> ', '')}</span>
                    </Show>
                    <span class="muted small nowrap">{c.when}</span>
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
