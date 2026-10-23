// The external plugins of Settings > Plugins (docs/design/plugins.md, *Phase 3*): install one
// from its git repository and a version constraint, accept its permissions one by one, update
// it (a new permission is asked again), remove it. Pod-wide: the lock is the pod's.
import { createSignal, For, Show } from 'solid-js'
import { request } from '../pod/rpc'
import type { PluginManifest, PluginState } from '../plugins/types'
import { Modal } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { t } from '../i18n'

/** A version resolved by the pod, with the permissions to accept. */
type Resolved = { url: string; tag: string; version: string; manifest: PluginManifest; grants: string[] }

/** What a permission lets the plugin do, as the dialog says it. */
function grantText(g: string): string {
  if (g.startsWith('core:')) return t('Call {method} of the IDE', { method: g.slice(5) })
  return (
    {
      'files:read': t('Read the files of the project'),
      'files:write': t('Change the files of the project'),
      net: t('Reach the network'),
      exec: t('Run programs'),
    }[g] ?? g
  )
}

/**
 * The grants dialog: each permission is a checkbox, none checked; the plugin gets the ones the
 * user accepted.
 */
function GrantsDialog(props: { title: string; resolved: Resolved; action: string; onClose: () => void; onAccept: (grants: string[]) => Promise<void> }) {
  const [accepted, setAccepted] = createSignal<string[]>([])
  const [busy, setBusy] = createSignal(false)
  const toggle = (g: string, on: boolean) => setAccepted((l) => (on ? [...l, g] : l.filter((x) => x !== g)))
  return (
    <Modal
      title={props.title}
      onClose={props.onClose}
      class="plugin-grants"
      footer={
        <>
          <button onClick={props.onClose}>{t('Cancel')}</button>
          <button
            class="primary"
            data-testid="plugin-grants-accept"
            disabled={busy()}
            onClick={() => {
              setBusy(true)
              props.onAccept(accepted()).then(props.onClose, (e) => (setBusy(false), errorToast(e)))
            }}>
            {props.action}
          </button>
        </>
      }>
      <p>
        <strong>{props.resolved.manifest.name}</strong> {props.resolved.version} <span class="muted small">{props.resolved.url}</span>
      </p>
      <Show when={props.resolved.grants.length} fallback={<p class="muted">{t('It asks no permission.')}</p>}>
        <p class="muted small">{t('It asks these permissions. Accept each one you grant it; it gets nothing else.')}</p>
        <For each={props.resolved.grants}>
          {(g) => (
            <label class="check">
              <input type="checkbox" data-testid={`plugin-grant-${g}`} onChange={(e) => toggle(g, e.currentTarget.checked)} />
              {grantText(g)} <code class="muted small">{g}</code>
            </label>
          )}
        </For>
      </Show>
    </Modal>
  )
}

// The Landlock ABI of the pod (plugins.host): 0, the backends of the external plugins cannot run.
const [landlock, setLandlock] = createSignal<number | undefined>()
let hostAsked = false
const askHost = () => {
  if (hostAsked) return
  hostAsked = true
  void request<{ landlock: number }>('plugins.host').then((r) => setLandlock(r.landlock), () => (hostAsked = false))
}

const [dialog, setDialog] = createSignal<{ title: string; resolved: Resolved; action: string; accept: (grants: string[]) => Promise<void> } | null>(null)

/** The dialog of an install or an update, when one is open. */
export function PluginDialogs() {
  return <Show when={dialog()}>{(d) => <GrantsDialog title={d().title} resolved={d().resolved} action={d().action} onAccept={d().accept} onClose={() => setDialog(null)} />}</Show>
}

/** Resolves a repository, then asks the grants of the version found and installs it. */
async function askInstall(url: string, constraint: string) {
  const r = await request<Resolved>('plugins.resolve', { url, constraint })
  setDialog({
    title: t('Install a plugin'),
    resolved: r,
    action: t('Install'),
    accept: async (grants) => {
      await request('plugins.install', { url, constraint, version: r.version, grants })
      toast(t('{name} {version} installed', { name: r.manifest.name, version: r.version }))
    },
  })
}

