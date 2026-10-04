// Detail pane of the Docker tool: Infos (inspect and live statistics) and Logs of the selected
// container, or a summary and the logs of the stack.
import { createResource, For, Show, type JSX } from 'solid-js'
import { request } from '../pod/rpc'
import { mutate, session } from '../state/project'
import { Icon } from '../ui/icons'
import { t } from '../i18n'
import { Logs } from './Logs'
import { portLabel, profiles, selected, selectedContainer, setSelected, shell, stack, stats, status, type Port } from './state'

interface Details {
  id: string
  name: string
  image: string
  state: string
  health?: string
  exitCode: number
  command: string
  created: string
  started?: string
  ports: Port[]
  mounts: { type: string; name?: string; source: string; destination: string; readOnly: boolean }[]
  networks: { name: string; ip: string; aliases: string[] }[]
}

export function Detail() {
  const tab = () => session.docker.detail
  const setTab = (id: 'infos' | 'logs') => mutate((s) => (s.docker.detail = id))
  const c = selectedContainer
  const isStack = () => selected()?.kind === 'stack'
  const title = () => (isStack() ? stack()?.stack?.name ?? '' : c()?.service || c()?.name || '')
  return (
    <div class="dk-detail" data-testid="docker-detail">
      <div class="dk-detail-head">
        <span class="dk-detail-title ellipsis">{title()}</span>
        <div class="dk-subtabs">
          <button class="toggle" classList={{ on: tab() === 'infos' }} onClick={() => setTab('infos')} data-testid="docker-detail-infos">
            {t('Infos')}
          </button>
          <button class="toggle" classList={{ on: tab() === 'logs' }} onClick={() => setTab('logs')} data-testid="docker-detail-logs">
            {t('Logs')}
          </button>
        </div>
        <span class="grow" />
        <Show when={c()?.state === 'running'}>
          <button class="icon-btn small" title={t('Shell')} onClick={() => shell(c()!)} data-testid="docker-shell">
            <Icon name="terminal" size={13} />
          </button>
        </Show>
        <button class="icon-btn small" title={t('Close')} onClick={() => setSelected(null)}>
          <Icon name="close" size={12} />
        </button>
      </div>
      <Show
        when={tab() === 'logs'}
        fallback={
          <div class="dk-detail-body">
            <Show when={isStack()} fallback={<Show when={c()} keyed>{(x) => <Infos id={x.id} state={x.state} />}</Show>}>
              <StackInfos />
            </Show>
          </div>
        }
      >
        <Show when={isStack()} fallback={<Show when={c()} keyed>{(x) => <Logs id={x.id} running={x.state === 'running'} />}</Show>}>
          <Logs id="" services={stack()?.stack?.services} running={!!stack()?.stack?.containers.some((x) => x.state === 'running')} />
        </Show>
      </Show>
    </div>
  )
}

function KV(props: { k: string; children: JSX.Element }) {
  return (
    <>
      <dt>{props.k}</dt>
      <dd>{props.children}</dd>
    </>
  )
}

function StackInfos() {
  const s = () => stack()?.stack
  return (
    <dl class="dk-kv">
      <KV k={t('Compose project')}>{s()?.name}</KV>
      <KV k={t('File')}>
        <span class="mono">{status()?.composeFile}</span>
      </KV>
      <KV k={t('Services')}>{s()?.services.join(', ')}</KV>
      <KV k={t('Running')}>{t('{n}/{total} running', { n: s()?.containers.filter((c) => c.state === 'running').length ?? 0, total: s()?.containers.length ?? 0 })}</KV>
      <Show when={profiles().length}>
        <KV k={t('Profiles')}>{profiles().join(', ')}</KV>
      </Show>
    </dl>
  )
}

const date = (s?: string) => (s && !s.startsWith('0001') ? new Date(s).toLocaleString() : '')

function Infos(props: { id: string; state: string }) {
  // Read again when the state of the container changes.
  const [d] = createResource(
    () => `${props.id}:${props.state}`,
    () => request<Details>('docker.inspect', { id: props.id }),
  )
  const st = () => stats()[props.id]
  return (
    <Show when={d()} fallback={<p class="muted">{d.error ? (d.error as Error).message : t('Loading…')}</p>}>
      <dl class="dk-kv">
        <KV k={t('Image')}>
          <span class="mono">{d()!.image}</span>
        </KV>
        <KV k={t('State')}>
          {d()!.state}
          <Show when={d()!.health}> · {d()!.health}</Show>
          <Show when={d()!.state === 'exited'}> · {t('code {code}', { code: d()!.exitCode })}</Show>
        </KV>
        <KV k={t('Command')}>
          <span class="mono small">{d()!.command}</span>
        </KV>
        <KV k={t('Created')}>{date(d()!.created)}</KV>
        <Show when={d()!.started}>
          <KV k={t('Started')}>{date(d()!.started)}</KV>
        </Show>
        <Show when={st()}>
          <KV k={t('CPU')}>
            <span data-testid="docker-cpu">{st()!.cpu}</span>
          </KV>
          <KV k={t('Memory')}>
            <span data-testid="docker-mem">
              {st()!.mem} ({st()!.memPerc})
            </span>
          </KV>
          <KV k={t('Network I/O')}>{st()!.net}</KV>
          <KV k={t('Block I/O')}>{st()!.block}</KV>
          <KV k={t('Processes')}>{st()!.pids}</KV>
        </Show>
        <KV k={t('Ports')}>
          <div class="dk-items" data-testid="docker-ports">
            <For each={d()!.ports} fallback={<span class="muted">{t('none')}</span>}>
              {(p) => <span class="mono" classList={{ muted: !p.published }}>{portLabel(p)}</span>}
            </For>
          </div>
        </KV>
        <KV k={t('Mounts')}>
          <div class="dk-items" data-testid="docker-mounts">
            <For each={d()!.mounts} fallback={<span class="muted">{t('none')}</span>}>
              {(m) => (
                <span class="mono small" title={m.source}>
                  <span class="muted">{m.type}</span> {m.type === 'volume' && m.name && m.name.length < 40 ? m.name : m.source} → {m.destination}
                  <Show when={m.readOnly}>
                    <span class="badge">ro</span>
                  </Show>
                </span>
              )}
            </For>
          </div>
        </KV>
        <KV k={t('Networks')}>
          <div class="dk-items" data-testid="docker-networks">
            <For each={d()!.networks} fallback={<span class="muted">{t('none')}</span>}>
              {(n) => (
                <span class="mono small">
                  {n.name} <span class="muted">{n.ip}</span>
                  <Show when={n.aliases.length}>
                    <span class="muted"> · {n.aliases.join(', ')}</span>
                  </Show>
                </span>
              )}
            </For>
          </div>
        </KV>
      </dl>
    </Show>
  )
}
