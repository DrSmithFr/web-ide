// The services this pod shares (docs/design/relay.md, *Services*), on the home page: an HTTP
// service of this machine (127.0.0.1:<port>) the other pods of the account reach, with its switch,
// and the pods attached to it, each with Cut.
import { createResource, createSignal, For, onCleanup, Show } from 'solid-js'
import { api, errorToast, Icon, t } from './plugin'
import { RouteBadge, type Route } from './RouteBadge'
import './services.css'

type Attached = { key: string; name: string; since: number; last?: number; requests: number; bytes: number; route?: Route }
type Service = { id: string; name: string; port: number; enabled: boolean; attached: Attached[] }

/** The form of a new service, or of one edited. */
function ServiceForm(props: { s?: Service; onDone: () => void }) {
  const [name, setName] = createSignal(props.s?.name ?? '')
  const [port, setPort] = createSignal(props.s ? String(props.s.port) : '')
  const save = (e: Event) => {
    e.preventDefault()
    const s = { id: props.s?.id ?? '', name: name(), port: Number(port()), enabled: props.s?.enabled ?? true }
    api.rpc('services.save', s).then(props.onDone, errorToast)
  }
  return (
    <form class="svc-form" data-testid="service-form" onSubmit={save}>
      <label>
        {t('Name')}
        <input class="input" value={name()} placeholder="Ollama" onInput={(e) => setName(e.currentTarget.value)} data-testid="service-name" />
      </label>
      <label>
        {t('Port of this machine')}
        <input class="input port" inputmode="numeric" value={port()} placeholder="11434" onInput={(e) => setPort(e.currentTarget.value)} data-testid="service-port" />
      </label>
      <button class="btn small primary" type="submit" disabled={!name().trim() || !port()} data-testid="service-save">
        {props.s ? t('Save') : t('Share')}
      </button>
      <button class="btn small" type="button" onClick={props.onDone}>
        {t('Cancel')}
      </button>
    </form>
  )
}

export function SharedServices() {
  const [list, { mutate }] = createResource(() => api.rpc<Service[]>('services.list'))
  onCleanup(api.on('services.changed', (l: Service[]) => mutate(l)))
  const [adding, setAdding] = createSignal(false)
  const [editing, setEditing] = createSignal('')
  const toggle = (s: Service, on: boolean) => api.rpc('services.save', { ...s, enabled: on }).catch(errorToast)
  const remove = (s: Service) => {
    if (confirm(t('Stop sharing {name}? The pods using it lose it.', { name: s.name }))) api.rpc('services.delete', { id: s.id }).catch(errorToast)
  }
  const cut = (s: Service, a: Attached) => api.rpc('services.cut', { id: s.id, pod: a.key }).catch(errorToast)
  return (
    <div class="home-services" data-testid="home-services">
      <div class="home-bar">
        <h3 class="home-sub">{t('Shared services')}</h3>
        <span class="muted small">{t('HTTP services of this machine the other pods of your account can reach.')}</span>
        <span class="grow" />
        <Show when={!adding()}>
          <button class="btn small" onClick={() => setAdding(true)} data-testid="service-add">
            {t('Share a service')}
          </button>
        </Show>
      </div>
      <Show when={adding()}>
        <ServiceForm onDone={() => setAdding(false)} />
      </Show>
      <Show when={!adding() && list.latest && !list.latest.length}>
        <p class="muted small">{t('Nothing shared. A service you share is reached only by the pods of your account, and only while it is on.')}</p>
      </Show>
      <For each={list.latest}>
        {(s) => (
          <Show when={editing() !== s.id} fallback={<ServiceForm s={s} onDone={() => setEditing('')} />}>
            <div class="svc-row" classList={{ off: !s.enabled }} data-testid={`service-${s.port}`}>
              <div class="svc-head">
                <input type="checkbox" class="ai-switch" checked={s.enabled} title={s.enabled ? t('Shared: turn off') : t('Off: share it')} onChange={(e) => toggle(s, e.currentTarget.checked)} data-testid={`service-on-${s.port}`} />
                <strong class="svc-name">{s.name}</strong>
                <span class="mono muted">127.0.0.1:{s.port}</span>
                <Show when={!s.enabled}>
                  <span class="badge">{t('off')}</span>
                </Show>
                <span class="grow" />
                <button class="icon-btn small" title={t('Edit')} onClick={() => setEditing(s.id)} data-testid={`service-edit-${s.port}`}>
                  <Icon name="edit" size={12} />
                </button>
                <button class="icon-btn small" title={t('Stop sharing')} onClick={() => remove(s)} data-testid={`service-delete-${s.port}`}>
                  <Icon name="close" size={12} />
                </button>
              </div>
              <Show when={s.attached.length}>
                <div class="svc-pods">
                  <For each={s.attached}>
                    {(a) => (
                      <div class="svc-pod" data-testid={`service-pod-${a.name}`}>
                        <span>{a.name}</span>
                        <RouteBadge route={a.route} />
                        <span class="muted small">
                          {t('attached {when}', { when: api.fmtAgo(a.since) })} · {api.tn(a.requests, '{n} request', '{n} requests')} · {api.fmtSize(a.bytes)}
                          <Show when={a.last}> · {t('last request {when}', { when: api.fmtAgo(a.last!) })}</Show>
                        </span>
                        <span class="grow" />
                        <button class="btn small" title={t('Detach this pod: its next request is refused')} onClick={() => cut(s, a)}>
                          {t('Cut')}
                        </button>
                      </div>
                    )}
                  </For>
                </div>
              </Show>
            </div>
          </Show>
        )}
      </For>
    </div>
  )
}
