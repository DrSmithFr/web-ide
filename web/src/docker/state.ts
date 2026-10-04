// State of the Docker tool: status of docker, Compose stack of the project, containers of the
// host, instant statistics, selection. Lists are refreshed while the tool is shown.
import { createSignal } from 'solid-js'
import { request } from '../pod/rpc'
import { session } from '../state/project'
import { newConsole } from '../console/consoles'
import { errorToast } from '../ui/toast'

export interface Port {
  ip?: string
  published?: number
  target: number
  protocol: string
}

export interface Container {
  id: string
  name: string
  image: string
  state: string
  status: string
  health?: string
  project?: string
  service?: string
  ports: Port[]
}

export interface Stack {
  name: string
  services: string[]
  containers: Container[]
}

export interface DockerStatus {
  available: boolean
  error?: string
  compose: boolean
  composeFile?: string
  profiles?: string[]
}

export interface Stats {
  cpu: string
  mem: string
  memPerc: string
  net: string
  block: string
  pids: string
}

/** The Stack row of the Project tab, or a container (by id). */
export type Selection = { kind: 'stack' } | { kind: 'container'; id: string } | null

const [status, setStatus] = createSignal<DockerStatus | null>(null)
const [stack, setStack] = createSignal<{ stack?: Stack; error?: string } | null>(null)
const [host, setHost] = createSignal<{ list?: Container[]; error?: string } | null>(null)
const [stats, setStats] = createSignal<Record<string, Stats>>({})
const [selected, setSelected] = createSignal<Selection>(null)
/** Keys of the actions running in the pod (`stack`, a service or a container id): spinners. */
const [busy, setBusy] = createSignal<Set<string>>(new Set())
export { status, stack, host, stats, selected, setSelected, busy }

export const profiles = () => session.docker.profiles.filter((p) => status()?.profiles?.includes(p))

export async function refreshStatus() {
  try {
    setStatus(await request<DockerStatus>('docker.status'))
  } catch (e) {
    setStatus({ available: false, compose: false, error: (e as Error).message })
  }
}

export async function refreshStack() {
  if (!status()?.available || !status()?.compose || !status()?.composeFile) return
  try {
    setStack({ stack: await request<Stack>('docker.stack', { profiles: profiles() }) })
  } catch (e) {
    setStack({ error: (e as Error).message })
  }
}

export async function refreshHost() {
  if (!status()?.available) return
  try {
    setHost({ list: await request<Container[]>('docker.containers') })
  } catch (e) {
    setHost({ error: (e as Error).message })
  }
}

/** Instant values of the running containers of the shown list. */
export async function refreshStats(list: Container[]) {
  const ids = list.filter((c) => c.state === 'running').map((c) => c.id)
  try {
    setStats(ids.length ? await request<Record<string, Stats>>('docker.stats', { ids }) : {})
  } catch {
    /* the list shows the error */
  }
}

/** Container of the selection, in the stack or the host list. */
export function selectedContainer(): Container | undefined {
  const s = selected()
  if (s?.kind !== 'container') return undefined
  return stack()?.stack?.containers.find((c) => c.id === s.id) ?? host()?.list?.find((c) => c.id === s.id)
}

async function busyWhile(key: string, f: () => Promise<unknown>) {
  setBusy((b) => new Set(b).add(key))
  try {
    await f()
  } catch (e) {
    errorToast(e)
  } finally {
    setBusy((b) => {
      const n = new Set(b)
      n.delete(key)
      return n
    })
    void refreshStack()
    void refreshHost()
  }
}

/** Start, stop or restart of the stack (service empty) or of a service, run by the pod. */
export const composeAction = (action: 'start' | 'stop' | 'restart', service = '') =>
  busyWhile(service || 'stack', () => request('docker.compose', { profiles: profiles(), action, service }))

export const containerAction = (action: 'start' | 'stop' | 'restart' | 'remove', id: string) =>
  busyWhile(id, () => request('docker.container', { action, id }))

const quote = (s: string) => (/^[\w./:=@,+-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`)

/** A longer Compose command (recreate, rebuild, pull, down) run as a Console task. */
export function composeTask(kind: 'recreate' | 'rebuild' | 'pull' | 'down' | 'downVolumes', service = '') {
  const base = ['docker', 'compose', ...profiles().flatMap((p) => ['--profile', p])]
  const svc = service ? [service] : []
  const cmds: string[][] = {
    recreate: [[...base, 'up', '-d', '--force-recreate', ...svc]],
    rebuild: [[...base, 'up', '-d', '--build', ...svc]],
    pull: [
      [...base, 'pull', ...svc],
      [...base, 'up', '-d', ...svc],
    ],
    down: [[...base, 'down']],
    downVolumes: [[...base, 'down', '-v']],
  }[kind]
  const line = cmds.map((c) => c.map(quote).join(' ')).join(' && ')
  return newConsole({ kind: 'task', command: ['sh', '-c', line], title: line.replace(/^docker compose /, 'compose ') })
}

/** A terminal in a container: bash when it has one, else sh. */
export function shell(c: Container, root = false) {
  return newConsole({
    command: ['docker', 'exec', '-it', ...(root ? ['-u', 'root'] : []), c.id, 'sh', '-c', 'command -v bash >/dev/null && exec bash || exec sh'],
    title: `${c.service || c.name}${root ? ' (root)' : ''}`,
  })
}

export function portLabel(p: Port) {
  if (!p.published) return `${p.target}/${p.protocol}`
  return `${p.ip ? p.ip + ':' : ''}${p.published}→${p.target}${p.protocol === 'tcp' ? '' : '/' + p.protocol}`
}
