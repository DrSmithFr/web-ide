// Settings > Remote access > Pods of the account: the cluster this pod belongs to, which pod of the
// cluster opens the others (the gateway the relay elected), and the switch that makes this pod the
// preferred one.
import { createSignal, onMount, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { errorToast, toast } from '../ui/toast'
import { t } from '../i18n'

interface PodsSelf {
  // "site" on the wire: the cluster (GLOSSARY.md)
  site: string
  gateway: string
  preferred: boolean
}

export function PodsSection() {
  const [self, setSelf] = createSignal<PodsSelf | null>(null)
  const [error, setError] = createSignal('')
  onMount(() => {
    request<PodsSelf>('pods.self').then(setSelf, (e) => setError(e instanceof Error ? e.message : String(e)))
  })
  const setPreferred = async (on: boolean) => {
    try {
      setSelf(await request<PodsSelf>('pods.preferred', { on }))
      toast(t('Saved'), 'ok')
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <>
      <Show when={error()}>
        <p class="muted small">{error()}</p>
      </Show>
      <Show when={self()}>
      {(s) => (
        <div data-testid="pods-settings">
          <dl class="props">
            <dt>{t('Cluster of this pod')}</dt>
            <dd class="mono small">{s().site || '—'}</dd>
            <dt>{t('Gateway of the cluster')}</dt>
            <dd class="mono small">{s().gateway || '—'}</dd>
          </dl>
          <label class="field-row small">
            <input type="checkbox" checked={s().preferred} onChange={(e) => void setPreferred(e.currentTarget.checked)} data-testid="pods-preferred" />
            {t('Preferred gateway: open the other pods of this cluster through this pod')}
          </label>
          <p class="muted small">
            {t('Without a preferred pod, the relay elects the one online the longest, and moves the role when it leaves.')}
          </p>
        </div>
      )}
      </Show>
    </>
  )
}
