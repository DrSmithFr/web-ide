// Settings > Remote access > Sync: what this pod syncs, the state of each kind, the vault switch,
// and the conflicts the user must resolve. The pods do the merge and the relay only carries
// ciphertext, so a conflict is answered here, by the pod that owns the setting; the choice is a
// normal write, recorded in the history like any other change.
import { For, Show, createSignal, onMount } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { errorToast, toast } from '../ui/toast'
import { fmtDate, t } from '../i18n'

interface Conflict {
  path: string
  local: any
  remote: any
  dev: string
  rev: number
}

interface SyncStatus {
  kind: string
  enabled: boolean
  hasKey: boolean
  epoch: number
  seq: number
  lastSync: number
  device: string
  error?: string
  conflicts: Conflict[] | null
}

/** A value of a conflict, shown as the user reads it. */
function shown(v: any) {
  return typeof v === 'string' ? v : JSON.stringify(v)
}

export function SyncPanel() {
  const [status, setStatus] = createSignal<SyncStatus[]>([])

  const put = (s: SyncStatus) => setStatus((list) => [...list.filter((x) => x.kind !== s.kind), s])

  async function refresh() {
    try {
      setStatus(await request<SyncStatus[]>('sync.status'))
    } catch (e) {
      errorToast(e)
    }
  }

  async function choose(kind: string, path: string, value: any) {
    try {
      put(await request<SyncStatus>('sync.resolve', { kind, path, value }))
      toast(t('Choice recorded: {path}', { path }), 'ok')
    } catch (e) {
      errorToast(e)
    }
  }

  async function setKind(kind: string, on: boolean) {
    try {
      put(await request<SyncStatus>('sync.switch', { kind, on }))
    } catch (e) {
      errorToast(e)
    }
  }

  onMount(() => {
    void refresh()
    onPod('sync.conflict', (d: { status: SyncStatus; payload: Conflict }) => {
      put(d.status)
      errorToast(t('{path} changed on {member} too. Choose the value to keep.', { path: d.payload.path, member: d.payload.dev }))
    })
    onPod('sync.changed', (d: { status: SyncStatus }) => put(d.status))
    onPod('sync.error', (d: { status: SyncStatus; payload: string }) => {
      put(d.status)
      errorToast(d.payload)
    })
  })

  return (
    <section class="form" data-testid="sync">
      <For each={status()}>
        {(s) => (
          <>
            <label class="check">
              <input type="checkbox" checked={s.enabled} onChange={(e) => void setKind(s.kind, e.currentTarget.checked)} />
              {s.kind === 'vault' ? t('Sync the vault with the other pods of the account') : t('Sync the settings with the other pods of the account')}
            </label>
            <dl class="props">
              <dt>{t('State')}</dt>
              <dd class="small">
                {!s.enabled ? t('off') : s.hasKey ? t('synced') : t('no sync key yet')}
                <Show when={s.error}>
                  <span class="err small"> · {t(s.error ?? '')}</span>
                </Show>
              </dd>
              <dt>{t('Last sync')}</dt>
              <dd class="small">{s.lastSync ? fmtDate(s.lastSync * 1000) : t('never')}</dd>
              <dt>{t('Epoch')}</dt>
              <dd class="mono small">{s.epoch}</dd>
              <dt>{t('Revision')}</dt>
              <dd class="mono small">{s.seq}</dd>
            </dl>

            <Show when={(s.conflicts ?? []).length}>
              <h3>{t('Conflicts')}</h3>
              <table class="rules">
                <thead>
                  <tr>
                    <th>{t('Field')}</th>
                    <th>{t('Here')}</th>
                    <th>{t('On the other pod')}</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  <For each={s.conflicts}>
                    {(c) => (
                      <tr>
                        <td class="mono small">{c.path}</td>
                        <td class="mono small">{shown(c.local)}</td>
                        <td class="mono small">
                          {shown(c.remote)} <span class="muted">{c.dev}</span>
                        </td>
                        <td>
                          <button class="btn small" onClick={() => void choose(s.kind, c.path, c.local)}>{t('Keep')}</button>
                          <button class="btn small" onClick={() => void choose(s.kind, c.path, c.remote)}>{t('Take the other')}</button>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </Show>
          </>
        )}
      </For>
      <p class="muted small">
        {t('The settings are merged field by field; only a field changed on two pods at once asks. The layout and the sizes stay per device.')}
      </p>
    </section>
  )
}