/** The form that installs a plugin from its repository. */
export function InstallForm() {
  const [url, setUrl] = createSignal('')
  const [constraint, setConstraint] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  return (
    <form
      class="plugin-install"
      data-testid="plugin-install"
      onSubmit={(e) => {
        e.preventDefault()
        setBusy(true)
        askInstall(url().trim(), constraint().trim())
          .catch(errorToast)
          .finally(() => setBusy(false))
      }}>
      <h3>{t('Install a plugin')}</h3>
      <p class="muted small">{t('From its git repository (https, ssh or a folder of this machine). It is installed on this pod only.')}</p>
      <input placeholder={t('Repository address')} value={url()} onInput={(e) => setUrl(e.currentTarget.value)} data-testid="plugin-install-url" />
      <input placeholder={t('Version (latest)')} value={constraint()} onInput={(e) => setConstraint(e.currentTarget.value)} data-testid="plugin-install-constraint" />
      <button class="primary" type="submit" disabled={busy() || !url().trim()} data-testid="plugin-install-find">
        {t('Find')}
      </button>
    </form>
  )
}

/** The row of an external plugin: its source, version, Update and Remove, or Install. */
export function ExternalRow(props: { s: PluginState }) {
  askHost()
  const ext = () => props.s.external!
  const update = async () => {
    const id = props.s.manifest.id
    const r = await request<Resolved & { upToDate?: boolean }>('plugins.update', { id })
    if (r.upToDate) return toast(t('{name} is up to date', { name: props.s.manifest.name }))
    if (r.manifest) {
      // A version asking a permission not granted yet.
      setDialog({
        title: t('Update {name}', { name: props.s.manifest.name }),
        resolved: r,
        action: t('Update'),
        accept: async (grants) => {
          await request('plugins.update', { id, grants })
          toast(t('{name} {version} installed', { name: r.manifest.name, version: r.version }))
        },
      })
      return
    }
    toast(t('{name} {version} installed', { name: props.s.manifest.name, version: r.version }))
  }
  return (
    <div class="plugin-external" data-testid={`plugin-external-${props.s.manifest.id}`}>
      <div>
        <span class="badge">{t('External')}</span> <span class="muted small">{ext().url}</span>
      </div>
      <Show
        when={ext().installed}
        fallback={
          <p class="err small">
            {t('Not installed on this pod.')}{' '}
            <button onClick={() => void askInstall(ext().url, '').catch(errorToast)}>{t('Install')}</button>
          </p>
        }>
        <Show when={props.s.manifest.backend && landlock() === 0}>
          <p class="err small">{t('Its backend cannot run on this pod: its kernel has no Landlock.')}</p>
        </Show>
        {/* Each permission it asks, granted or not: revoked at once, for its views and its backend. */}
        <For each={props.s.manifest.permissions ?? []}>
          {(g) => (
            <label class="check small">
              <input
                type="checkbox"
                data-testid={`plugin-granted-${props.s.manifest.id}-${g}`}
                checked={ext().grants.includes(g)}
                onChange={(e) => {
                  const on = e.currentTarget.checked
                  const grants = on ? [...ext().grants, g] : ext().grants.filter((x) => x !== g)
                  void request('plugins.grants', { id: props.s.manifest.id, grants }).catch(errorToast)
                }}
              />
              {grantText(g)} <code class="muted small">{g}</code>
            </label>
          )}
        </For>
        <div class="row">
          <button onClick={() => void update().catch(errorToast)} data-testid={`plugin-update-${props.s.manifest.id}`}>
            {t('Update')}
          </button>
          <button
            class="danger"
            data-testid={`plugin-remove-${props.s.manifest.id}`}
            onClick={() => {
              if (confirm(t('Remove {name} from this pod?', { name: props.s.manifest.name }))) void request('plugins.remove', { id: props.s.manifest.id }).catch(errorToast)
            }}>
            {t('Remove')}
          </button>
        </div>
      </Show>
    </div>
  )
}
