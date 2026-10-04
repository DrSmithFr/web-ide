// Database explorer tool: connections > databases > tables > columns and indexes
// (Redis: databases > typed keys with TTL). Click expands, right click opens the menu,
// double click on a table opens the spreadsheet view.
import { batch, createSignal, For, onMount, Show } from 'solid-js'
import { createStore } from 'solid-js/store'
import { request } from '../pod/rpc'
import { mutate, openTab, openTextTab } from '../state/project'
import { contextMenu } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { connect, connections, connById, refreshConnections, withAuth, type ConnView, type DbNode } from './api'
import { ConnectionsModal } from './ConnectionForm'
import { t } from '../i18n'
import { EmptyState } from '../ui/EmptyState'
import { copyText } from '../ui/clipboard'

interface Sel {
  connId: string
  node: DbNode | null
}

const [children, setChildren] = createStore<Record<string, DbNode[] | undefined>>({})
const [expanded, setExpanded] = createStore<Record<string, boolean>>({})
const [errors, setErrors] = createStore<Record<string, string | undefined>>({})

const key = (connId: string, n: DbNode | null) => connId + '|' + (n?.id ?? '')

async function load(connId: string, n: DbNode | null) {
  const k = key(connId, n)
  setErrors(k, undefined)
  try {
    const list = await withAuth(connId, () => request<DbNode[]>('db.children', { id: connId, node: n ?? { id: '', kind: '' } }))
    setChildren(k, list)
  } catch (e) {
    setErrors(k, (e as Error).message)
    setChildren(k, [])
  }
}

function toggle(connId: string, n: DbNode | null) {
  const k = key(connId, n)
  const open = !expanded[k]
  setExpanded(k, open)
  if (open && !children[k]) load(connId, n)
}

export function openConsole(connId: string, db?: string, text?: string) {
  const c = connById(connId)
  // In one batch, so the console starts with its text.
  batch(() => {
    const id = openTab({ kind: 'sql', title: `${t('Console')} · ${c?.name ?? ''}${db ? ' · ' + db : ''}`, connId, db: db ?? '' })
    if (text) mutate((s) => (s.sqlText[id] = (s.sqlText[id] ? s.sqlText[id] + '\n' : '') + text))
  })
}

function openTable(connId: string, n: DbNode) {
  openTab({ kind: 'table', title: n.label, connId, db: n.db, table: n.table })
}

function quoteIdent(kind: string, name: string) {
  if (kind === 'postgres') return name.split('.').map((p) => `"${p.replace(/"/g, '""')}"`).join('.')
  return `"${name.replace(/"/g, '""')}"`
}

