// Settings > Remote access > Local network: the LAN port of the pod, the pods of the account
// it sees, and the addresses typed by hand for a network where mDNS fails.
import { createEffect, createSignal, For, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { useLan, type LanStatus } from '../state/lan'
import { errorToast, toast } from '../ui/toast'
import { t } from '../i18n'

/** The text of a reason an address failed (codes of the handshake, pod/lan). */
function reason(code: string) {
  switch (code) {
    case 'unpaired':
      return t('not paired')
    case 'foreign':
      return t('another account')
    case 'revoked':
      return t('revoked')
    case 'proof':
      return t('the proof does not match')
    case 'version':
      return t('another version: update this pod')
    default:
      return t('no answer')
  }
}

export function LanSection() {
  const [lan, setLan] = useLan()
  const [text, setText] = createSignal('')
  const [edited, setEdited] = createSignal(false)
  createEffect(() => {
    if (!edited()) setText((lan()?.manual ?? []).join('\n'))
  })
  const save = async () => {
    const peers = text()
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean)
    try {
      setLan(await request<LanStatus>('lan.setPeers', { peers }))
      setEdited(false)
      toast(t('Addresses saved'), 'ok')
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <Show when={lan()} fallback={<p class="muted small">{t('The LAN link is off (-lan off).')}</p>}>
      {(st) => (
        <div class="lan-settings" data-testid="lan-settings">
          <dl class="props">
            <dt>{t('LAN port')}</dt>
            <dd class="mono">{st().listen || '—'}</dd>
          </dl>
          <Show when={st().error}>
            <p class="err small">{st().error}</p>
          </Show>
          <Show when={!st().paired}>
            <p class="muted small">{t('Pair this pod (web-ide-pod pair) to find the other pods of the account on the local network.')}</p>
          </Show>
          <Show when={st().peers.length}>
            <ul class="lan-peers">
              <For each={st().peers}>
                {(p) => (
                  <li data-testid="lan-peer">
                    <span class={`dot dot-${p.online ? 'connected' : 'disconnected'}`} /> {p.name}{' '}
                    <span class="muted small">{p.fingerprint}</span> <span class="muted small mono">{p.addr}</span>
                  </li>
                )}
              </For>
            </ul>
          </Show>
          <Show when={st().failed.length}>
            <ul class="lan-peers">
              <For each={st().failed}>
                {(f) => (
                  <li class="muted small" data-testid="lan-failed">
                    <span class="mono">{f.addr}</span> — {reason(f.error)}
                  </li>
                )}
              </For>
            </ul>
          </Show>
          <label class="field">
            <span>{t('Addresses of pods typed by hand (host:port, one per line), when mDNS does not find them')}</span>
            <textarea
              rows="3"
              class="mono"
              value={text()}
              placeholder="192.168.1.20:4436"
              onInput={(e) => {
                setEdited(true)
                setText(e.currentTarget.value)
              }}
              data-testid="lan-manual"
            />
          </label>
          <div class="field-row">
            <button class="btn" onClick={() => void save()} disabled={!edited()} data-testid="lan-save">
              {t('Save')}
            </button>
          </div>
          <p class="muted small">{t('Only the pods of this account are listed: each one proves its certificate, and the traffic between pods is encrypted.')}</p>
        </div>
      )}
    </Show>
  )
}
