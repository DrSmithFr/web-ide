// Settings > Notifications, on the relay origin: push notifications of this device (Web Push
// through the relay, encrypted by the pod), a switch per kind, and a test.
import { createSignal, For, onMount, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { disablePush, enablePush, pushMissing, relayDevice } from '../pod/push'
import { errorToast, toast } from '../ui/toast'
import { t } from '../i18n'

type Status = { available: boolean; subscribed?: boolean; kinds: string[]; choices?: Record<string, boolean>; sub?: string }

const kindLabels: Record<string, string> = {
  finished: 'The agent finished a turn',
  asks: 'The agent asks for an approval or an answer',
  tests: 'Tests failed (a saved command flagged test)',
  feedback: 'Feedback on a ticket from the assistant or Claude Code',
}

export function Notifications() {
  const [status, setStatus] = createSignal<Status | null>(null)
  const [busy, setBusy] = createSignal(false)
  const missing = pushMissing()
  const refresh = async () => setStatus(await request<Status>('push.status', { device: relayDevice() }))
  onMount(() => void refresh().catch(errorToast))

  const run = async (f: () => Promise<unknown>, done?: string) => {
    setBusy(true)
    try {
      await f()
      if (done) toast(t(done), 'ok')
      await refresh()
    } catch (e) {
      errorToast(new Error(t((e as Error).message)))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section class="form" data-testid="notifications">
      <p class="muted small">{t('The pod notifies this device when it shows no window of the pod. The notifications are encrypted by the pod: the relay forwards them without reading them.')}</p>
      <Show when={missing}>
        <p class="err">{t(missing)}</p>
      </Show>
      <Show when={!missing && status() && !status()!.available}>
        <p class="muted">{t('This pod has no relay: push notifications go through it.')}</p>
      </Show>
      <Show when={!missing && status()?.available}>
        <Show
          when={status()?.subscribed}
          fallback={
            <div class="field-row">
              <button class="btn" disabled={busy()} onClick={() => void run(enablePush, 'Notifications enabled on this device.')} data-testid="push-enable">
                {t('Enable on this device')}
              </button>
            </div>
          }
        >
          <div class="checks">
            <For each={status()!.kinds}>
              {(k) => (
                <label class="check">
                  <input type="checkbox" checked={!!status()!.choices?.[k]} data-testid={`push-kind-${k}`}
                    onChange={(e) => void run(() => request('push.settings', { device: relayDevice(), kind: k, on: e.currentTarget.checked }))} />
                  {t(kindLabels[k] ?? k)}
                </label>
              )}
            </For>
          </div>
          <div class="field-row">
            <button class="btn" disabled={busy()} onClick={() => void run(() => request('push.test', { device: relayDevice() }), 'Test notification sent.')} data-testid="push-test">
              {t('Send a test')}
            </button>
            <button class="btn" disabled={busy()} onClick={() => void run(() => disablePush(status()?.sub), 'Notifications disabled on this device.')} data-testid="push-disable">
              {t('Disable on this device')}
            </button>
          </div>
        </Show>
      </Show>
    </section>
  )
}
