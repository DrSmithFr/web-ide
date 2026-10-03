// Spreadsheet view of a table (or of a Redis key): paginated, no query to write. Same
// connection as the consoles; the pod shares the result cache between them.
import { createEffect, createSignal, on, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { openTab, type TabState } from '../state/project'
import { Icon } from '../ui/icons'
import { ResultGrid } from './ResultGrid'
import { connById, connect, withAuth, type Result } from './api'
import { errorToast } from '../ui/toast'
import { t } from '../i18n'

export function TableView(props: { tab: TabState; paneId: string }) {
  const tab = props.tab
  const [offset, setOffset] = createSignal(0)
  const [limit, setLimit] = createSignal(100)
  const [result, setResult] = createSignal<Result | null>(null)
  const [error, setError] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  const conn = () => connById(tab.connId)

  const load = async (refresh = false) => {
    setBusy(true)
    setError('')
    try {
      const r = await withAuth(tab.connId!, () =>
        request<Result>('db.page', { id: tab.connId, db: tab.db, table: tab.table, offset: offset(), limit: limit(), refresh }),
      )
      setResult(r)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  createEffect(on([offset, limit], () => load()))

  const total = () => result()?.total ?? -1
  const lastPage = () => (total() > 0 ? Math.floor((total() - 1) / limit()) * limit() : offset())
  const hasNext = () => (total() >= 0 ? offset() + limit() < total() : (result()?.rows?.length ?? 0) === limit())

  return (
    <div class="table-view">
      <div class="toolbar">
        <button class="icon-btn" title={t('First page')} disabled={offset() === 0} onClick={() => setOffset(0)}>
          «
        </button>
        <button class="icon-btn" title={t('Previous page')} disabled={offset() === 0} onClick={() => setOffset(Math.max(0, offset() - limit()))}>
          ‹
        </button>
        <span class="small">
          {offset() + 1} – {offset() + (result()?.rows?.length ?? 0)}
          {total() >= 0 ? ` ${t('of {n}', { n: total() })}` : ''}
          {result()?.message ? ` (${result()!.message})` : ''}
        </span>
        <button class="icon-btn" title={t('Next page')} disabled={!hasNext()} onClick={() => setOffset(offset() + limit())}>
          ›
        </button>
        <button class="icon-btn" title={t('Last page')} disabled={!hasNext() || total() < 0} onClick={() => setOffset(lastPage())}>
          »
        </button>
        <select class="input small" value={limit()} onChange={(e) => (setOffset(0), setLimit(parseInt(e.currentTarget.value, 10)))}>
          <option value="50">50</option>
          <option value="100">100</option>
          <option value="250">250</option>
          <option value="500">500</option>
        </select>
        <button class="icon-btn" title={t('Refresh')} onClick={() => load(true)}>
          <Icon name="refresh" />
        </button>
        <span class="grow" />
        <span class="muted small">
          {conn()?.name} · {tab.db} · {tab.table}
        </span>
        <button
          class="btn small"
          onClick={() => openTab({ kind: 'sql', title: `${t('Console')} · ${conn()?.name ?? ''}`, connId: tab.connId, db: tab.db })}
        >
          {t('Console')}
        </button>
      </div>
      <Show when={error()}>
        <div class="sql-error">
          <pre>{error()}</pre>
          <Show when={conn()?.status.state === 'closed'}>
            <button class="btn small" onClick={() => connect(tab.connId!).then(() => load(true)).catch(errorToast)}>
              {t('Reopen the connection')}
            </button>
          </Show>
        </div>
      </Show>
      <Show when={busy() && !result()}>
        <div class="muted pad">{t('Loading…')}</div>
      </Show>
      <Show when={result()}>{(r) => <ResultGrid result={r()} offset={offset()} />}</Show>
    </div>
  )
}
