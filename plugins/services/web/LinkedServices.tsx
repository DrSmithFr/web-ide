// The services of the other pods of the account (docs/design/relay.md, *Services*), on the home
// page: each with a switch that links it to a port of this machine, the route it takes (the LAN
// first, else the relay), its state and what went through it.
import { createResource, createSignal, For, onCleanup, Show } from 'solid-js'
import { api, errorToast, t } from './plugin'
import { RouteBadge, type Route } from './RouteBadge'

type LinkView = {
  id: string
  pod: string
  podName: string
  service: string
  name: string
  port: number
  scope: 'local' | 'lan'
  on: boolean
  state: 'off' | 'on' | 'offline' | 'cut' | 'gone' | 'port-taken'
  listen?: number
  route: Route
  requests: number
  bytes: number
}
type RemoteService = { id: string; name: string; link?: LinkView }
type RemotePod = { key: string; name: string; online: boolean; route: Route; services: RemoteService[] }

function stateText(l: LinkView): string {
  switch (l.state) {
    case 'offline':
      return t('{pod} offline, retrying', { pod: l.podName })
    case 'cut':
      return t('cut by {pod}', { pod: l.podName })
    case 'gone':
      return t('no longer shared by {pod}', { pod: l.podName })
    case 'port-taken':
      return t('port {port} taken on this machine', { port: l.port })
  }
  return ''
}

function ServiceLink(props: { pod: RemotePod; s: RemoteService }) {
  const l = () => props.s.link
  const [port, setPort] = createSignal(l()?.port ? String(l()!.port) : '')
  const [scope, setScope] = createSignal<'local' | 'lan'>(l()?.scope ?? 'local')
  const set = (on: boolean) =>
    api.rpc('services.link', { pod: props.pod.key, podName: props.pod.name, service: props.s.id, name: props.s.name, port: Number(port()) || 0, scope: scope(), on }).catch(errorToast)
  const address = () => `http://${scope() === 'lan' ? location.hostname : '127.0.0.1'}:${l()?.listen}`
  return (
    <div class="svc-link" classList={{ on: !!l()?.on }} data-testid={`link-${props.pod.name}-${props.s.name}`}>
      <div class="svc-head">
        <input type="checkbox" class="ai-switch" checked={!!l()?.on} onChange={(e) => set(e.currentTarget.checked)} title={l()?.on ? t('Linked: turn off') : t('Link it to a port of this machine')} data-testid="link-on" />
        <strong>{props.s.name}</strong>
        <Show when={l()?.on && l()?.listen}>
          <a class="mono" href={address()} target="_blank" rel="noreferrer" data-testid="link-address">
            {scope() === 'lan' ? '0.0.0.0' : '127.0.0.1'}:{l()!.listen}
          </a>
          <RouteBadge route={l()!.route} />
        </Show>
        <Show when={l() && stateText(l()!)}>
          <span class="badge warn small" data-testid="link-state">
            {stateText(l()!)}
          </span>
        </Show>
        <span class="grow" />
        <Show when={l()?.on && l()!.requests}>
          <span class="muted small">
            {api.tn(l()!.requests, '{n} request', '{n} requests')} · {api.fmtSize(l()!.bytes)}
          </span>
        </Show>
      </div>
      <div class="svc-opts">
        <label>
          {t('Local port')}
          <input class="input port" inputmode="numeric" placeholder={t('the service’s')} value={port()} onInput={(e) => setPort(e.currentTarget.value)} onChange={() => l()?.on && set(true)} data-testid="link-port" />
        </label>
        <label>
          {t('Reached from')}
          <select class="input" value={scope()} onChange={(e) => (setScope(e.currentTarget.value as 'local' | 'lan'), l()?.on && set(true))} data-testid="link-scope">
            <option value="local">{t('This machine')}</option>
            <option value="lan">{t('The local network')}</option>
          </select>
        </label>
        <Show when={scope() === 'lan'}>
          <span class="warn small">{t('Any machine of the local network reaches this service, without authentication.')}</span>
        </Show>
        <Show when={l() && !l()!.on}>
          <button class="btn small" onClick={() => api.rpc('services.forget', { id: l()!.id }).catch(errorToast)}>
            {t('Forget')}
          </button>
        </Show>
      </div>
    </div>
  )
}

export function LinkedServices() {
  const [pods, { refetch }] = createResource(() => api.rpc<RemotePod[]>('services.remote'))
  onCleanup(api.on('services.links', () => refetch()))
  onCleanup(api.on('pods.changed', () => refetch()))
  // The route moves by itself (the LAN link comes and goes, a tunnel goes idle).
  const timer = setInterval(() => refetch(), 5000)
  onCleanup(() => clearInterval(timer))
  return (
    <Show when={pods.latest?.length}>
      <div class="home-services" data-testid="home-linked-services">
        <div class="home-bar">
          <h3 class="home-sub">{t('Services of your other pods')}</h3>
          <span class="muted small">{t('Link one to a port of this machine: your tools reach it as if it ran here.')}</span>
        </div>
        <For each={pods.latest}>
          {(p) => (
            <div class="svc-row" classList={{ off: !p.online }} data-testid={`remote-pod-${p.name}`}>
              <div class="svc-head">
                <span class="pod-dot" classList={{ on: p.online }} />
                <strong>{p.name}</strong>
                <Show when={!p.online}>
                  <span class="muted small">{t('offline')}</span>
                </Show>
              </div>
              <div class="svc-pods">
                <For each={p.services}>{(s) => <ServiceLink pod={p} s={s} />}</For>
              </div>
            </div>
          )}
        </For>
      </div>
    </Show>
  )
}
