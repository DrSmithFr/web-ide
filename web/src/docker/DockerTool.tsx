// Docker tool: Project tab (Compose stack of the project), Host tab (every container), and a
// detail pane beside the list. Lists and statistics are refreshed while the tool is shown.
import { For, onCleanup, onMount, Show, type JSX } from 'solid-js'
import { mutate, session } from '../state/project'
import { contextMenu, prompt, type MenuItem } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { t } from '../i18n'
import {
  busy, composeAction, composeTask, containerAction, host, portLabel, profiles, refreshHost, refreshStack, refreshStats, refreshStatus, selected, setSelected, shell,
  stack, stats, status, type Container,
} from './state'
import { Detail } from './Detail'
import './docker.css'

type Tab = 'project' | 'host'

export function DockerTool() {
  const tab = () => (session.docker.tab === 'host' ? 'host' : 'project') as Tab
  const setTab = (id: Tab) => mutate((s) => (s.docker.tab = id))

  // One refresh at a time: docker stats takes about a second.
  let timer: number | undefined
  let alive = true
  const tick = async () => {
    if (document.visibilityState === 'visible') {
      if (!status()?.available) await refreshStatus()
      if (tab() === 'project') {
        await refreshStack()
        await refreshStats(stack()?.stack?.containers ?? [])
      } else {
        await refreshHost()
        await refreshStats(host()?.list ?? [])
      }
    }
    if (alive) timer = window.setTimeout(tick, 2500)
  }
  onMount(async () => {
    await refreshStatus()
    void tick()
  })
  onCleanup(() => {
    alive = false
    clearTimeout(timer)
  })
  const switchTab = (id: Tab) => {
    setTab(id)
    setSelected(null)
    clearTimeout(timer)
    void tick()
  }

  return (
    <div class="panel docker-tool">
      <div class="tabbar tool-tabbar" role="tablist">
        <div class="tab" role="tab" data-testid="docker-tab-project" aria-selected={tab() === 'project'} classList={{ active: tab() === 'project' }} onClick={() => switchTab('project')}>
          <span class="tab-title">{t('docker|Project')}</span>
        </div>
        <div class="tab" role="tab" data-testid="docker-tab-host" aria-selected={tab() === 'host'} classList={{ active: tab() === 'host' }} onClick={() => switchTab('host')}>
          <span class="tab-title">{t('Host')}</span>
        </div>
      </div>
      <div class="tool-body">
        <Show when={status()} fallback={<p class="muted pad">{t('Loading…')}</p>}>
          <Show when={status()!.available} fallback={<Unavailable />}>
            <div class="dk-main">
              <div class="dk-list">
                <Show when={tab() === 'project'} fallback={<HostList />}>
                  <ProjectList />
                </Show>
              </div>
              <Show when={selected()}>
                <Detail />
              </Show>
            </div>
          </Show>
        </Show>
      </div>
    </div>
  )
}

function Unavailable() {
  return (
    <div class="pad dk-message">
      <p class="danger mono small">{status()!.error}</p>
      <p class="muted small">{t('Docker cannot be reached. Check that it is installed and running, and that your user may use it (member of the docker group).')}</p>
      <button class="btn small" onClick={() => refreshStatus()}>
        {t('Retry')}
      </button>
    </div>
  )
}

// ---------- rows ----------

function stateClass(c: Container | undefined) {
  if (!c) return 'none'
  if (c.health === 'unhealthy') return 'bad'
  return c.state === 'running' ? (c.health === 'starting' ? 'starting' : 'up') : c.state === 'restarting' || c.state === 'paused' ? 'starting' : c.state === 'dead' ? 'bad' : 'down'
}

function Row(props: {
  name: JSX.Element
  title?: string
  c?: Container
  /** State dot of a row without container (the stack). */
  dot?: string
  busyKey: string
  selected: boolean
  onSelect: () => void
  menu: () => MenuItem[]
  running: boolean
  onStart: () => void
  onStop: () => void
  onRestart: () => void
  testid?: string
}) {
  const st = () => (props.c ? stats()[props.c.id] : undefined)
  return (
    <div
      class="dk-row"
      classList={{ selected: props.selected }}
      data-testid={props.testid}
      title={props.title}
      onClick={props.onSelect}
      onContextMenu={(e) => contextMenu(e, props.menu())}
    >
      <span class={`dk-dot ${props.dot ?? stateClass(props.c)}`} title={props.c ? props.c.status : props.dot ? '' : t('not created')} />
      <span class="dk-name ellipsis">{props.name}</span>
      <span class="dk-image ellipsis muted">{props.c?.image ?? ''}</span>
      <span class="dk-ports ellipsis">
        <For each={props.c?.ports.filter((p) => p.published) ?? []}>{(p) => <span class="dk-port">{portLabel(p)}</span>}</For>
      </span>
      <span class="dk-num muted" title={t('CPU')}>
        {st()?.cpu ?? ''}
      </span>
      <span class="dk-num muted" title={t('Memory')}>
        {st()?.mem.split(' / ')[0] ?? ''}
      </span>
      <span class="dk-actions" onClick={(e) => e.stopPropagation()}>
        <Show when={busy().has(props.busyKey)} fallback={
          <>
            <Show when={props.running} fallback={
              <button class="icon-btn small" title={t('Start')} data-action="start" onClick={props.onStart}>
                <Icon name="play" size={12} />
              </button>
            }>
              <button class="icon-btn small" title={t('Stop')} data-action="stop" onClick={props.onStop}>
                <Icon name="stop" size={12} />
              </button>
            </Show>
            <button class="icon-btn small" title={t('Restart')} data-action="restart" disabled={!props.c && !props.dot} onClick={props.onRestart}>
              <Icon name="refresh" size={12} />
            </button>
          </>
        }>
          <span class="spinner" />
        </Show>
        <button class="icon-btn small" title={t('More actions')} onClick={(e) => contextMenu(e, props.menu())}>
          ⋯
        </button>
      </span>
    </div>
  )
}

