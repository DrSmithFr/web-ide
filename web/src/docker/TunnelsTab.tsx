// Tunnels tab of the Docker tool (SSH projects): ports reached from the SSH host made
// available on the machine of the pod, like ssh -L. Kept in .ide/tunnels.json by the pod.
import { createResource, createSignal, For, onCleanup, Show } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { errorToast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { t } from '../i18n'

export interface Tunnel {
  id: string
  remoteHost: string
  remotePort: number
  localPort: number
  lan: boolean
  enabled: boolean
  open: boolean
  error?: string
  conns: number
}

export function TunnelsTab() {
  const [list, { refetch, mutate }] = createResource(() => request<Tunnel[]>('tunnels.get'))
  onCleanup(onPod('tunnels.changed', () => refetch()))
  const [host, setHost] = createSignal('127.0.0.1')
  const [remote, setRemote] = createSignal('')
  const [local, setLocal] = createSignal('')
  const [lan, setLan] = createSignal(false)

  const save = async (spec: Partial<Tunnel>) => {
    try {
      mutate(await request<Tunnel[]>('tunnels.save', spec))
      return true
    } catch (e) {
      errorToast(e)
      refetch()
      return false
    }
  }
  const add = async (e: Event) => {
    e.preventDefault()
    const ok = await save({ remoteHost: host().trim(), remotePort: +remote(), localPort: +local() || +remote(), lan: lan(), enabled: true })
    if (ok) {
      setRemote('')
      setLocal('')
      setLan(false)
    }
  }
  const remove = async (tn: Tunnel) => {
    if (!confirm(t('Delete the tunnel to {remote}?', { remote: `${tn.remoteHost}:${tn.remotePort}` }))) return
    try {
      mutate(await request<Tunnel[]>('tunnels.delete', { id: tn.id }))
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <div class="dk-disk" data-testid="docker-tunnels">
      <p class="muted small pad">{t('Ports reached from the SSH host, made available on this machine (like ssh -L). Enabled tunnels open with the project and close 5 minutes after the last window of the pod.')}</p>
      <For each={list.latest ?? []} fallback={<p class="muted small pad">{list.loading ? t('Loading…') : t('No tunnel.')}</p>}>
        {(tn) => (
          <div class="dk-tunnel" data-testid={`tunnel-${tn.localPort}`}>
            <span class={`dk-dot ${tn.open ? 'up' : tn.error ? 'bad' : 'down'}`} title={tn.open ? t('tunnel|Open') : tn.error ? tn.error : t('Closed')} />
            <a class="mono" href={`http://127.0.0.1:${tn.localPort}`} target="_blank" rel="noreferrer" title={t('Open in the browser')}>
              {tn.lan ? '0.0.0.0' : '127.0.0.1'}:{tn.localPort}
            </a>
            <span class="muted">←</span>
            <span class="mono">
              {tn.remoteHost}:{tn.remotePort}
            </span>
            <Show when={tn.lan}>
              <span class="badge warn" title={t('Any machine of the local network reaches this service, without authentication.')}>
                {t('local network')}
              </span>
            </Show>
            <Show when={tn.conns}>
              <span class="muted small">{t('{n} connection(s)', { n: tn.conns })}</span>
            </Show>
            <Show when={tn.error && !tn.open}>
              <span class="danger small ellipsis" title={tn.error}>
                {tn.error}
              </span>
            </Show>
            <span class="grow" />
            <Show
              when={tn.open}
              fallback={
                <button class="icon-btn small" title={t('Start')} data-action="start" onClick={() => save({ ...tn, enabled: true })}>
                  <Icon name="play" size={12} />
                </button>
              }
            >
              <button class="icon-btn small" title={t('Stop')} data-action="stop" onClick={() => save({ ...tn, enabled: false })}>
                <Icon name="stop" size={12} />
              </button>
            </Show>
            <button class="icon-btn small" title={t('Delete')} data-action="delete" onClick={() => remove(tn)}>
              <Icon name="close" size={12} />
            </button>
          </div>
        )}
      </For>
      <form class="dk-tunnel-form" onSubmit={add} data-testid="tunnel-form">
        <label>
          <span class="muted small">{t('Remote address')}</span>
          <input class="input small mono" value={host()} onInput={(e) => setHost(e.currentTarget.value)} placeholder="127.0.0.1" />
        </label>
        <label>
          <span class="muted small">{t('Remote port')}</span>
          <input class="input small mono" type="number" min="1" max="65535" required value={remote()} onInput={(e) => setRemote(e.currentTarget.value)} data-testid="tunnel-remote" />
        </label>
        <label>
          <span class="muted small">{t('Local port')}</span>
          <input class="input small mono" type="number" min="1" max="65535" value={local()} placeholder={remote() || t('same')} onInput={(e) => setLocal(e.currentTarget.value)} data-testid="tunnel-local" />
        </label>
        <label class="dk-check">
          <input type="checkbox" checked={lan()} onChange={(e) => setLan(e.currentTarget.checked)} data-testid="tunnel-lan" />
          <span class="small">{t('Reachable from the local network')}</span>
        </label>
        <button class="btn small primary" type="submit">
          <Icon name="plus" size={12} /> {t('Add')}
        </button>
        <Show when={lan()}>
          <p class="warn small dk-lan-warn">{t('Any machine of the local network reaches this service, without authentication.')}</p>
        </Show>
      </form>
    </div>
  )
}
