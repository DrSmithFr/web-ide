// Docker tools of the assistant, read-only in every mode: state of the Compose services of the
// project and the last lines of the logs of a service or container. It runs no Docker action.
import { request } from '../pod/rpc'
import { session } from '../state/project'
import { stripAnsi } from '../docker/ansi'
import { portLabel, type Container, type DockerStatus, type Stack } from '../docker/state'
import { t, tn } from '../i18n'
import type { ToolResult } from './tools'

const str = (description: string) => ({ type: 'string', description })
const int = (description: string) => ({ type: 'integer', description })
const fn = (name: string, description: string, properties: Record<string, any>, required: string[] = []) => ({
  type: 'function',
  function: { name, description, parameters: { type: 'object', properties, required } },
})

export const dockerToolDefs = [
  fn(
    'docker_ps',
    'State of the Docker Compose services of the project (Compose file at the project root): container, state, health, image, published ports. Without Compose file, lists every container of the host.',
    {},
  ),
  fn(
    'docker_logs',
    'Last lines of the logs of a Compose service of the project, or of any container (name or id), stdout and stderr together, with timestamps.',
    { service: str('Compose service of the project'), container: str('Container name or id (instead of service)'), lines: int('Number of lines (200 by default, 2000 max)'), filter: str('Keep only the lines containing this text (case-insensitive)') },
  ),
]

export const dockerToolNames = new Set(dockerToolDefs.map((d) => d.function.name))

const profiles = () => session.docker.profiles

function row(c: Container) {
  const ports = c.ports.filter((p) => p.published).map(portLabel).join(', ')
  return `- ${c.service ? c.service + ' (' + c.name + ')' : c.name}: ${c.status || c.state}${c.health ? ', ' + c.health : ''} · ${c.image}${ports ? ' · ports ' + ports : ''}`
}

async function ps(): Promise<ToolResult> {
  const st = await request<DockerStatus>('docker.status')
  if (!st.available) return { content: `Error: Docker is not reachable: ${st.error}`, summary: st.error ?? '', status: 'error' }
  if (!st.composeFile || !st.compose) {
    const list = await request<Container[]>('docker.containers')
    const why = st.composeFile ? 'Docker Compose is not installed' : 'No Compose file at the project root'
    return { content: `${why}. Containers of the host:\n${list.map(row).join('\n') || '(none)'}`, summary: tn(list.length, '{n} container', '{n} containers'), status: 'ok' }
  }
  const s = await request<Stack>('docker.stack', { profiles: profiles().filter((p) => st.profiles?.includes(p)) })
  const missing = s.services.filter((svc) => !s.containers.some((c) => c.service === svc))
  const running = s.containers.filter((c) => c.state === 'running').length
  const text = [
    `Compose project "${s.name}" (${st.composeFile}), ${running}/${s.services.length} services running.`,
    ...s.containers.map(row),
    ...missing.map((svc) => `- ${svc}: not created`),
    ...(st.profiles?.length ? [`Profiles declared: ${st.profiles.join(', ')}; active: ${profiles().join(', ') || 'none'}.`] : []),
  ]
  return { content: text.join('\n'), summary: t('{n}/{total} running', { n: running, total: s.services.length }), status: 'ok' }
}

async function logs(a: Record<string, any>): Promise<ToolResult> {
  const service = String(a.service ?? '').trim()
  const container = String(a.container ?? '').trim()
  if (!service && !container) throw new Error('service or container is missing')
  const lines = Math.min(Math.max(Number(a.lines) || 200, 1), 2000)
  const r = await request<{ output: string }>('docker.logsTail', { id: container, service: container ? '' : service, profiles: profiles(), lines })
  let out = stripAnsi(r.output).replace(/\s+$/, '').split('\n')
  const filter = String(a.filter ?? '').toLowerCase()
  if (filter) out = out.filter((l) => l.toLowerCase().includes(filter))
  const what = container || service
  return {
    content: `Logs of ${what} (last ${lines} lines${filter ? `, filtered on "${a.filter}"` : ''}):\n${out.join('\n') || '(no output)'}`,
    summary: `${what} · ${tn(out.length, '{n} line', '{n} lines')}`,
    status: 'ok',
  }
}

export async function runDockerTool(name: string, a: Record<string, any>): Promise<ToolResult> {
  return name === 'docker_ps' ? ps() : logs(a)
}
