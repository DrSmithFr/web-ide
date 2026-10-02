// Database explorer state: connections of the project (stored in .ide/connections.json,
// secrets kept by the pod) and helpers asking for passwords when the pod needs them.
import { createSignal } from 'solid-js'
import { on, request, RpcError } from '../pod/rpc'
import { prompt } from '../ui/overlay'

export interface SSHTunnel {
  enabled: boolean
  host: string
  port: number
  user: string
  auth: 'agent' | 'key' | 'password'
  keyPath?: string
}

export interface ConnConfig {
  id: string
  name: string
  kind: 'sqlite' | 'postgres' | 'redis'
  path?: string
  host?: string
  port?: number
  database?: string
  user?: string
  sslMode?: string
  redisDb?: number
  rememberPassword: boolean
  ssh?: SSHTunnel
}

export interface ConnView extends ConnConfig {
  status: { state: 'untested' | 'connected' | 'error' | 'closed'; error?: string }
  hasSecret: boolean
}

export interface Secret {
  password?: string
  sshPassword?: string
  sshPassphrase?: string
}

export interface DbNode {
  id: string
  label: string
  kind: '' | 'database' | 'table' | 'view' | 'column' | 'index' | 'key'
  detail?: string
  leaf: boolean
  db?: string
  table?: string
}

export interface Result {
  columns: string[] | null
  rows: unknown[][] | null
  affected: number
  command?: string
  durationMs: number
  truncated?: boolean
  total: number
  inTx: boolean
  message?: string
}

export interface ConsoleState {
  id: string
  connId: string
  db: string
  autoCommit: boolean
  inTx: boolean
  connected: boolean
  running: boolean
}

const [connections, setConnections] = createSignal<ConnView[]>([])
export { connections }

export async function refreshConnections() {
  try {
    setConnections(await request('db.list'))
  } catch {
    /* no project */
  }
}
on('db.changed', (list: ConnView[]) => setConnections(list))

export function connById(id?: string) {
  return connections().find((c) => c.id === id)
}

/** Runs f; when the pod needs a database or SSH secret, asks for it, connects, retries once. */
export async function withAuth<T>(connId: string, f: () => Promise<T>): Promise<T> {
  for (let attempt = 0; ; attempt++) {
    try {
      return await f()
    } catch (e) {
      if (!(e instanceof RpcError) || attempt > 2) throw e
      let secret: Secret | null = null
      if (e.code === 'db_password') {
        const pw = await prompt({ title: 'Mot de passe', label: e.message, password: true })
        if (pw === null) throw e
        secret = { password: pw }
      } else if (e.code === 'auth_required') {
        const pw = await prompt({ title: e.data?.kind === 'passphrase' ? 'Phrase de passe SSH' : 'Mot de passe SSH', label: e.message, password: true })
        if (pw === null) throw e
        secret = e.data?.kind === 'passphrase' ? { sshPassphrase: pw } : { sshPassword: pw }
      } else throw e
      await connectWith(connId, secret)
    }
  }
}

async function connectWith(id: string, secret: Secret) {
  try {
    await request('db.connect', { id, secret })
  } catch (e) {
    if (e instanceof RpcError && (e.code === 'db_password' || e.code === 'auth_required')) return
    throw e
  }
}

export function connect(id: string) {
  return withAuth(id, () => request('db.connect', { id, secret: {} }))
}

export function queryLanguage(c?: ConnView) {
  return c?.kind === 'redis' ? 'redis' : 'sql'
}

/**
 * Splits a console into statements. SQL: ";" outside strings, identifiers, comments and
 * $$ blocks. Redis: one command per line (or per ";").
 */
export function splitStatements(text: string, lang: string): [number, number][] {
  const out: [number, number][] = []
  let start = 0
  const push = (end: number) => {
    const seg = text.slice(start, end)
    if (seg.trim()) {
      const lead = seg.length - seg.trimStart().length
      const trail = seg.length - seg.trimEnd().length
      out.push([start + lead, end - trail])
    }
  }
  if (lang === 'redis') {
    for (let i = 0; i <= text.length; i++) {
      const ch = text[i]
      if (i === text.length || ch === '\n' || ch === ';') {
        push(i)
        start = i + 1
      }
    }
    return out
  }
  let i = 0
  while (i < text.length) {
    const ch = text[i]
    const next = text[i + 1]
    if (ch === "'" || ch === '"' || ch === '`') {
      const q = ch
      i++
      while (i < text.length && !(text[i] === q && text[i + 1] !== q)) i += text[i] === q ? 2 : 1
      i++
    } else if (ch === '-' && next === '-') {
      while (i < text.length && text[i] !== '\n') i++
    } else if (ch === '/' && next === '*') {
      const end = text.indexOf('*/', i + 2)
      i = end < 0 ? text.length : end + 2
    } else if (ch === '$' && /[$A-Za-z_]/.test(next ?? '')) {
      const m = /^\$[A-Za-z_]*\$/.exec(text.slice(i))
      if (m) {
        const end = text.indexOf(m[0], i + m[0].length)
        i = end < 0 ? text.length : end + m[0].length
      } else i++
    } else if (ch === ';') {
      push(i + 1)
      start = i + 1
      i++
    } else i++
  }
  push(text.length)
  return out
}

/** Statement under the caret (or the last one before it). */
export function activeStatement(list: [number, number][], caret: number): number {
  if (!list.length) return -1
  for (let i = 0; i < list.length; i++) if (caret >= list[i][0] && caret <= list[i][1]) return i
  let best = -1
  for (let i = 0; i < list.length; i++) if (list[i][1] <= caret) best = i
  return best < 0 ? 0 : best
}
