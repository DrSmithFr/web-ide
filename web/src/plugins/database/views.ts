// The tabs of the Database explorer: a SQL / Redis console and the data of a table, views of
// the plugin whose state (connection, database, table, the text of a console) goes with the tab.
import { connById, type DbNode } from './connections'
import { api, t, type ViewState } from './plugin'

/** A new console on a connection (and a database), starting with a text. */
export function openConsole(connId: string, db?: string, text?: string) {
  const c = connById(connId)
  api.openView('sql', {
    title: `${t('Console')} · ${c?.name ?? ''}${db ? ' · ' + db : ''}`,
    // Each console is a tab of its own.
    key: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
    state: { connId, db: db ?? '', text: text ?? '' } satisfies ViewState,
  })
}

export function openTable(connId: string, n: DbNode) {
  api.openView('table', { title: n.label, key: `${connId}/${n.db ?? ''}/${n.table}`, state: { connId, db: n.db ?? '', table: n.table } satisfies ViewState })
}
