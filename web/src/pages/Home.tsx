// Home page: one list of the projects of every pod of the account, the state of those pods,
// creation of local or SSH projects, workspace folders not yet added.
import { base, podPath } from '../app/base'
import { homeBase } from '../app/home'
import { createEffect, createResource, createSignal, For, onCleanup, onMount, Show } from 'solid-js'
import { createStore } from 'solid-js/store'
import { on as onPod, request } from '../pod/rpc'
import { traceBoot } from '../pod/transport'
import { navigate } from '../app/router'
import { Modal } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { copyText } from '../ui/clipboard'
import { Icon } from '../ui/icons'
import { PodStatus } from './ProjectPage'
import { openSettings } from '../settings/SettingsModal'
import { t } from '../i18n'
import { defaultSpec, iconOf, iconURL, type IconSpec, loadIcon, loadIcons, renderIcon, setFavicon } from '../ui/projectIcon'
import { IconEditor } from '../ui/IconEditor'
import { OpenTunnels } from '../docker/OpenTunnels'
import { useLan } from '../state/lan'
import { podOpenable, podURL, usePods, type PodInfo, type PodProject } from '../state/pods'
import { askUpdate, updateOffer, type UpdateAnswer } from '../pod/update'

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
  onMount(() => {
    document.title = `${t('Projects')} · Web IDE`
    setFavicon(null)
  })
  createEffect(() => {
    const list = projects()
    if (list) loadIcons(list)
    // The home page shows its projects: the end of a load through the relay (#75).
    if (list) requestAnimationFrame(() => traceBoot('home'))
  })
  const [iconFor, setIconFor] = createSignal<{ p: Project; spec: IconSpec | null } | null>(null)
  const editIcon = async (p: Project) => {
    try {
      setIconFor({ p, spec: (await loadIcon(p.id)).spec })
    } catch (e) {
      errorToast(e)
    }
  }

  // One list for every pod of the account, the last opened first: the projects of this pod, and
  // those the other pods published (a project never opened, or of a pod that tells no date, last).
  const [pods] = usePods()
  const entries = (): Entry[] => {
    const all: Entry[] = (projects() ?? []).map((p) => ({ pod: null, p, opened: Date.parse(p.openedAt) || 0 }))
    for (const pod of pods() ?? []) for (const p of pod.projects ?? []) all.push({ pod, p, opened: Date.parse(p.openedAt ?? '') || 0 })
    return all.sort((a, b) => b.opened - a.opened || a.p.name.localeCompare(b.p.name))
  }
  // The home page of a pod served under a prefix is the one of the window's entry pod.
  onMount(() => {
    void homeBase().then((to) => {
      if (to !== base) location.replace(to + '/')
    })
  })

  const available = () => {
    const used = new Set((projects() ?? []).filter((p) => p.type === 'local').map((p) => p.path))
    return (dirs()?.dirs ?? []).filter((d: any) => !used.has(d.path))
  }

  const addQuick = async (path: string) => {
    try {
      const p = await request('projects.create', { type: 'local', path, title: '', description: '' })
      if (p.gitError) toast(t('git init failed: {error}', { error: p.gitError }), 'error')
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
        <span class="muted small mono" data-testid="pod-version">{ws()?.version}</span>
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
          <For each={entries()} fallback={<p class="muted">{projects.loading ? t('Loading…') : t('No project. Create one, or add a folder of the workspace below.')}</p>}>
            {(e) =>
              e.pod ? (
                <RemoteCard pod={e.pod} p={e.p as PodProject} />
              ) : (
                <article class="project-card" data-pod={ws()?.name}>
                  <a
                    class="project-open"
                    href={podPath(`/project/${e.p.id}`)}
                    onClick={(ev) => {
                      ev.preventDefault()
                      navigate(`/project/${e.p.id}`)
                    }}
                  >
                    <Show when={iconOf(e.p.id)} fallback={<span class="project-icon" />}>
                      <img class="project-icon" src={iconURL(iconOf(e.p.id))} alt="" data-testid="project-icon" />
                    </Show>
                    <ProjectInfo p={e.p} pod={(pods() ?? []).length ? ws()?.name : undefined} where={where(e.p as Project)} />
                  </a>
                  <div class="project-actions">
                    <span class="muted small">{e.p.openedAt ? new Date(e.p.openedAt).toLocaleDateString() : ''}</span>
                    <button class="icon-btn" title={t('Change the icon')} onClick={() => editIcon(e.p as Project)} data-testid="project-icon-edit">
                      <Icon name="sparkle" />
                    </button>
                    <button class="icon-btn" title={t('Edit')} onClick={() => setEditing(structuredClone(e.p as Project))}>
                      <Icon name="edit" />
                    </button>
                    <button class="icon-btn" title={t('Remove from the list')} onClick={() => remove(e.p as Project)}>
                      <Icon name="close" />
                    </button>
                  </div>
                </article>
              )
            }
          </For>
        </div>
        <Show when={(pods() ?? []).length}>
          <Pods pods={pods()!} name={ws()?.name ?? ''} version={ws()?.version ?? ''} />
        </Show>
        <OpenTunnels />
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
      <Show when={iconFor()}>
        {(f) => <IconEditor id={f().p.id} name={f().p.name} spec={f().spec} onClose={() => setIconFor(null)} />}
      </Show>
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

interface Entry {
  /** The pod that holds the project, null for this pod. */
  pod: PodInfo | null
  p: Project | PodProject
  opened: number
}

const where = (p: Project) => (p.type === 'ssh' ? `${p.ssh?.user ? p.ssh.user + '@' : ''}${p.ssh?.host}:${p.path}` : p.path)

/** The text of a project card: its name, its kind, the pod that holds it (when the account has
 * several), its description and its path. */
function ProjectInfo(props: { p: { name: string; type: string; description?: string }; pod?: string; where: string }) {
  return (
    <div class="project-info">
      <div class="project-title">
        <span class="ellipsis">{props.p.name}</span> <span class="badge">{props.p.type === 'ssh' ? 'SSH' : 'local'}</span>
        <Show when={props.pod}>
          <span class="badge pod-badge" data-testid="project-pod" title={t('Pod')}>
            {props.pod}
          </span>
        </Show>
      </div>
      <Show when={props.p.description}>
        <div class="project-desc">{props.p.description}</div>
      </Show>
      <div class="muted small mono ellipsis">{props.where}</div>
    </div>
  )
}

/** A project of another pod: it opens with the front end of that pod, in this window. */
function RemoteCard(props: { pod: PodInfo; p: PodProject }) {
  const pod = () => props.pod
  const icon = () => props.p.icon || renderIcon(defaultSpec(props.p.id, props.p.name))
  const why = () =>
    pod().mismatch
      ? t('{name} runs another multi-pod protocol: update it', { name: pod().name })
      : !pod().online
        ? t('This pod is offline.')
        : t('Opening the projects of another site comes with the next version')
  const body = () => (
    <>
      <img class="project-icon" src={iconURL(icon())} alt="" />
      <ProjectInfo p={props.p} pod={pod().name} where={props.p.path} />
    </>
  )
  return (
    <article class="project-card" classList={{ offline: !pod().online }} data-pod={pod().name}>
      <Show
        when={pod().online && podOpenable(pod())}
        fallback={
          <div class="project-open" title={why()} data-testid="pod-project">
            {body()}
          </div>
        }
      >
        <a class="project-open" href={podURL(pod(), `/project/${props.p.id}`)} data-testid="pod-project">
          {body()}
        </a>
      </Show>
      <div class="project-actions">
        <span class="muted small">{props.p.openedAt ? new Date(props.p.openedAt).toLocaleDateString() : ''}</span>
      </div>
    </article>
  )
}

/** The pods of the account, one line each, this pod first: what the list cannot say about their
 * projects — offline, gateway, another version (with *Update this pod*), no sync key yet, LAN
 * one-way. */
function Pods(props: { pods: PodInfo[]; name: string; version: string }) {
  const [lan] = useLan()
  const outdated = () => (lan()?.failed ?? []).filter((f) => f.error === 'version')
  const [self] = createResource(() => request<{ key?: string; fingerprint?: string; gateway: string }>('pods.self').catch(() => null))
  const own = (): PodInfo | null => {
    const s = self()
    if (!s?.key) return null
    return { key: s.key, name: props.name, version: props.version, fingerprint: s.fingerprint ?? '', online: true, gateway: s.gateway === s.key }
  }
  return (
    <section class="home-pods">
      <h3 class="home-sub">{t('Pods')}</h3>
      <Show when={own()}>{(o) => <PodRow pod={o()} self />}</Show>
      <For each={props.pods}>{(pod) => <PodRow pod={pod} />}</For>
      <For each={outdated()}>
        {(f) => (
          <p class="muted small" data-testid="lan-outdated">
            {t('The pod at {addr} runs another version: update this pod.', { addr: f.addr })}
          </p>
        )}
      </For>
    </section>
  )
}

function PodRow(props: { pod: PodInfo; self?: boolean }) {
  const p = () => props.pod
  const seen = () => {
    const at = p().seen
    if (!at) return ''
    const d = new Date(at)
    return t('seen {time}', { time: d.toLocaleString() })
  }
  // *Update this pod*: only a pod running a release version has a version to offer, and only a pod
  // the site says runs an older one is worth offering it to (the pod asked does the checking).
  const [offer, setOffer] = createSignal<{ can: boolean; version: string } | null>(null)
  const [answer, setAnswer] = createSignal<UpdateAnswer | null>(null)
  const [busy, setBusy] = createSignal(false)
  const outdated = () => p().mismatch === 'outdated' && !!offer()?.can
  onMount(() => {
    if (p().mismatch) void updateOffer().then(setOffer, () => {})
  })
  const runUpdate = async () => {
    setBusy(true)
    try {
      const ans = await askUpdate(p().key, offer()!.version)
      setAnswer(ans)
      if (ans.status === 'running') toast(t('{name} updates: it comes back in a moment.', { name: p().name }), 'ok')
      else if (ans.status === 'refused') errorToast(new Error(ans.message || t('the pod refused the request')))
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
    }
  }
  const oneway = () =>
    p().online
      ? t('This pod cannot reach {name} on the local network, only {name} reaches this pod: {name} is reached through the relay.', { name: p().name })
      : t('This pod cannot reach {name} on the local network, only {name} reaches this pod (a NAT, WSL2): without the relay, {name} cannot be opened from here.', { name: p().name })
  return (
    <div class="pod-row" classList={{ offline: !p().online }} data-testid={props.self ? 'pod-own' : 'pod-block'} data-name={p().name}>
      <div class="home-bar">
        <span class="pod-dot" classList={{ on: p().online }} />
        <strong>{p().name}</strong>
        <Show when={props.self}>
          <span class="badge pod-badge" data-testid="pod-self">
            {t('this pod')}
          </span>
        </Show>
        <span class="muted small" title={t('Fingerprint of the pod')}>
          {p().fingerprint}
        </span>
        <Show when={p().gateway}>
          <span class="badge" data-testid="pod-gateway">
            {t('gateway')}
          </span>
        </Show>
        <Show when={p().mismatch}>
          <span class="badge" data-testid="pod-mismatch">
            {p().mismatch === 'outdated' ? t('older version') : t('newer version')}
          </span>
        </Show>
        <Show when={p().oneway}>
          <span class="badge" data-testid="pod-oneway" title={oneway()}>
            {t('LAN one-way')}
          </span>
        </Show>
        <Show when={!p().online}>
          <span class="badge">{t('offline')}</span>
        </Show>
        <Show when={p().nokey}>
          <span class="badge" data-testid="pod-nokey" title={t('This pod has not published its projects yet.')}>
            {t('waiting for the sync key')}
          </span>
        </Show>
        <span class="grow" />
        <Show when={!p().online && seen()}>
          <span class="muted small">{seen()}</span>
        </Show>
        <span class="muted small mono">{p().version}</span>
        <Show when={outdated()}>
          <span data-testid="pod-update">
            <button class="btn small" disabled={busy()} onClick={runUpdate}>
              {t('Update this pod')}
            </button>{' '}
            <span class="muted small">{t('to {version}', { version: offer()!.version })}</span>
          </span>
        </Show>
      </div>
      <Show when={p().oneway && !p().online}>
        <p class="muted small">{oneway()}</p>
      </Show>
      <Show when={answer()?.status === 'not_service'}>
        <div data-testid="pod-update-cmd">
          <p class="small">{t('That pod does not run as the service: run this command there, then restart it:')}</p>
          <pre class="mono small">{answer()!.command}</pre>
          <button class="btn small" onClick={() => void copyText(answer()!.command ?? '')}>{t('Copy')}</button>
        </div>
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
  const [remote, setRemote] = createSignal('')
  const [sshInfo] = createResource(() => request('ssh.info'))
  const isNew = !props.initial.id

  const save = async (e: Event) => {
    e.preventDefault()
    const body: any = { id: p.id, title: p.title, description: p.description, type: p.type, path: p.path }
    if (p.type === 'ssh') body.ssh = { ...p.ssh }
    if (isNew) body.remote = remote()
    try {
      const v = await request(isNew ? 'projects.create' : 'projects.update', body)
      if (v.gitError) toast(t('git init failed: {error}', { error: v.gitError }), 'error')
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
        <Show when={isNew}>
          <label class="field">
            <span>{t('Git remote (optional, added as origin)')}</span>
            <input class="mono" value={remote()} placeholder="git@github.com:me/project.git" onInput={(e) => setRemote(e.currentTarget.value)} data-testid="project-remote" />
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
