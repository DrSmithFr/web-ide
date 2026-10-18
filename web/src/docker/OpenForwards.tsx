// Open port forwards of every project, listed on the home page: they outlive the project windows
// and close 5 minutes after the last window of the pod. The RPC methods keep the name tunnels.*
// (GLOSSARY.md: port forward; a tunnel is the WebRTC connection).
import { createResource, For, onCleanup, Show } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { errorToast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { t } from '../i18n'
import type { Forward } from './ForwardsTab'

type Open = Forward & { project: string; name: string; host: string }

export function OpenForwards() {
  const [list, { refetch }] = createResource(() => request<Open[]>('tunnels.all'))
  onCleanup(onPod('tunnels.changed', () => refetch()))
  const close = (o?: Open) => request(o ? 'tunnels.close' : 'tunnels.closeAll', o ? { project: o.project, id: o.id } : {}).catch(errorToast)
  return (
    <Show when={list.latest?.length}>
      <div class="home-forwards" data-testid="home-forwards">
        <div class="home-bar">
          <h3 class="home-sub">{t('Open port forwards')}</h3>
          <span class="muted small">{t('They close 5 minutes after the last window of the pod.')}</span>
          <span class="grow" />
          <button class="btn small" onClick={() => close()} data-testid="forwards-close-all">
            {t('Close all')}
          </button>
        </div>
        <For each={list.latest}>
          {(o) => (
            <div class="home-forward" data-testid={`home-forward-${o.localPort}`}>
              <span class="mono">
                {o.lan ? '0.0.0.0' : '127.0.0.1'}:{o.localPort}
              </span>
              <span class="muted">←</span>
              <span class="mono">
                {o.remoteHost}:{o.remotePort}
              </span>
              <span class="muted small">
                {t('via {host}', { host: o.host })} · {o.name}
              </span>
              <Show when={o.lan}>
                <span class="badge warn">{t('local network')}</span>
              </Show>
              <span class="grow" />
              <button class="icon-btn small" title={t('Close the port forward')} onClick={() => close(o)}>
                <Icon name="close" size={12} />
              </button>
            </div>
          )}
        </For>
      </div>
    </Show>
  )
}