const isSel = (id: string) => {
  const s = selected()
  return s?.kind === 'container' && s.id === id
}

function containerItems(c: Container): MenuItem[] {
  return [
    { label: t('Logs'), action: () => openDetail(c.id, 'logs') },
    { label: t('Shell'), action: () => shell(c), disabled: c.state !== 'running' },
    { label: t('Shell as root'), action: () => shell(c, true), disabled: c.state !== 'running' },
  ]
}

function openDetail(id: string, tab: 'infos' | 'logs') {
  setSelected({ kind: 'container', id })
  mutate((s) => (s.docker.detail = tab))
}

// ---------- Project tab ----------

function ProjectList() {
  const st = () => status()!
  const stk = () => stack()?.stack
  const running = () => stk()?.containers.filter((c) => c.state === 'running').length ?? 0
  const rows = () => {
    const s = stk()
    if (!s) return []
    const services = [...new Set([...s.services, ...s.containers.map((c) => c.service ?? '')])].filter(Boolean).sort()
    return services.flatMap((svc) => {
      const cs = s.containers.filter((c) => c.service === svc)
      return cs.length ? cs.map((c) => ({ svc, c: c as Container | undefined })) : [{ svc, c: undefined }]
    })
  }
  const serviceItems = (svc: string, c?: Container): MenuItem[] => [
    { label: t('Start'), action: () => composeAction('start', svc) },
    { label: t('Stop'), action: () => composeAction('stop', svc), disabled: !c },
    { label: t('Restart'), action: () => composeAction('restart', svc), disabled: !c },
    { separator: true, label: '' },
    { label: t('Recreate'), action: () => composeTask('recreate', svc) },
    { label: t('Rebuild'), action: () => composeTask('rebuild', svc) },
    { label: t('Pull'), action: () => composeTask('pull', svc) },
    ...(c ? [{ separator: true, label: '' }, ...containerItems(c)] : []),
  ]
  const stackItems = (): MenuItem[] => [
    { label: t('Start'), action: () => composeAction('start') },
    { label: t('Stop'), action: () => composeAction('stop') },
    { label: t('Restart'), action: () => composeAction('restart') },
    { separator: true, label: '' },
    { label: t('Recreate'), action: () => composeTask('recreate') },
    { label: t('Rebuild'), action: () => composeTask('rebuild') },
    { label: t('Pull'), action: () => composeTask('pull') },
    { separator: true, label: '' },
    { label: t('Logs'), action: () => (setSelected({ kind: 'stack' }), mutate((s) => (s.docker.detail = 'logs'))) },
    { separator: true, label: '' },
    { label: t('docker|Down'), danger: true, action: down },
    { label: t('Down with volumes…'), danger: true, action: downVolumes },
  ]
  const down = () => {
    if (confirm(t('Stop and remove the containers and networks of {name}?', { name: stk()?.name ?? '' }))) void composeTask('down')
  }
  const downVolumes = async () => {
    const name = stk()?.name ?? ''
    const v = await prompt({
      title: t('Down with volumes'),
      label: t('The containers, networks and volumes of {name} are removed: their data is lost. Type {name} to confirm.', { name }),
    })
    if (v === null) return
    if (v.trim() !== name) return void alert(t('The name does not match: nothing was removed.'))
    void composeTask('downVolumes')
  }
  const chooseProfiles = (e: MouseEvent) =>
    contextMenu(
      e,
      (st().profiles ?? []).map((p) => ({
        label: p,
        checked: session.docker.profiles.includes(p),
        action: () => {
          mutate((s) => (s.docker.profiles = s.docker.profiles.includes(p) ? s.docker.profiles.filter((x) => x !== p) : [...s.docker.profiles, p]))
          void refreshStack()
        },
      })),
    )

  return (
    <Show when={st().composeFile} fallback={<p class="muted pad">{t('No Compose file at the project root (compose.yaml, docker-compose.yml…).')}</p>}>
      <Show when={st().compose} fallback={<p class="muted pad">{t('The Docker Compose plugin is not installed (docker compose).')}</p>}>
        <Show when={stack()} fallback={<p class="muted pad">{t('Loading…')}</p>}>
          <Show when={!stack()!.error} fallback={<p class="danger pad mono small dk-error">{stack()!.error}</p>}>
            <div class="dk-bar">
              <span class="muted small mono">{st().composeFile}</span>
              <Show when={st().profiles?.length}>
                <button class="btn small" onClick={chooseProfiles} data-testid="docker-profiles">
                  {t('Profiles')}
                  <Show when={profiles().length}>: {profiles().join(', ')}</Show>
                </button>
              </Show>
            </div>
            <Row
              testid="docker-stack"
              name={
                <>
                  <b>{stk()?.name}</b> <span class="muted small">{t('{n}/{total} running', { n: running(), total: rows().length })}</span>
                </>
              }
              busyKey="stack"
              selected={selected()?.kind === 'stack'}
              onSelect={() => setSelected({ kind: 'stack' })}
              menu={stackItems}
              running={running() > 0}
              onStart={() => composeAction('start')}
              onStop={() => composeAction('stop')}
              onRestart={() => composeAction('restart')}
              dot={running() ? 'up' : 'down'}
            />
            <For each={rows()}>
              {(r) => (
                <Row
                  testid={`docker-service-${r.svc}`}
                  name={
                    <>
                      <span class="dk-indent" />
                      {r.svc}
                      <Show when={!r.c}>
                        <span class="muted small"> · {t('not created')}</span>
                      </Show>
                    </>
                  }
                  title={r.c?.name}
                  c={r.c}
                  busyKey={r.svc}
                  selected={!!r.c && isSel(r.c.id)}
                  onSelect={() => r.c && setSelected({ kind: 'container', id: r.c.id })}
                  menu={() => serviceItems(r.svc, r.c)}
                  running={r.c?.state === 'running'}
                  onStart={() => composeAction('start', r.svc)}
                  onStop={() => composeAction('stop', r.svc)}
                  onRestart={() => composeAction('restart', r.svc)}
                />
              )}
            </For>
          </Show>
        </Show>
      </Show>
    </Show>
  )
}

