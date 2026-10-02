// SQL / Redis console, opened as a tab of the main editor. The statement under the caret is
// framed; Ctrl+Enter runs it (or opens a chooser when the console holds several).
import { createEffect, createSignal, For, onCleanup, onMount, Show, untrack } from 'solid-js'
import { request } from '../pod/rpc'
import { Doc } from '../editor/doc'
import { useEditorView } from '../ui/EditorArea'
import { findLeaf, mutate, session, setActivePane, type TabState } from '../state/project'
import { registerAction, shortcutOf } from '../keys/bindings'
import { Modal, pick } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { ResultGrid } from './ResultGrid'
import { activeStatement, connById, connect, splitStatements, withAuth, type ConsoleState, type Result } from './api'
import { setCursorInfo } from '../ui/status'

interface HistoryEntry {
  ts: string
  query: string
  durationMs: number
  rows: number
  error?: string
}

export function SqlConsole(props: { tab: TabState; paneId: string }) {
  const tab = props.tab
  const conn = () => connById(tab.connId)
  const lang = conn()?.kind === 'redis' ? 'redis' : 'sql'
  const doc = new Doc(`console:${tab.id}`, untrack(() => session.sqlText[tab.id]) ?? '', { lang })
  let host!: HTMLDivElement
  const [hostEl, setHostEl] = createSignal<HTMLElement>()
  const [state, setState] = createSignal<ConsoleState | null>(null)
  const [result, setResult] = createSignal<Result | null>(null)
  const [error, setError] = createSignal('')
  const [lastQuery, setLastQuery] = createSignal('')
  const [running, setRunning] = createSignal(false)
  const [history, setHistory] = createSignal<HistoryEntry[] | null>(null)

  onMount(async () => {
    setHostEl(host)
    try {
      setState(await request('db.consoleOpen', { id: tab.id, connId: tab.connId, db: tab.db ?? '' }))
    } catch (e) {
      errorToast(e)
    }
  })

  // Keep the text in the session (it survives reloads and is shared with the other windows).
  let saveTimer: number | undefined
  const off = doc.onChange(() => {
    clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => mutate((s) => (s.sqlText[tab.id] = doc.text)), 500)
  })
  onCleanup(() => {
    off()
    clearTimeout(saveTimer)
  })

  const isActive = () => session.activePane === props.paneId && findLeaf(session.layout, props.paneId)?.active === tab.id

  const statements = () => splitStatements(doc.text, lang)
  const frame = () => {
    const v = view()
    if (!v) return
    const list = statements()
    const i = activeStatement(list, v.getSelection().head)
    v.setStatement(list.length > 1 && i >= 0 ? list[i] : null)
  }

  const view = useEditorView(() => doc, hostEl, {
    onSelection: (v) => {
      frame()
      const { line, col } = doc.pos(v.getSelection().head)
      setCursorInfo({ line: line + 1, col: col + 1, sel: 0, lang: lang === 'redis' ? 'Redis' : 'SQL' })
    },
    onFocus: () => setActivePane(props.paneId),
  })
  createEffect(() => {
    if (view()) requestAnimationFrame(() => view()!.focus())
  })

  const run = async (query: string) => {
    if (!query.trim() || running()) return
    setRunning(true)
    setError('')
    setLastQuery(query)
    try {
      const r = await withAuth(tab.connId!, () => request('db.exec', { id: tab.id, query }))
      setResult(r.result)
      setState(r.state)
    } catch (e) {
      setError((e as Error).message)
      setResult(null)
    } finally {
      setRunning(false)
    }
  }

  const runActive = async () => {
    const v = view()
    if (!v) return
    const sel = v.selectedText()
    if (sel.trim()) return run(sel)
    const list = statements()
    if (!list.length) return
    const caret = v.getSelection().head
    const i = activeStatement(list, caret)
    if (list.length === 1) return run(doc.text.slice(list[0][0], list[0][1]))
    const c = v.coordsAt(caret)
    const chosen = await pick({
      placeholder: 'Requête à exécuter',
      noFilter: false,
      initial: i,
      anchor: { left: c.left, top: c.bottom + 4 },
      items: list.map(([a, b], k) => {
        const text = doc.text.slice(a, b)
        const first = text.split('\n')[0]
        return { label: first.length > 90 ? first.slice(0, 90) + '…' : first + (text.includes('\n') ? ' …' : ''), detail: `#${k + 1}`, value: k }
      }),
    })
    v.focus()
    if (chosen !== null && chosen !== undefined) {
      v.setStatement(list[chosen])
      run(doc.text.slice(list[chosen][0], list[chosen][1]))
    }
  }

  const call = async (method: string, extra: object = {}) => {
    try {
      setState(await request(method, { id: tab.id, ...extra }))
    } catch (e) {
      errorToast(e)
    }
  }

  const offs = [
    registerAction('sql.execute', () => {
      if (!isActive()) return false
      runActive()
    }),
    registerAction('edit.undo', () => (view()?.hasFocus() ? view()!.undo() : false)),
    registerAction('edit.redo', () => (view()?.hasFocus() ? view()!.redo() : false)),
    registerAction('edit.toggleComment', () => (view()?.hasFocus() ? view()!.toggleComment() : false)),
    registerAction('edit.duplicateLine', () => (view()?.hasFocus() ? view()!.duplicateLine() : false)),
    registerAction('edit.deleteLine', () => (view()?.hasFocus() ? view()!.deleteLine() : false)),
    registerAction('nav.subwordLeft', () => (view()?.hasFocus() ? view()!.moveSubword(-1, false) : false)),
    registerAction('nav.subwordRight', () => (view()?.hasFocus() ? view()!.moveSubword(1, false) : false)),
    registerAction('nav.subwordLeftSelect', () => (view()?.hasFocus() ? view()!.moveSubword(-1, true) : false)),
    registerAction('nav.subwordRightSelect', () => (view()?.hasFocus() ? view()!.moveSubword(1, true) : false)),
  ]
  onCleanup(() => offs.forEach((f) => f()))

  const showHistory = async () => {
    try {
      setHistory(await request('db.history', { connId: tab.connId }))
    } catch (e) {
      errorToast(e)
    }
  }

  const connected = () => conn()?.status.state === 'connected'
  const auto = () => state()?.autoCommit ?? true
  const inTx = () => !!state()?.inTx

  return (
    <div class="sql-console">
      <div class="toolbar">
        <button class="btn small primary" disabled={running()} onClick={runActive} title={`Exécuter la requête active (${shortcutOf('sql.execute')})`}>
          <Icon name="play" size={12} /> Exécuter
        </button>
        <button class="btn small" onClick={showHistory} title="Historique des commandes">
          <Icon name="history" size={13} /> Historique
        </button>
        <Show when={lang === 'sql'}>
          <span class="sep" />
          <label class="check" title="Transaction automatique">
            <input type="checkbox" checked={auto()} onChange={(e) => call('db.autocommit', { on: e.currentTarget.checked })} /> Auto-commit
          </label>
          <button class="btn small" disabled={auto() || !inTx()} onClick={() => call('db.commit')}>
            <Icon name="check" size={13} /> Commit
          </button>
          <button class="btn small" disabled={auto() || !inTx()} onClick={() => call('db.rollback')}>
            <Icon name="undo" size={13} /> Rollback
          </button>
        </Show>
        <button class="btn small" disabled={!running()} onClick={() => request('db.cancel', { id: tab.id }).catch(errorToast)} title="Annuler l'instruction en cours">
          <Icon name="stop" size={12} /> Annuler
        </button>
        <span class="grow" />
        <Show when={inTx()}>
          <span class="badge warn">transaction ouverte</span>
        </Show>
        <span class="conn-label" title={conn()?.status.error}>
          <span class={`dot dot-${connected() ? 'connected' : conn()?.status.state === 'error' ? 'error' : 'disconnected'}`} />
          {conn()?.name ?? 'connexion supprimée'}
          {tab.db ? ` · ${tab.db}` : ''}
          <Show when={!connected()}>
            <button class="link" onClick={() => connect(tab.connId!).catch(errorToast)}>
              {conn()?.status.state === 'closed' ? 'rouvrir' : 'connecter'}
            </button>
          </Show>
        </span>
      </div>
      <div class="sql-split">
        <div class="editor-host sql-editor" ref={host} />
        <div class="sql-result">
          <Show when={running()}>
            <div class="muted pad">Exécution…</div>
          </Show>
          <Show when={!running() && error()}>
            <div class="sql-error">
              <strong>Erreur</strong>
              <pre>{error()}</pre>
            </div>
          </Show>
          <Show when={!running() && !error() && result()}>
            {(r) => (
              <>
                <div class="result-meta">
                  <span>{r().command}</span>
                  <span>
                    {r().columns?.length ? `${r().rows?.length ?? 0} ligne(s)${r().truncated ? ' (tronqué à 1000)' : ''}` : `${r().affected} ligne(s) affectée(s)`}
                  </span>
                  <span>{r().durationMs.toFixed(1)} ms</span>
                  <span class="muted mono ellipsis" title={lastQuery()}>
                    {lastQuery().split('\n')[0]}
                  </span>
                </div>
                <Show when={r().columns?.length}>
                  <ResultGrid result={r()} />
                </Show>
              </>
            )}
          </Show>
          <Show when={!running() && !error() && !result()}>
            <div class="muted pad small">
              {shortcutOf('sql.execute')} exécute la requête sous le curseur. {lang === 'redis' ? 'Une commande par ligne (GET, HGETALL, SCAN…).' : 'Séparer les requêtes par « ; ».'}
            </div>
          </Show>
        </div>
      </div>
      <Show when={history()}>
        <HistoryModal
          entries={history()!}
          onClose={() => setHistory(null)}
          onInsert={(q) => {
            const v = view()
            if (!v) return
            const end = doc.text.length
            const text = (doc.text && !doc.text.endsWith('\n') ? '\n' : '') + q + (q.trimEnd().endsWith(';') || lang === 'redis' ? '' : ';')
            v.edit(end, end, text)
            setHistory(null)
          }}
          onRun={(q) => {
            setHistory(null)
            run(q)
          }}
        />
      </Show>
    </div>
  )
}

