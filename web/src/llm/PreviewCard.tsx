// Card of share_preview: the app the model offers to try. Nothing runs before the click;
// then the pod starts the command, waits for its port and the app opens in a new tab.
import { createSignal, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast } from '../ui/toast'
import { copyText } from '../ui/clipboard'
import { t } from '../i18n'
import { mutate, project } from '../state/project'
import { consoles } from '../console/consoles'
import { showTool } from '../state/zones'
import { closePreview, openPreview, previews, sameSpec, setPublic, type PreviewSpec } from './previews'

/** Brings the console of the command to the front, in a window of its project. */
async function showConsole(projectId: string, id: string) {
  if (!id || project()?.id !== projectId) return
  // The console the pod just created may not be listed here yet.
  for (let i = 0; i < 20 && !consoles().some((c) => c.id === id); i++) await new Promise((r) => setTimeout(r, 100))
  if (!consoles().some((c) => c.id === id)) return
  mutate((s) => {
    showTool(s, 'console')
    s.bottom.active = id
  })
}

export function PreviewCard(props: { spec: PreviewSpec }) {
  const [busy, setBusy] = createSignal(false)
  const running = () => previews().find((p) => sameSpec(p, props.spec))
  const open = async () => {
    const p = running()
    if (p) {
      window.open(p.url, '_blank', 'noopener')
      return
    }
    setBusy(true)
    try {
      const p = await openPreview(props.spec)
      void showConsole(p.project, p.console)
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
    }
  }
  const act = (f: () => Promise<unknown>) => () => {
    setBusy(true)
    f()
      .catch(errorToast)
      .finally(() => setBusy(false))
  }
  return (
    <div class="ai-preview" classList={{ running: !!running() }} data-testid="ai-preview">
      <div class="ai-preview-head">
        <Icon name="play" size={13} />
        <b class="ellipsis">{props.spec.title}</b>
        <span class="mono muted small ellipsis">
          {props.spec.command} → :{props.spec.port}
        </span>
      </div>
      <div class="ai-preview-actions">
        <button class="btn small primary" disabled={busy()} onClick={() => void open()} data-testid="ai-preview-open">
          <Show when={busy() && !running()} fallback={<Icon name="external" size={12} />}>
            <span class="spinner" />
          </Show>
          {busy() && !running() ? t('Starting…') : running() ? t('Open the app') : t('Start and open the app')}
        </button>
        <Show when={running()}>
          {(p) => (
            <>
              <a class="mono small ellipsis" href={p().url} target="_blank" rel="noopener" data-testid="ai-preview-url">
                {p().url}
              </a>
              <Show when={p().tailscale}>
                <button
                  class="btn small"
                  disabled={busy()}
                  onClick={act(() => setPublic(p().id, !p().public))}
                  data-testid="ai-preview-public"
                  title={p().public ? t('Only your tailnet reaches it again') : t('Anyone with the link reaches it, through Tailscale Funnel')}
                >
                  <Icon name={p().public ? 'lock' : 'external'} size={12} /> {p().public ? t('Make private') : t('Make public')}
                </button>
              </Show>
              <button class="btn small" disabled={busy()} onClick={act(() => closePreview(p().id))} title={t('Stops the command and the preview')} data-testid="ai-preview-close">
                <Icon name="stop" size={12} /> {t('Stop')}
              </button>
            </>
          )}
        </Show>
      </div>
      <Show when={running()?.publicUrl}>
        {(u) => (
          <div class="ai-preview-public small">
            <span class="badge warn">{t('public')}</span>
            <span class="mono ellipsis">{u()}</span>
            <button class="icon-btn small" title={t('Copy the link')} onClick={() => void copyText(u())}>
              <Icon name="copy" size={12} />
            </button>
          </div>
        )}
      </Show>
      <Show when={running() && !running()!.tailscale}>
        <p class="muted small" data-testid="ai-preview-local">
          {t('Without Tailscale, the preview is only reachable from the machine of the IDE.')}
        </p>
      </Show>
    </div>
  )
}
