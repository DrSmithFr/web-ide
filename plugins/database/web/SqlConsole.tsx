// SQL / Redis console, opened as a tab of the main editor. The statement under the caret is
// framed; Ctrl+Enter runs it (or opens a chooser when the console holds several).
import { createEffect, createSignal, For, onCleanup, onMount, Show, untrack } from 'solid-js'
import type { TabState } from '@ide/api'
import { ResultGrid } from './ResultGrid'
import { activeStatement, connById, connect, splitStatements, withAuth, type ConsoleState, type Result } from './connections'
import { api, copyText, errorToast, fmtDate, Icon, Modal, pick, t, tn, toast, viewState } from './plugin'

interface HistoryEntry {
  ts: string
  query: string
  durationMs: number
  rows: number
  error?: string
}

export function SqlConsole(props: { tab: TabState; paneId: string }) {
  const tab = props.tab
  const st = viewState(tab)
  const conn = () => connById(st.connId)
  const lang = conn()?.kind === 'redis' ? 'redis' : 'sql'
  const doc = new api.editor.Doc(`console:${tab.id}`, untrack(() => viewState(tab).text) ?? '', { lang })
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
      setState(await api.rpc('consoleOpen', { id: tab.id, connId: st.connId, db: st.db ?? '' }))
    } catch (e) {
      errorToast(e)
    }
  })

  // Keep the text in the state of the tab (it survives reloads and is shared with the other windows).
  let saveTimer: number | undefined
  const off = doc.onChange(() => {
    clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => api.setViewState<{ text?: string }>(tab.id, (s) => (s.text = doc.text)), 500)
  })
  onCleanup(() => {
    off()
    clearTimeout(saveTimer)
  })

  const isActive = () => api.panes.isActive(props.paneId, tab.id)

  const statements = () => splitStatements(doc.text, lang)
  const frame = () => {
    const v = view()
    if (!v) return
    const list = statements()
    const i = activeStatement(list, v.getSelection().head)
    v.setStatement(list.length > 1 && i >= 0 ? list[i] : null)
  }

  const view = api.editor.useView(() => doc, hostEl, {
    onSelection: (v) => {
      frame()
      const { line, col } = doc.pos(v.getSelection().head)
      api.editor.setCursor({ line: line + 1, col: col + 1, sel: 0, lang: lang === 'redis' ? 'Redis' : 'SQL' })
    },
    onFocus: () => api.panes.activate(props.paneId),
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
      const r = await withAuth(st.connId, () => api.rpc('exec', { id: tab.id, query }))
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
      placeholder: t('Statement to run'),
      codeLabel: true,
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
      setState(await api.rpc(method, { id: tab.id, ...extra }))
    } catch (e) {
      errorToast(e)
    }
  }

  const offs = [
    api.keys.register('database.execute', () => {
      if (!isActive()) return false
      runActive()
    }),
    api.keys.register('edit.undo', () => (view()?.hasFocus() ? view()!.undo() : false)),
    api.keys.register('edit.redo', () => (view()?.hasFocus() ? view()!.redo() : false)),
    api.keys.register('edit.toggleComment', () => (view()?.hasFocus() ? view()!.toggleComment() : false)),
    api.keys.register('edit.duplicateLine', () => (view()?.hasFocus() ? view()!.duplicateLine() : false)),
    api.keys.register('edit.deleteLine', () => (view()?.hasFocus() ? view()!.deleteLine() : false)),
    api.keys.register('nav.subwordLeft', () => (view()?.hasFocus() ? view()!.moveSubword(-1, false) : false)),
    api.keys.register('nav.subwordRight', () => (view()?.hasFocus() ? view()!.moveSubword(1, false) : false)),
    api.keys.register('nav.subwordLeftSelect', () => (view()?.hasFocus() ? view()!.moveSubword(-1, true) : false)),
    api.keys.register('nav.subwordRightSelect', () => (view()?.hasFocus() ? view()!.moveSubword(1, true) : false)),
  ]
  onCleanup(() => offs.forEach((f) => f()))

  const showHistory = async () => {
    try {
      setHistory(await api.rpc('history', { connId: st.connId }))
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
        <button class="btn small primary" disabled={running()} onClick={runActive} title={`${t('Run the active statement')} (${api.keys.shortcut('database.execute')})`}>
          <Icon name="play" size={12} /> {t('Run')}
        </button>
        <button class="btn small" onClick={showHistory} title={t('Command history')}>
          <Icon name="history" size={13} /> {t('History')}
        </button>
        <Show when={lang === 'sql'}>
          <span class="sep" />
          <label class="check" title={t('Automatic transaction')}>
            <input type="checkbox" checked={auto()} onChange={(e) => call('autocommit', { on: e.currentTarget.checked })} /> Auto-commit
          </label>
          <button class="btn small" disabled={auto() || !inTx()} onClick={() => call('commit')}>
            <Icon name="check" size={13} /> Commit
          </button>
          <button class="btn small" disabled={auto() || !inTx()} onClick={() => call('rollback')}>
            <Icon name="undo" size={13} /> Rollback
          </button>
        </Show>
        <button class="btn small" disabled={!running()} onClick={() => api.rpc('cancel', { id: tab.id }).catch(errorToast)} title={t('Cancel the running statement')}>
          <Icon name="stop" size={12} /> {t('Cancel')}
        </button>
        <span class="grow" />
        <Show when={inTx()}>
          <span class="badge warn">{t('open transaction')}</span>
        </Show>
        <span class="conn-label" title={conn()?.status.error}>
          <span class={`dot dot-${connected() ? 'connected' : conn()?.status.state === 'error' ? 'error' : 'disconnected'}`} />
          {conn()?.name ?? t('connection deleted')}
          {st.db ? ` · ${st.db}` : ''}
          <Show when={!connected()}>
            <button class="link" onClick={() => connect(st.connId).catch(errorToast)}>
              {conn()?.status.state === 'closed' ? 'rouvrir' : 'connecter'}
            </button>
          </Show>
        </span>
      </div>
      <div class="sql-split">
        <div class="editor-host sql-editor" ref={host} />
        <div class="sql-result">
          <Show when={running()}>
            <div class="muted pad">{t('Running…')}</div>
          </Show>
          <Show when={!running() && error()}>
            <div class="sql-error">
              <strong>{t('Error')}</strong>
              <pre>{error()}</pre>
            </div>
          </Show>
          <Show when={!running() && !error() && result()}>
            {(r) => (
              <>
                <div class="result-meta">
                  <span>{r().command}</span>
                  <span>
                    {r().columns?.length ? `${tn(r().rows?.length ?? 0, '{n} row', '{n} rows')}${r().truncated ? ` (${t('truncated at 1000')})` : ''}` : tn(r().affected ?? 0, '{n} row affected', '{n} rows affected')}
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
              {t('{shortcut} runs the statement under the caret.', { shortcut: api.keys.shortcut('database.execute') })} {lang === 'redis' ? t('One command per line (GET, HGETALL, SCAN…).') : t('Separate the statements with “;”.')}
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
    <Modal title={t('Command history')} onClose={props.onClose} class="modal-wide">
      <div class="history" tabIndex={0} onKeyDown={key} ref={(el) => queueMicrotask(() => el.focus())}>
        <div class="history-list">
          <For each={props.entries} fallback={<div class="muted pad">{t('No command run on this connection.')}</div>}>
            {(e, i) => (
              <div class="history-item" classList={{ selected: i() === index(), failed: !!e.error }} onClick={() => setIndex(i())} onDblClick={() => props.onInsert(e.query)}>
                <span class="mono ellipsis">{e.query.split('\n')[0]}</span>
                <span class="muted small">
                  {fmtDate(e.ts)} · {e.durationMs.toFixed(0)} ms{e.error ? ` · ${t('error')}` : ''}
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
              {fmtDate(cur()!.ts)} · {cur()!.durationMs.toFixed(1)} ms · {tn(cur()!.rows ?? 0, '{n} row', '{n} rows')}
            </p>
            <div class="form-actions">
              <button class="btn" onClick={() => copyText(cur()!.query).then((ok) => ok && toast(t('Copied'), 'ok'))}>
                {t('Copy')}
              </button>
              <button class="btn" onClick={() => props.onInsert(cur()!.query)}>
                {t('Insert in the console')}
              </button>
              <button class="btn primary" onClick={() => props.onRun(cur()!.query)}>
                {t('Run')}
              </button>
            </div>
          </Show>
        </div>
      </div>
    </Modal>
  )
}