function HistoryModal(props: { entries: HistoryEntry[]; onClose: () => void; onInsert: (q: string) => void; onRun: (q: string) => void }) {
  const [index, setIndex] = createSignal(0)
  const cur = () => props.entries[index()]
  const key = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown') setIndex((i) => Math.min(props.entries.length - 1, i + 1))
    else if (e.key === 'ArrowUp') setIndex((i) => Math.max(0, i - 1))
    else if (e.key === 'Enter' && cur()) props.onInsert(cur().query)
    else return
    e.preventDefault()
  }
  return (
    <Modal title="Historique des commandes" onClose={props.onClose} class="modal-wide">
      <div class="history" tabIndex={0} onKeyDown={key} ref={(el) => queueMicrotask(() => el.focus())}>
        <div class="history-list">
          <For each={props.entries} fallback={<div class="muted pad">Aucune commande exécutée sur cette connexion.</div>}>
            {(e, i) => (
              <div class="history-item" classList={{ selected: i() === index(), failed: !!e.error }} onClick={() => setIndex(i())} onDblClick={() => props.onInsert(e.query)}>
                <span class="mono ellipsis">{e.query.split('\n')[0]}</span>
                <span class="muted small">
                  {new Date(e.ts).toLocaleString()} · {e.durationMs.toFixed(0)} ms{e.error ? ' · erreur' : ''}
                </span>
              </div>
            )}
          </For>
        </div>
        <div class="history-detail">
          <Show when={cur()}>
            <pre class="mono">{cur()!.query}</pre>
            <Show when={cur()!.error}>
              <p class="danger small">{cur()!.error}</p>
            </Show>
            <p class="muted small">
              {new Date(cur()!.ts).toLocaleString()} · {cur()!.durationMs.toFixed(1)} ms · {cur()!.rows} ligne(s)
            </p>
            <div class="form-actions">
              <button class="btn" onClick={() => navigator.clipboard.writeText(cur()!.query).then(() => toast('Copié', 'ok'))}>
                Copier
              </button>
              <button class="btn" onClick={() => props.onInsert(cur()!.query)}>
                Insérer dans la console
              </button>
              <button class="btn primary" onClick={() => props.onRun(cur()!.query)}>
                Exécuter
              </button>
            </div>
          </Show>
        </div>
      </div>
    </Modal>
  )
}
