// Home page: project list, creation of local or SSH projects, workspace folders not yet added.
import { createResource, createSignal, For, onCleanup, onMount, Show } from 'solid-js'
import { createStore } from 'solid-js/store'
import { on as onPod, request } from '../pod/rpc'
import { navigate } from '../app/router'
import { Modal } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { PodStatus } from './ProjectPage'
import { openSettings } from '../settings/SettingsModal'
import { t } from '../i18n'

interface Project {
  id: string
  name: string
  title: string
  description: string
  type: 'local' | 'ssh'
  path: string
  ssh?: { host: string; port: number; user: string; auth: 'agent' | 'key' | 'password'; keyPath?: string }
  openedAt: string
}

export function Home() {
  const [projects, { refetch }] = createResource(() => request<Project[]>('projects.list'))
  const [ws, { refetch: refetchWs }] = createResource(() => request('workspace.get'))
  const [dirs, { refetch: refetchDirs }] = createResource(() => request('workspace.dirs', {}).catch(() => null))
  const [editing, setEditing] = createSignal<Partial<Project> | null>(null)
  const off = onPod('projects.changed', () => {
    refetch()
    refetchDirs()
  })
  onCleanup(off)
  onMount(() => (document.title = `${t('Projects')} · Web IDE`))

  const available = () => {
    const used = new Set((projects() ?? []).filter((p) => p.type === 'local').map((p) => p.path))
    return (dirs()?.dirs ?? []).filter((d: any) => !used.has(d.path))
  }

  const addQuick = async (path: string) => {
    try {
      const p = await request('projects.create', { type: 'local', path, title: '', description: '' })
      navigate(`/project/${p.id}`)
    } catch (e) {
      errorToast(e)
    }
  }
  const remove = async (p: Project) => {
    if (!confirm(t('Remove “{name}” from the list? The files are not deleted.', { name: p.name }))) return
    try {
      await request('projects.delete', { id: p.id })
    } catch (e) {
      errorToast(e)
    }
  }

  return (
    <div class="home">
      <header class="home-head">
        <h1>Web IDE</h1>
        <span class="grow" />
        <button class="icon-btn" title={t('Settings')} onClick={() => openSettings('workspace')}>
          <Icon name="gear" />
        </button>
        <PodStatus />
      </header>
      <main class="home-main">
        <div class="home-bar">
          <h2>{t('Projects')}</h2>
          <span class="muted small">
            {t('Workspace:')} <span class="mono">{ws()?.workspace}</span>
          </span>
          <span class="grow" />
          <button class="btn primary" onClick={() => setEditing({ type: 'local', path: ws()?.workspace ? ws().workspace + '/' : '' })}>
            <Icon name="plus" size={14} /> {t('New project')}
          </button>
        </div>
        <div class="project-list">
          <For each={projects() ?? []} fallback={<p class="muted">{projects.loading ? t('Loading…') : t('No project. Create one, or add a folder of the workspace below.')}</p>}>
            {(p) => (
              <article class="project-card">
                <a
                  class="project-open"
                  href={`/project/${p.id}`}
                  onClick={(e) => {
                    e.preventDefault()
                    navigate(`/project/${p.id}`)
                  }}
                >
                  <div class="project-title">
                    {p.name} <span class="badge">{p.type === 'ssh' ? 'SSH' : 'local'}</span>
                  </div>
                  <Show when={p.description}>
                    <div class="project-desc">{p.description}</div>
                  </Show>
                  <div class="muted small mono ellipsis">{p.type === 'ssh' ? `${p.ssh?.user ? p.ssh.user + '@' : ''}${p.ssh?.host}:${p.path}` : p.path}</div>
                </a>
                <div class="project-actions">
                  <span class="muted small">{p.openedAt ? new Date(p.openedAt).toLocaleDateString() : ''}</span>
                  <button class="icon-btn" title={t('Edit')} onClick={() => setEditing(structuredClone(p))}>
                    <Icon name="edit" />
                  </button>
                  <button class="icon-btn" title={t('Remove from the list')} onClick={() => remove(p)}>
                    <Icon name="close" />
                  </button>
                </div>
              </article>
            )}
          </For>
        </div>
        <Show when={available().length}>
          <h3 class="home-sub">{t('In the workspace')}</h3>
          <div class="ws-dirs">
            <For each={available()}>
              {(d: any) => (
                <button class="ws-dir" onClick={() => addQuick(d.path)} title={t('Add {path}', { path: d.path })}>
                  <Icon name="folder" size={14} /> {d.name}
                  <Icon name="plus" size={12} />
                </button>
              )}
            </For>
          </div>
        </Show>
      </main>
      <Show when={editing()}>
        <ProjectForm
          initial={editing()!}
          workspace={ws()?.workspace ?? ''}
          onClose={() => setEditing(null)}
          onSaved={(p, isNew) => {
            setEditing(null)
            refetch()
            refetchWs()
            if (isNew) navigate(`/project/${p.id}`)
            else toast(t('Project saved'), 'ok')
          }}
        />
      </Show>
    </div>
  )
}