export function DatabaseTool() {
  const [sel, setSel] = createSignal<Sel | null>(null)
  const [editing, setEditing] = createSignal<string | 'new' | null>(null)
  onMount(refreshConnections)

  const conn = () => connById(sel()?.connId)
  const selTable = () => {
    const n = sel()?.node
    return n && (n.kind === 'table' || n.kind === 'view' || n.kind === 'key') ? n : null
  }

  const refresh = (c: ConnView) => {
    for (const k of Object.keys(children)) if (k.startsWith(c.id + '|')) setChildren(k, undefined)
    setExpanded(key(c.id, null), true)
    load(c.id, null)
  }
  const close = async (c: ConnView) => {
    await request('db.disconnect', { id: c.id }).catch(errorToast)
    for (const k of Object.keys(expanded)) if (k.startsWith(c.id + '|')) setExpanded(k, false)
  }
  const duplicate = async (c: ConnView) => {
    const { status: _s, hasSecret: _h, ...cfg } = c
    try {
      await request('db.save', { config: { ...cfg, id: '', name: c.name + ' (copie)' }, secret: {} })
    } catch (e) {
      errorToast(e)
    }
  }
  const remove = async (c: ConnView) => {
    if (!confirm(t('Delete the connection “{name}”? The open consoles switch to disconnected mode.', { name: c.name }))) return
    await request('db.delete', { id: c.id }).catch(errorToast)
  }

  const connMenu = (e: MouseEvent, c: ConnView) =>
    contextMenu(e, [
      { label: c.status.state === 'connected' ? t('Close the connection') : t('Connect'), action: () => (c.status.state === 'connected' ? close(c) : connect(c.id).then(() => refresh(c)).catch(errorToast)) },
      { label: t('New console'), action: () => openConsole(c.id) },
      { separator: true, label: '' },
      { label: t('Edit…'), action: () => setEditing(c.id) },
      { label: t('Duplicate'), action: () => duplicate(c) },
      { label: t('Refresh'), action: () => refresh(c) },
      { label: t('Delete'), danger: true, action: () => remove(c) },
    ])

  const nodeMenu = (e: MouseEvent, c: ConnView, n: DbNode) => {
    const showText = async (title: string, f: () => Promise<string>) => {
      try {
        openTextTab(title, await f(), c.kind === 'redis' ? 'redis' : 'sql')
      } catch (err) {
        errorToast(err)
      }
    }
    switch (n.kind) {
      case 'database':
        return contextMenu(e, [
          { label: t('New console on this database'), action: () => openConsole(c.id, n.db) },
          { label: t('Refresh'), action: () => load(c.id, n) },
        ])
      case 'table':
      case 'view':
        return contextMenu(e, [
          { label: t('View the data'), action: () => openTable(c.id, n) },
          { label: t('View the DDL'), action: () => showText(`DDL · ${n.label}`, () => request('db.ddl', { id: c.id, db: n.db, table: n.table })) },
          { label: t('Empty query on the table'), action: () => openConsole(c.id, n.db, `SELECT *\nFROM ${quoteIdent(c.kind, n.table!)}\nLIMIT 100;`) },
          { separator: true, label: '' },
          { label: t('Copy the name'), action: () => copyText(n.table!) },
          { label: t('Refresh'), action: () => load(c.id, n) },
        ])
      case 'column':
        return contextMenu(e, [
          { label: t('Copy the name'), action: () => copyText(n.label) },
          { label: t('Copy the qualified name (table.column)'), action: () => copyText(`${n.table!.split('.').pop()}.${n.label}`) },
        ])
      case 'index':
        return contextMenu(e, [
          { label: t('View the definition'), action: () => showText(`Index · ${n.label}`, () => request('db.indexDef', { id: c.id, db: n.db, table: n.table, index: n.label })) },
          { label: t('Copy the name'), action: () => copyText(n.label) },
        ])
      case 'key':
        return contextMenu(e, [
          { label: t('View the value'), action: () => openTable(c.id, n) },
          { label: t('Copy the name'), action: () => copyText(n.label) },
          { label: t('Console with TTL'), action: () => openConsole(c.id, n.db, `TYPE ${JSON.stringify(n.label)}\nTTL ${JSON.stringify(n.label)}`) },
        ])
    }
  }

  const icon = (k: string) => ({ database: 'database', table: 'table', view: 'outline', column: 'outline', index: 'key', key: 'key' })[k] ?? 'file'

  function NodeRows(p: { c: ConnView; parent: DbNode | null; depth: number }) {
    const k = () => key(p.c.id, p.parent)
    return (
      <>
        <Show when={errors[k()]}>
          <div class="tree-error" style={{ 'padding-left': `${p.depth * 14 + 22}px` }}>
            {errors[k()]}
          </div>
        </Show>
        <Show when={expanded[k()] && !children[k()] && !errors[k()]}>
          <div class="muted small" style={{ 'padding-left': `${p.depth * 14 + 22}px` }}>
            {t('loading…')}
          </div>
        </Show>
        <For each={children[k()] ?? []}>
          {(n) => {
            const nk = () => key(p.c.id, n)
            const selected = () => sel()?.connId === p.c.id && sel()?.node?.id === n.id
            return (
              <>
                <div
                  class="tree-row"
                  classList={{ active: selected() }}
                  style={{ 'padding-left': `${p.depth * 14 + 6}px` }}
                  onClick={() => {
                    setSel({ connId: p.c.id, node: n })
                    if (!n.leaf) toggle(p.c.id, n)
                  }}
                  onDblClick={() => (n.kind === 'table' || n.kind === 'view' || n.kind === 'key') && openTable(p.c.id, n)}
                  onContextMenu={(e) => {
                    setSel({ connId: p.c.id, node: n })
                    nodeMenu(e, p.c, n)
                  }}
                >
                  <span class="tree-twist" classList={{ open: !!expanded[nk()], none: n.leaf }}>
                    <Show when={!n.leaf}>
                      <Icon name="chevron" size={12} />
                    </Show>
                  </span>
                  <Icon name={icon(n.kind)} size={13} />
                  <span class="tree-name">{n.label}</span>
                  <Show when={n.detail}>
                    <span class="tree-detail">{n.detail}</span>
                  </Show>
                </div>
                <Show when={!n.leaf && expanded[nk()]}>
                  <NodeRows c={p.c} parent={n} depth={p.depth + 1} />
                </Show>
              </>
            )
          }}
        </For>
      </>
    )
  }

  return (
    <div class="panel db-tool">
      <div class="panel-head">
        <span class="panel-title">{t('Database explorer')}</span>
      </div>
      <div class="toolbar compact">
        <button class="icon-btn" title={t('Add a connection')} onClick={() => setEditing('new')}>
          <Icon name="plus" size={14} />
        </button>
        <button class="icon-btn" title={t('Edit the connections')} onClick={() => setEditing(sel()?.connId ?? connections()[0]?.id ?? 'new')}>
          <Icon name="edit" size={14} />
        </button>
        <button class="icon-btn" title={t('Refresh the selected connection')} disabled={!conn()} onClick={() => conn() && refresh(conn()!)}>
          <Icon name="refresh" size={14} />
        </button>
        <button class="icon-btn" title={t('Close the selected connection')} disabled={conn()?.status.state !== 'connected'} onClick={() => conn() && close(conn()!)}>
          <Icon name="stop" size={14} />
        </button>
        <button class="icon-btn" title={t('Open a console (selected connection)')} disabled={!conn()} onClick={() => conn() && openConsole(conn()!.id, sel()?.node?.db)}>
          <Icon name="terminal" size={14} />
        </button>
        <button class="icon-btn" title={t('Open the selected table')} disabled={!selTable()} onClick={() => selTable() && openTable(sel()!.connId, selTable()!)}>
          <Icon name="table" size={14} />
        </button>
      </div>
      <div class="panel-body tree">
        <For each={connections()} fallback={<EmptyState icon="database" text={t('No connection yet: SQLite, PostgreSQL or Redis.')} action={t('Add a connection')} onAction={() => setEditing('new')} />}>
          {(c) => {
            const k = () => key(c.id, null)
            return (
              <>
                <div
                  class="tree-row conn-row"
                  classList={{ active: sel()?.connId === c.id && !sel()?.node }}
                  onClick={() => {
                    setSel({ connId: c.id, node: null })
                    toggle(c.id, null)
                  }}
                  onContextMenu={(e) => {
                    setSel({ connId: c.id, node: null })
                    connMenu(e, c)
                  }}
                  title={c.status.error ?? ''}
                >
                  <span class="tree-twist" classList={{ open: !!expanded[k()] }}>
                    <Icon name="chevron" size={12} />
                  </span>
                  <span class={`dot dot-${c.status.state === 'connected' ? 'connected' : c.status.state === 'error' ? 'error' : 'idle'}`} title={t({ connected: 'db|connected', error: 'db|error', untested: 'db|untested', closed: 'db|closed' }[c.status.state] ?? '')} />
                  <span class="tree-name">{c.name}</span>
                  <span class="tree-detail">
                    {c.kind}
                    {c.host ? ` · ${c.host}` : ''}
                    {c.ssh ? ' · ssh' : ''}
                  </span>
                </div>
                <Show when={expanded[k()]}>
                  <NodeRows c={c} parent={null} depth={1} />
                </Show>
              </>
            )
          }}
        </For>
      </div>
      <Show when={editing()}>
        <ConnectionsModal
          initial={editing()!}
          onClose={() => {
            setEditing(null)
            refreshConnections()
          }}
        />
      </Show>
    </div>
  )
}

