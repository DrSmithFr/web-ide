// Home page: one list of the projects of every pod of the account, the state of those pods,
// creation of local or SSH projects, workspace folders not yet added.
import { base, podPath } from '../app/base'
import { homeBase } from '../app/home'
import { createEffect, createResource, createSignal, For, onCleanup, onMount, Show } from 'solid-js'
import { createStore } from 'solid-js/store'
import { on as onPod, request } from '../pod/rpc'
import { traceBoot } from '../pod/transport'
import { deskPath, navigate } from '../app/router'
import { lastDesk } from '../state/desks'
import { contextMenu, Modal } from '../ui/overlay'
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
import { askPower, type PowerAction, type PowerAnswer } from '../pod/power'
import { type Call, podCall } from '../pod/remote'

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
  // The project form: a project of this pod (pod null) or of another one, which saves it itself.
  const [editing, setEditing] = createSignal<{ p: Partial<Project>; pod: PodInfo | null } | null>(null)
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
  const [iconFor, setIconFor] = createSignal<{ p: { id: string; name: string }; spec: IconSpec | null; save?: (spec: IconSpec, svg: string) => Promise<void> } | null>(null)
  const editIcon = async (p: Project) => {
    try {
      setIconFor({ p, spec: (await loadIcon(p.id)).spec })
    } catch (e) {
      errorToast(e)
    }
  }
  // The actions on a project of another pod: the request goes to that pod, which checks it as it
  // checks its own windows; its list comes back by pods.changed.
  const remoteEdit = async (pod: PodInfo, id: string) => {
    try {
      const list = await podCall(pod)<Project[]>('projects.list')
      const p = list.find((x) => x.id === id)
      if (!p) throw new Error(t('{name} no longer holds this project', { name: pod.name }))
      setEditing({ p: structuredClone(p), pod })
    } catch (e) {
      errorToast(e)
    }
  }
  const remoteIcon = async (pod: PodInfo, p: PodProject) => {
    const call = podCall(pod)
    try {
      const r = await call<{ spec: IconSpec | null }>('projects.icon', { id: p.id })
      setIconFor({ p, spec: r.spec, save: (spec, svg) => call('projects.icon.save', { id: p.id, spec, svg }) })
    } catch (e) {
      errorToast(e)
    }
  }
  const remoteRemove = async (pod: PodInfo, p: PodProject) => {
    if (!confirm(t('Remove “{name}” from the list of {pod}? The files are not deleted.', { name: p.name, pod: pod.name }))) return
    try {
      await podCall(pod)('projects.delete', { id: p.id })
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
          <button class="btn primary" onClick={() => setEditing({ p: { type: 'local' }, pod: null })}>
            <Icon name="plus" size={14} /> {t('New project')}
          </button>
        </div>
        <div class="project-list">
          <For each={entries()} fallback={<p class="muted">{projects.loading ? t('Loading…') : t('No project. Create one, or add a folder of the workspace below.')}</p>}>
            {(e) =>
              e.pod ? (
                <RemoteCard pod={e.pod} p={e.p as PodProject} onEdit={remoteEdit} onIcon={remoteIcon} onRemove={remoteRemove} />
              ) : (
                <article class="project-card" data-pod={ws()?.name}>
                  <a
                    class="project-open"
                    href={podPath(deskPath(e.p.id, lastDesk(e.p.id)))}
                    onClick={(ev) => {
                      ev.preventDefault()
                      navigate(deskPath(e.p.id, lastDesk(e.p.id)))
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
                    <button class="icon-btn" title={t('Edit')} onClick={() => setEditing({ p: structuredClone(e.p as Project), pod: null })}>
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
        {/* A quiet line, not a button: a plain link, nothing is loaded from GitHub until it is clicked. */}
        <p class="home-sponsor" data-testid="sponsor-line">
          {t('Web IDE is free and open source. If it saves you time, you can')}{' '}
          <a href="https://github.com/sponsors/DrSmithFr" target="_blank" rel="noreferrer" data-testid="sponsor">{t('support it on GitHub')}</a>.
        </p>
      </main>
      <Show when={iconFor()}>
        {(f) => <IconEditor id={f().p.id} name={f().p.name} spec={f().spec} save={f().save} onClose={() => setIconFor(null)} />}
      </Show>
      <Show when={editing()}>
        <ProjectForm
          initial={editing()!.p}
          pod={editing()!.pod}
          pods={(pods() ?? []).filter(reachable)}
          ownName={ws()?.name ?? ''}
          onClose={() => setEditing(null)}
          onSaved={(p, isNew, pod) => {
            setEditing(null)
            if (pod) {
              // A project of another pod opens with the front end of that pod.
              if (isNew) location.assign(podURL(pod, `/project/${p.id}`))
              else toast(t('Project saved on {pod}', { pod: pod.name }), 'ok')
              return
            }
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

/** Another pod can be asked from here: online, and speaking the protocol of this one. */
const reachable = (p: PodInfo) => p.online && podOpenable(p)

/** A project of another pod: it opens with the front end of that pod, in this window, and its
 * actions are requests to that pod. */
function RemoteCard(props: {
  pod: PodInfo
  p: PodProject
  onEdit: (pod: PodInfo, id: string) => void
  onIcon: (pod: PodInfo, p: PodProject) => void
  onRemove: (pod: PodInfo, p: PodProject) => void
}) {
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
        when={reachable(pod())}
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
        <button class="icon-btn" title={reachable(pod()) ? t('Change the icon') : why()} disabled={!reachable(pod())} onClick={() => props.onIcon(pod(), props.p)} data-testid="project-icon-edit">
          <Icon name="sparkle" />
        </button>
        <button class="icon-btn" title={reachable(pod()) ? t('Edit') : why()} disabled={!reachable(pod())} onClick={() => props.onEdit(pod(), props.p.id)} data-testid="project-edit">
          <Icon name="edit" />
        </button>
        <button class="icon-btn" title={reachable(pod()) ? t('Remove from the list') : why()} disabled={!reachable(pod())} onClick={() => props.onRemove(pod(), props.p)} data-testid="project-remove">
          <Icon name="close" />
        </button>
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
  const [self] = createResource(() => request<{ key?: string; fingerprint?: string; gateway: string; power?: boolean }>('pods.self').catch(() => null))
  const own = (): PodInfo | null => {
    const s = self()
    if (!s?.key) return null
    return { key: s.key, name: props.name, version: props.version, fingerprint: s.fingerprint ?? '', online: true, gateway: s.gateway === s.key, power: s.power }
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
  // Power (relay.md, *Power*): Wake for a pod that sleeps and published a card, a menu for one that
  // runs a version that knows the power requests. *Waking…* lasts until the pod is back, or 3 min.
  const [waking, setWaking] = createSignal(false)
  const [refusal, setRefusal] = createSignal<PowerAnswer | null>(null)
  let wakeTimer: ReturnType<typeof setTimeout> | undefined
  createEffect(() => p().online && setWaking(false))
  onCleanup(() => clearTimeout(wakeTimer))
  const runPower = async (action: PowerAction) => {
    setBusy(true)
    setRefusal(null)
    try {
      const ans = await askPower(p().key, action)
      if (ans.status === 'sent') {
        setWaking(true)
        clearTimeout(wakeTimer)
        wakeTimer = setTimeout(() => setWaking(false), 3 * 60_000)
        toast(t('The magic packet left: {name} wakes up.', { name: p().name }), 'ok')
      } else if (ans.status === 'running') {
        const done: Record<string, string> = {
          restart: t('{name} restarts: it comes back in a moment.', { name: p().name }),
          reboot: t('{name} reboots.', { name: p().name }),
          shutdown: t('{name} shuts down.', { name: p().name }),
        }
        toast(done[action], 'ok')
      } else if (ans.status === 'not_service') {
        errorToast(new Error(t('That pod does not run as the service: it cannot restart itself.')))
      } else {
        if (ans.command) setRefusal(ans)
        errorToast(new Error(ans.message ? t(ans.message) : t('the pod refused the request')))
      }
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
    }
  }
  const powerMenu = (e: MouseEvent) => {
    const name = p().name
    const allowed = !!p().power
    const why = allowed ? undefined : t('Run sudo web-ide-pod allow-shutdown on that machine to allow it.')
    const ask = (action: PowerAction, question: string) => () => {
      if (confirm(question)) void runPower(action)
    }
    contextMenu(e, [
      { label: t('Restart the pod'), action: ask('restart', t('Restart the pod {name}?', { name })) },
      { label: t('Reboot'), disabled: !allowed, title: why, action: ask('reboot', t('Reboot the machine of {name}?', { name })) },
      { label: t('Shut down'), danger: true, disabled: !allowed, title: why, action: ask('shutdown', t('Shut down the machine of {name}? Waking it needs Wake-on-LAN and an awake pod on its network.', { name })) },
    ])
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
        <Show when={!p().online && p().wake}>
          <button class="btn small" disabled={busy() || waking()} onClick={() => void runPower('wake')} data-testid="pod-wake">
            {waking() ? t('Waking…') : t('Wake')}
          </button>
        </Show>
        <Show when={p().online && p().power !== undefined}>
          <button class="icon-btn" title={t('Power')} disabled={busy()} onClick={powerMenu} data-testid="pod-power">
            <Icon name="power" />
          </button>
        </Show>
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
      <Show when={refusal()}>
        <div data-testid="pod-power-cmd">
          <p class="small">{t('That machine does not let its pod power it off: run this command on it:')}</p>
          <pre class="mono small">{refusal()!.command}</pre>
          <button class="btn small" onClick={() => void copyText(refusal()!.command ?? '')}>{t('Copy')}</button>
        </div>
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

/** The form of a project: saved by the pod that holds it. A new project picks its pod among this
 * one and the pods it can ask (pods); the folder, the workspace and the SSH hosts are that pod's. */
function ProjectForm(props: {
  initial: Partial<Project>
  pod: PodInfo | null
  pods: PodInfo[]
  ownName: string
  onClose: () => void
  onSaved: (p: Project, isNew: boolean, pod: PodInfo | null) => void
}) {
  const isNew = !props.initial.id
  const [target, setTarget] = createSignal<PodInfo | null>(props.pod)
  const call = (): Call => {
    const pod = target()
    return pod ? podCall(pod) : request
  }
  // Keyed by its source: a pod chosen, then null for this pod, must fetch again.
  const [ws] = createResource(() => ({ pod: target() }), () => call()<{ workspace: string }>('workspace.get').catch(() => ({ workspace: '' })))
  return (
    <Modal title={isNew ? t('New project') : t('Edit · {name}', { name: props.initial.name ?? '' })} onClose={props.onClose}>
      <Show when={isNew && props.pods.length}>
        <label class="field">
          <span>{t('Pod')}</span>
          <select data-testid="project-pod-select" value={target()?.key ?? ''} onChange={(e) => setTarget(props.pods.find((p) => p.key === e.currentTarget.value) ?? null)}>
            <option value="">{t('{name} (this pod)', { name: props.ownName })}</option>
            <For each={props.pods}>{(p) => <option value={p.key}>{p.name}</option>}</For>
          </select>
        </label>
      </Show>
      {/* Everything below is the chosen pod's: a new form once its workspace is known. */}
      <Show when={!ws.loading && ws()} keyed>
        {(w) => (
          <ProjectFields
            initial={isNew ? { ...props.initial, path: w.workspace ? w.workspace + '/' : '' } : props.initial}
            isNew={isNew}
            workspace={w.workspace}
            call={call()}
            onClose={props.onClose}
            onSaved={(p) => props.onSaved(p, isNew, target())}
          />
        )}
      </Show>
    </Modal>
  )
}

function ProjectFields(props: { initial: Partial<Project>; isNew: boolean; workspace: string; call: Call; onClose: () => void; onSaved: (p: Project) => void }) {
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
  const [sshInfo] = createResource(() => props.call('ssh.info'))
  const isNew = props.isNew

  const save = async (e: Event) => {
    e.preventDefault()
    const body: any = { id: p.id, title: p.title, description: p.description, type: p.type, path: p.path }
    if (p.type === 'ssh') body.ssh = { ...p.ssh }
    if (isNew) body.remote = remote()
    try {
      const v = await props.call(isNew ? 'projects.create' : 'projects.update', body)
      if (v.gitError) toast(t('git init failed: {error}', { error: v.gitError }), 'error')
      props.onSaved(v)
    } catch (err) {
      errorToast(err)
    }
  }

  return (
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
          <FolderBrowser start={p.path.replace(/\/$/, '') || props.workspace} call={props.call} onPick={(d) => setP('path', d)} />
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
  )
}

function FolderBrowser(props: { start: string; call: Call; onPick: (dir: string) => void }) {
  const [dir, setDir] = createSignal(props.start)
  const [data] = createResource(dir, (d) => props.call('workspace.dirs', { path: d }).catch((e) => ({ error: (e as Error).message })))
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