function ProjectForm(props: { initial: Partial<Project>; workspace: string; onClose: () => void; onSaved: (p: Project, isNew: boolean) => void }) {
  const [p, setP] = createStore<Project>({
    id: '',
    name: '',
    title: '',
    description: '',
    type: 'local',
    path: '',
    openedAt: '',
    ...props.initial,
    ssh: { host: '', port: 22, user: '', auth: 'agent', keyPath: '', ...(props.initial.ssh ?? {}) },
  } as Project)
  const [browse, setBrowse] = createSignal(false)
  const [sshInfo] = createResource(() => request('ssh.info'))
  const isNew = !props.initial.id

  const save = async (e: Event) => {
    e.preventDefault()
    const body: any = { id: p.id, title: p.title, description: p.description, type: p.type, path: p.path }
    if (p.type === 'ssh') body.ssh = { ...p.ssh }
    try {
      const v = await request(isNew ? 'projects.create' : 'projects.update', body)
      props.onSaved(v, isNew)
    } catch (err) {
      errorToast(err)
    }
  }

  return (
    <Modal title={isNew ? t('New project') : t('Edit · {name}', { name: props.initial.name ?? '' })} onClose={props.onClose}>
      <form class="form" onSubmit={save}>
        <div class="segmented" role="radiogroup">
          <button type="button" role="radio" aria-checked={p.type === 'local'} classList={{ on: p.type === 'local' }} onClick={() => setP('type', 'local')}>
            {t('Local')}
          </button>
          <button type="button" role="radio" aria-checked={p.type === 'ssh'} classList={{ on: p.type === 'ssh' }} onClick={() => setP({ type: 'ssh', path: p.type === 'ssh' ? p.path : '~' })}>
            SSH
          </button>
        </div>
        <Show when={p.type === 'local'}>
          <label class="field">
            <span>{t('Folder')}</span>
            <div class="field-row">
              <input class="grow mono" value={p.path} placeholder={props.workspace + '/my-project'} onInput={(e) => setP('path', e.currentTarget.value)} required />
              <button type="button" class="btn" onClick={() => setBrowse(!browse())}>
                {t('Browse')}
              </button>
            </div>
          </label>
          <Show when={browse()}>
            <FolderBrowser start={p.path.replace(/\/$/, '') || props.workspace} onPick={(d) => setP('path', d)} />
          </Show>
        </Show>
        <Show when={p.type === 'ssh'}>
          <div class="field-row">
            <label class="field grow">
              <span>{t('Host (or alias of ~/.ssh/config)')}</span>
              <input list="home-ssh-hosts" value={p.ssh!.host} onInput={(e) => setP('ssh', 'host', e.currentTarget.value)} required />
              <datalist id="home-ssh-hosts">
                <For each={sshInfo()?.hosts ?? []}>{(h: any) => <option value={h.alias}>{h.hostName}</option>}</For>
              </datalist>
            </label>
            <label class="field w-port">
              <span>{t('Port')}</span>
              <input type="number" value={p.ssh!.port} onInput={(e) => setP('ssh', 'port', parseInt(e.currentTarget.value, 10) || 22)} />
            </label>
          </div>
          <div class="field-row">
            <label class="field grow">
              <span>{t('User')}</span>
              <input value={p.ssh!.user} placeholder={t('local user by default')} onInput={(e) => setP('ssh', 'user', e.currentTarget.value)} />
            </label>
            <label class="field">
              <span>{t('Authentication')}</span>
              <select value={p.ssh!.auth} onChange={(e) => setP('ssh', 'auth', e.currentTarget.value as any)}>
                <option value="agent">{t('Agent + default keys')}</option>
                <option value="key">{t('Dedicated key')}</option>
                <option value="password">{t('Password (asked when opening)')}</option>
              </select>
            </label>
          </div>
          <Show when={p.ssh!.auth === 'key'}>
            <label class="field">
              <span>{t('Private key')}</span>
              <input list="home-ssh-keys" class="mono" value={p.ssh!.keyPath ?? ''} onInput={(e) => setP('ssh', 'keyPath', e.currentTarget.value)} />
              <datalist id="home-ssh-keys">
                <For each={sshInfo()?.keys ?? []}>{(k: string) => <option value={k} />}</For>
              </datalist>
            </label>
          </Show>
          <label class="field">
            <span>{t('Remote folder (absolute, or relative to home)')}</span>
            <input class="mono" value={p.path} onInput={(e) => setP('path', e.currentTarget.value)} />
          </label>
        </Show>
        <label class="field">
          <span>{t('Title (optional: derived from the path or the host)')}</span>
          <input value={p.title} onInput={(e) => setP('title', e.currentTarget.value)} />
        </label>
        <label class="field">
          <span>{t('Description (optional)')}</span>
          <textarea rows="2" value={p.description} onInput={(e) => setP('description', e.currentTarget.value)} />
        </label>
        <div class="form-actions">
          <button type="button" class="btn" onClick={props.onClose}>
            {t('Cancel')}
          </button>
          <button type="submit" class="btn primary">
            {isNew ? t('Create and open') : t('Save')}
          </button>
        </div>
      </form>
    </Modal>
  )
}

function FolderBrowser(props: { start: string; onPick: (dir: string) => void }) {
  const [dir, setDir] = createSignal(props.start)
  const [data] = createResource(dir, (d) => request('workspace.dirs', { path: d }).catch((e) => ({ error: (e as Error).message })))
  return (
    <div class="folder-browser">
      <div class="folder-path">
        <button type="button" class="icon-btn" title={t('Parent folder')} onClick={() => setDir(data()?.parent ?? dir())}>
          ↑
        </button>
        <span class="mono small ellipsis">{data()?.path ?? dir()}</span>
        <span class="grow" />
        <button type="button" class="btn small" onClick={() => props.onPick(data()?.path ?? dir())}>
          {t('Choose this folder')}
        </button>
      </div>
      <Show when={data()?.error}>
        <p class="danger small">{data()!.error}</p>
      </Show>
      <div class="folder-list">
        <For each={data()?.dirs ?? []}>
          {(d: any) => (
            <button type="button" class="folder-item" onClick={() => props.onPick(d.path)} onDblClick={() => setDir(d.path)}>
              <Icon name="folder" size={14} /> {d.name}
            </button>
          )}
        </For>
      </div>
      <p class="muted small">{t('Click: choose · double-click: open the folder')}</p>
    </div>
  )
}