// ---------- Host tab ----------

function HostList() {
  const groups = () => {
    const out: { project: string; list: Container[] }[] = []
    for (const c of host()?.list ?? []) {
      const g = out.find((x) => x.project === (c.project ?? ''))
      if (g) g.list.push(c)
      else out.push({ project: c.project ?? '', list: [c] })
    }
    // Containers outside Compose last.
    return out.sort((a, b) => (!a.project ? 1 : !b.project ? -1 : a.project.localeCompare(b.project)))
  }
  const items = (c: Container): MenuItem[] => [
    { label: t('Start'), action: () => containerAction('start', c.id), disabled: c.state === 'running' },
    { label: t('Stop'), action: () => containerAction('stop', c.id), disabled: c.state !== 'running' },
    { label: t('Restart'), action: () => containerAction('restart', c.id) },
    { separator: true, label: '' },
    ...containerItems(c),
    { separator: true, label: '' },
    {
      label: t('docker|Remove'),
      danger: true,
      action: () => {
        const msg = t('Remove the container {name}?', { name: c.name }) + (c.state === 'running' ? ' ' + t('It is stopped first.') : '')
        if (confirm(msg)) void containerAction('remove', c.id)
      },
    },
  ]
  return (
    <Show when={host()} fallback={<p class="muted pad">{t('Loading…')}</p>}>
      <Show when={!host()!.error} fallback={<p class="danger pad mono small dk-error">{host()!.error}</p>}>
        <Show when={host()!.list!.length} fallback={<p class="muted pad">{t('No container on this host.')}</p>}>
          <For each={groups()}>
            {(g) => (
              <>
                <div class="dk-group muted small">{g.project || t('Outside Compose')}</div>
                <For each={g.list}>
                  {(c) => (
                    <Row
                      testid={`docker-container-${c.name}`}
                      name={c.name}
                      title={c.status}
                      c={c}
                      busyKey={c.id}
                      selected={isSel(c.id)}
                      onSelect={() => setSelected({ kind: 'container', id: c.id })}
                      menu={() => items(c)}
                      running={c.state === 'running'}
                      onStart={() => containerAction('start', c.id)}
                      onStop={() => containerAction('stop', c.id)}
                      onRestart={() => containerAction('restart', c.id)}
                    />
                  )}
                </For>
              </>
            )}
          </For>
        </Show>
      </Show>
    </Show>
  )
}
