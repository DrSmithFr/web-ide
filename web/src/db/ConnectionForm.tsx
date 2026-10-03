// Connection editor: list on the left, form on the right (SQLite, Postgres, Redis, optional
// SSH tunnel), with a Test button. Secrets go to the pod, never into .ide.
import { createResource, createSignal, For, Show } from 'solid-js'
import { createStore, unwrap } from 'solid-js/store'
import { request } from '../pod/rpc'
import { Modal } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { connections, type ConnConfig, type Secret } from './api'
import { t } from '../i18n'

const blank = (kind: ConnConfig['kind'] = 'postgres'): ConnConfig => ({
  id: '',
  name: '',
  kind,
  host: kind === 'sqlite' ? '' : '127.0.0.1',
  port: kind === 'postgres' ? 5432 : kind === 'redis' ? 6379 : undefined,
  database: kind === 'postgres' ? 'postgres' : '',
  user: kind === 'postgres' ? 'postgres' : '',
  sslMode: 'prefer',
  redisDb: 0,
  path: '',
  rememberPassword: true,
  ssh: { enabled: false, host: '', port: 22, user: '', auth: 'agent', keyPath: '' },
})

export function ConnectionsModal(props: { initial?: string | 'new'; onClose: () => void }) {
  const first = props.initial === 'new' ? null : connections().find((c) => c.id === props.initial) ?? connections()[0] ?? null
  const [selected, setSelected] = createSignal<string>(first?.id ?? '')
  const [form, setForm] = createStore<ConnConfig>(first ? withDefaults(structuredClone(unwrap(first))) : blank())
  const [secret, setSecret] = createStore<Secret>({})
  const [test, setTest] = createSignal<{ ok: boolean; text: string } | null>(null)
  const [busy, setBusy] = createSignal(false)
  const [sshInfo] = createResource(() => request('ssh.info'))

  function withDefaults(c: ConnConfig): ConnConfig {
    const b = blank(c.kind)
    return { ...b, ...c, ssh: { ...b.ssh!, ...(c.ssh ?? {}), enabled: !!c.ssh?.enabled } }
  }

  const select = (id: string) => {
    const c = connections().find((x) => x.id === id)
    setSelected(id)
    setForm(withDefaults(structuredClone(unwrap(c ?? blank()))))
    setSecret({ password: undefined, sshPassword: undefined, sshPassphrase: undefined })
    setTest(null)
  }
  const fresh = (kind: ConnConfig['kind'] = 'postgres') => {
    setSelected('')
    setForm(blank(kind))
    setSecret({ password: undefined, sshPassword: undefined, sshPassphrase: undefined })
    setTest(null)
  }
  const payload = () => {
    const c = structuredClone(unwrap(form)) as ConnConfig
    if (!c.ssh?.enabled) delete c.ssh
    return { config: c, secret: structuredClone(unwrap(secret)) }
  }
  const runTest = async () => {
    setBusy(true)
    setTest(null)
    try {
      setTest({ ok: true, text: await request('db.test', payload()) })
    } catch (e) {
      setTest({ ok: false, text: (e as Error).message })
    } finally {
      setBusy(false)
    }
  }
  const save = async () => {
    try {
      const v = await request('db.save', payload())
      setSelected(v.id)
      setForm('id', v.id)
      toast(t('Connection “{name}” saved', { name: v.name }), 'ok')
    } catch (e) {
      errorToast(e)
    }
  }
  const duplicate = () => {
    setSelected('')
    setForm({ id: '', name: (form.name || t('connection')) + ` (${t('copy')})` })
  }
  const remove = async () => {
    if (!form.id || !confirm(t('Delete the connection “{name}”?', { name: form.name }))) return
    try {
      await request('db.delete', { id: form.id })
      fresh()
    } catch (e) {
      errorToast(e)
    }
  }

  const tcp = () => form.kind !== 'sqlite'

  return (
    <Modal title={t('Database connections')} onClose={props.onClose} class="modal-wide">
      <div class="conn-editor">
        <aside class="conn-list">
          <For each={connections()}>
            {(c) => (
              <button class="conn-item" classList={{ selected: selected() === c.id }} onClick={() => select(c.id)}>
                <span class={`dot dot-${c.status.state === 'connected' ? 'connected' : c.status.state === 'error' ? 'error' : 'idle'}`} />
                <span class="ellipsis">{c.name}</span>
                <span class="muted small">{c.kind}</span>
              </button>
            )}
          </For>
          <div class="conn-new">
            <span class="muted small">{t('Add:')}</span>
            <button class="btn small" onClick={() => fresh('postgres')}>
              Postgres
            </button>
            <button class="btn small" onClick={() => fresh('sqlite')}>
              SQLite
            </button>
            <button class="btn small" onClick={() => fresh('redis')}>
              Redis
            </button>
          </div>
        </aside>
        <form
          class="form conn-form"
          onSubmit={(e) => {
            e.preventDefault()
            save()
          }}
        >
          <div class="field-row">
            <label class="field grow">
              <span>{t('Name')}</span>
              <input value={form.name} placeholder={t('derived from the host when empty')} onInput={(e) => setForm('name', e.currentTarget.value)} />
            </label>
            <label class="field">
              <span>{t('Type')}</span>
              <select value={form.kind} disabled={!!form.id} onChange={(e) => setForm({ ...blank(e.currentTarget.value as ConnConfig['kind']), name: form.name })}>
                <option value="postgres">PostgreSQL</option>
                <option value="sqlite">SQLite</option>
                <option value="redis">Redis</option>
              </select>
            </label>
          </div>
          <Show when={form.kind === 'sqlite'}>
            <label class="field">
              <span>{t('File (absolute, or relative to the project; on the SSH host for a remote project)')}</span>
              <input value={form.path ?? ''} placeholder="var/data.db" onInput={(e) => setForm('path', e.currentTarget.value)} />
            </label>
          </Show>
          <Show when={tcp()}>
            <div class="field-row">
              <label class="field grow">
                <span>{form.ssh?.enabled ? t('Host (seen from the SSH server)') : t('Host')}</span>
                <input value={form.host ?? ''} onInput={(e) => setForm('host', e.currentTarget.value)} />
              </label>
              <label class="field w-port">
                <span>{t('Port')}</span>
                <input type="number" value={form.port ?? ''} onInput={(e) => setForm('port', parseInt(e.currentTarget.value, 10) || undefined)} />
              </label>
            </div>
            <Show when={form.kind === 'postgres'}>
              <div class="field-row">
                <label class="field grow">
                  <span>{t('Database')}</span>
                  <input value={form.database ?? ''} onInput={(e) => setForm('database', e.currentTarget.value)} />
                </label>
                <label class="field">
                  <span>{t('SSL mode')}</span>
                  <select value={form.sslMode ?? 'prefer'} onChange={(e) => setForm('sslMode', e.currentTarget.value)}>
                    <For each={['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full']}>{(m) => <option value={m}>{m}</option>}</For>
                  </select>
                </label>
              </div>
            </Show>
            <Show when={form.kind === 'redis'}>
              <label class="field w-port">
                <span>{t('Database (index)')}</span>
                <input type="number" min="0" value={form.redisDb ?? 0} onInput={(e) => setForm('redisDb', parseInt(e.currentTarget.value, 10) || 0)} />
              </label>
            </Show>
            <div class="field-row">
              <label class="field grow">
                <span>{t('User')}{form.kind === 'redis' ? ' (ACL, facultatif)' : ''}</span>
                <input value={form.user ?? ''} onInput={(e) => setForm('user', e.currentTarget.value)} />
              </label>
              <label class="field grow">
                <span>{t('Password')}</span>
                <input
                  type="password"
                  autocomplete="new-password"
                  placeholder={connections().find((c) => c.id === form.id)?.hasSecret ? t('•••••• (unchanged)') : ''}
                  value={secret.password ?? ''}
                  onInput={(e) => setSecret('password', e.currentTarget.value)}
                />
              </label>
            </div>
            <label class="check">
              <input type="checkbox" checked={form.rememberPassword} onChange={(e) => setForm('rememberPassword', e.currentTarget.checked)} />
              {t('Remember the password in the pod (otherwise asked at each session)')}
            </label>

            <fieldset class="fieldset">
              <legend>
                <label class="check">
                  <input type="checkbox" checked={!!form.ssh?.enabled} onChange={(e) => setForm('ssh', 'enabled', e.currentTarget.checked)} />
                  {t('SSH tunnel')}
                </label>
              </legend>
              <Show when={form.ssh?.enabled}>
                <div class="field-row">
                  <label class="field grow">
                    <span>{t('SSH host')}</span>
                    <input list="ssh-hosts" value={form.ssh!.host} onInput={(e) => setForm('ssh', 'host', e.currentTarget.value)} />
                    <datalist id="ssh-hosts">
                      <For each={sshInfo()?.hosts ?? []}>{(h: any) => <option value={h.alias} />}</For>
                    </datalist>
                  </label>
                  <label class="field w-port">
                    <span>{t('Port')}</span>
                    <input type="number" value={form.ssh!.port} onInput={(e) => setForm('ssh', 'port', parseInt(e.currentTarget.value, 10) || 22)} />
                  </label>
                  <label class="field grow">
                    <span>{t('User')}</span>
                    <input value={form.ssh!.user} onInput={(e) => setForm('ssh', 'user', e.currentTarget.value)} />
                  </label>
                </div>
                <div class="field-row">
                  <label class="field">
                    <span>{t('Authentication')}</span>
                    <select value={form.ssh!.auth} onChange={(e) => setForm('ssh', 'auth', e.currentTarget.value as any)}>
                      <option value="agent">{t('Existing key set (agent + default keys)')}</option>
                      <option value="key">{t('Dedicated key')}</option>
                      <option value="password">{t('Password')}</option>
                    </select>
                  </label>
                  <Show when={form.ssh!.auth === 'key'}>
                    <label class="field grow">
                      <span>{t('Private key')}</span>
                      <input list="ssh-keys" value={form.ssh!.keyPath ?? ''} onInput={(e) => setForm('ssh', 'keyPath', e.currentTarget.value)} />
                      <datalist id="ssh-keys">
                        <For each={sshInfo()?.keys ?? []}>{(k: string) => <option value={k} />}</For>
                      </datalist>
                    </label>
                    <label class="field grow">
                      <span>{t('Passphrase')}</span>
                      <input type="password" value={secret.sshPassphrase ?? ''} onInput={(e) => setSecret('sshPassphrase', e.currentTarget.value)} />
                    </label>
                  </Show>
                  <Show when={form.ssh!.auth === 'password'}>
                    <label class="field grow">
                      <span>{t('SSH password')}</span>
                      <input type="password" value={secret.sshPassword ?? ''} onInput={(e) => setSecret('sshPassword', e.currentTarget.value)} />
                    </label>
                  </Show>
                </div>
              </Show>
            </fieldset>
          </Show>

          <Show when={test()}>
            <div class={`test-result ${test()!.ok ? 'ok' : 'danger'}`} role="status">
              {test()!.text}
            </div>
          </Show>
          <div class="form-actions">
            <Show when={form.id}>
              <button type="button" class="btn danger" onClick={remove}>
                {t('Delete')}
              </button>
              <button type="button" class="btn" onClick={duplicate}>
                {t('Duplicate')}
              </button>
            </Show>
            <span class="grow" />
            <button type="button" class="btn" disabled={busy()} onClick={runTest}>
              {busy() ? t('Testing…') : t('Test')}
            </button>
            <button type="submit" class="btn primary">
              {t('Save')}
            </button>
          </div>
        </form>
      </div>
    </Modal>
  )
}
