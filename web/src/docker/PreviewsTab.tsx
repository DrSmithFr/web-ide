// Previews tab of the Docker tool: the apps served on temporary URLs (share_preview), for
// every project of the pod. A preview stops with its command.
import { For, Show } from 'solid-js'
import { errorToast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { t } from '../i18n'
import { closePreview, previews, setPublic } from '../llm/previews'

export function PreviewsTab() {
  return (
    <div class="dk-disk" data-testid="docker-previews">
      <p class="muted small pad">{t('Apps the assistant offered to try, served on temporary URLs. A preview stops with its command, after 24 hours at most.')}</p>
      <For each={previews()} fallback={<p class="muted small pad">{t('No preview.')}</p>}>
        {(p) => (
          <div class="dk-tunnel" data-testid={`preview-${p.port}`}>
            <span class="dk-dot up" />
            <b class="ellipsis">{p.title}</b>
            <a class="mono ellipsis" href={p.url} target="_blank" rel="noopener" title={t('Open in the browser')}>
              {p.url}
            </a>
            <span class="muted small mono ellipsis">
              {p.command} → :{p.port}
            </span>
            <Show when={p.public}>
              <span class="badge warn" title={p.publicUrl}>
                {t('public')}
              </span>
            </Show>
            <span class="grow" />
            <Show when={p.tailscale}>
              <button class="btn small" onClick={() => setPublic(p.id, !p.public).catch(errorToast)}>
                {p.public ? t('Make private') : t('Make public')}
              </button>
            </Show>
            <button class="icon-btn small" title={t('Stops the command and the preview')} onClick={() => closePreview(p.id).catch(errorToast)} data-testid="preview-close">
              <Icon name="close" size={12} />
            </button>
          </div>
        )}
      </For>
    </div>
  )
}
