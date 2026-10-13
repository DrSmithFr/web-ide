// Agents tool: one tab per ACP session of a CLI agent (Claude Code, opencode, pi), its thread
// streamed by the pod, a prompt box under it. Permissions are asked in the thread.
import { createEffect, createResource, createSignal, For, Match, on, onMount, Show, Switch } from 'solid-js'
import { Icon } from '../ui/icons'
import { EmptyState } from '../ui/EmptyState'
import { WorktreeChip } from '../ui/WorktreeChip'
import { contextMenu, type MenuItem } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { copyText } from '../ui/clipboard'
import { openFile, project, relPath } from '../state/project'
import { DiffBlock, Markdown } from '../llm/parts'
import { t } from '../i18n'
import {
  active,
  answerAgent,
  catalog,
  closeAgent,
  createAgent,
  diffLines,
  installAgent,
  loadThread,
  restartAgent,
  sessions,
  setActive,
  threads,
  type AgentInfo,
  type CatalogAgent,
  type Entry,
} from './state'
import '../llm/assistant.css'
import { ProjectAgentStats, statsTitle } from './stats'
import { AgentComposer } from './Composer'
import { UserAttachments } from './UserAttachments'
import './agents.css'

const stateLabel = (s: AgentInfo['state']) =>
  ({ starting: t('agent|Starting'), ready: t('agent|Ready'), busy: t('agent|Working'), asking: t('Waiting for you'), error: t('Stopped') })[s]

export function AgentsTool() {
  const current = () => {
    const a = active()
    return sessions().some((s) => s.id === a) ? a : (sessions()[0]?.id ?? null)
  }
  const plus = async (e: MouseEvent) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const at = { clientX: r.left, clientY: r.bottom + 2, preventDefault() {}, stopPropagation() {} } as MouseEvent
    try {
      const c = await catalog()
      if (!c.local) return toast(t('Agents run on local projects only, for now.'), 'warn')
      const items: MenuItem[] = c.agents.map((a) =>
        a.installed ? { label: a.name, hint: a.missing ? t('needs {command}', { command: a.missing }) : '', action: () => void open(a.id) } : { label: t('Install {name}…', { name: a.name }), action: () => void install(a) },
      )
      contextMenu(at, items)
    } catch (err) {
      errorToast(err)
    }
  }
  return (
    <div class="panel ai-panel agents-panel">
      <div class="tabbar tool-tabbar agents-tabs" role="tablist">
        <For each={sessions()}>
          {(s) => (
            <div
              class="tab"
              role="tab"
              data-testid="agent-tab"
              data-state={s.state}
              aria-selected={current() === s.id}
              classList={{ active: current() === s.id }}
              title={`${s.name} · ${stateLabel(s.state)}` + (s.stats ? '\n' + statsTitle(s.stats) : '')}
              onClick={() => setActive(s.id)}
              onMouseDown={(e) => e.button === 1 && (e.preventDefault(), void close(s.id))}
            >
              <span class={`agent-dot ${s.state}`} />
              <span class="tab-title">{s.title}</span>
              <WorktreeChip project={s.project} />
              <button class="tab-close" title={t('Close')} onMouseDown={(e) => e.stopPropagation()} onClick={(e) => (e.stopPropagation(), void close(s.id))}>
                <span class="x">✕</span>
              </button>
            </div>
          )}
        </For>
        <button class="icon-btn small" title={t('New agent session')} onClick={(e) => void plus(e)} data-testid="agent-new">
          <Icon name="plus" />
        </button>
      </div>
      <div class="tool-body agents-body">
        <Show when={current()} keyed fallback={<Launcher />}>
          {(id) => <SessionView id={id} />}
        </Show>
      </div>
    </div>
  )
}

async function open(agent: string) {
  try {
    await createAgent(agent)
  } catch (e) {
    errorToast(e)
  }
}

async function close(id: string) {
  try {
    await closeAgent(id)
  } catch (e) {
    errorToast(e)
  }
}

const [installing, setInstalling] = createSignal<string | null>(null)

async function install(a: CatalogAgent) {
  if (!a.package) {
    if (a.hint) void copyText(a.hint)
    return toast(t('{name} is installed by hand: {command} (copied)', { name: a.name, command: a.hint ?? '' }), 'info', undefined, 8000)
  }
  setInstalling(a.id)
  try {
    await installAgent(a.id)
    toast(t('{name} is installed.', { name: a.name }))
    return true
  } catch (e) {
    errorToast(e)
  } finally {
    setInstalling(null)
  }
}

/** No session: the agents, to open or install. */
function Launcher() {
  const [list, { refetch }] = createResource(() => project()?.id, (pid) => catalog(pid))
  return (
    <div class="agents-launcher" data-testid="agents-launcher">
      <Show when={list()?.local !== false} fallback={<EmptyState icon="sparkle" text={t('Agents run on local projects only, for now.')} />}>
        <p class="muted small">{t('Open a session with a coding agent. It works in this project, through the Agent Client Protocol.')}</p>
        <ProjectAgentStats />
        <For each={list()?.agents ?? []}>
          {(a) => (
            <div class="agent-card" data-testid={`agent-card-${a.id}`}>
              <div class="agent-card-name">{a.name}</div>
              <div class="muted small grow">
                <Show when={a.installed} fallback={a.package ? t('Not installed: the IDE installs {package}.', { package: a.package }) : t('Not installed: {command}', { command: a.hint ?? '' })}>
                  <Show when={a.missing} fallback={t('Installed')}>
                    {t('needs {command}', { command: a.missing! })}
                  </Show>
                </Show>
              </div>
              <Show
                when={a.installed}
                fallback={
                  <button class="btn small" disabled={!!installing()} onClick={() => void install(a).then((ok) => ok && refetch())}>
                    {installing() === a.id ? t('Installing…') : a.package ? t('Install') : t('Copy the command')}
                  </button>
                }
              >
                <button class="btn small primary" onClick={() => void open(a.id)}>
                  {t('Open')}
                </button>
              </Show>
            </div>
          )}
        </For>
      </Show>
    </div>
  )
}

function SessionView(props: { id: string }) {
  const info = () => sessions().find((s) => s.id === props.id)
  const entries = () => threads[props.id]?.entries ?? []
  onMount(() => void loadThread(props.id).catch(errorToast))
  let scroller!: HTMLDivElement
  let stick = true
  createEffect(
    on(
      () => {
        const e = entries()
        return [e.length, e[e.length - 1]?.text?.length, e[e.length - 1]?.status]
      },
      () => stick && requestAnimationFrame(() => (scroller.scrollTop = scroller.scrollHeight)),
    ),
  )
  const busy = () => info()?.state === 'busy' || info()?.state === 'asking'
  return (
    <div class="agent-session" data-testid="agent-session">
      <Show when={info()?.state === 'error'}>
        <div class="ai-banner" data-testid="agent-error">
          <Icon name="warning" size={14} />
          <span class="grow">
            {info()!.crashed ? t('The agent kept ending, it is not restarted anymore.') : t('The agent stopped.')}
            <Show when={info()!.error}>
              <pre class="agent-stderr">{info()!.error}</pre>
            </Show>
          </span>
          <button class="btn small" onClick={() => void restartAgent(props.id).catch(errorToast)} data-testid="agent-restart">
            {t('Restart')}
          </button>
        </div>
      </Show>
      <div class="ai-messages" ref={scroller} onScroll={() => (stick = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 40)}>
        <div class="ai-thread agent-thread" data-testid="agent-thread">
          <Show when={info()?.state === 'starting' && entries().length === 0}>
            <div class="muted small">{t('Starting {name}…', { name: info()?.name ?? '' })}</div>
          </Show>
          <For each={entries()}>{(e, i) => <EntryView id={props.id} entry={e} index={i()} />}</For>
          <Show when={busy()}>
            <div class="ai-live-stats">
              <span class="ai-dots">
                <i />
                <i />
                <i />
              </span>
              {stateLabel(info()!.state)}
            </div>
          </Show>
        </div>
      </div>
      <AgentComposer id={props.id} />
    </div>
  )
}

function EntryView(props: { id: string; entry: Entry; index: number }) {
  const e = () => props.entry
  return (
    <Switch>
      <Match when={e().kind === 'user'}>
        <div class="ai-msg user" data-testid="agent-user">
          <Show when={e().attachments?.length}>
            <UserAttachments id={props.id} list={e().attachments!} />
          </Show>
          <div class="ai-user-bubble">
            <div class="ai-user-text">{e().text}</div>
          </div>
        </div>
      </Match>
      <Match when={e().kind === 'agent'}>
        <div class="ai-msg assistant" data-testid="agent-message">
          <Markdown text={e().text ?? ''} final={true} />
        </div>
      </Match>
      <Match when={e().kind === 'thought'}>
        <Thought text={e().text ?? ''} />
      </Match>
      <Match when={e().kind === 'tool'}>
        <ToolView entry={e()} />
      </Match>
      <Match when={e().kind === 'plan'}>
        <div class="agent-plan" data-testid="agent-plan">
          <div class="agent-plan-head">
            <Icon name="check" size={13} /> {t('Plan')}
          </div>
          <For each={e().plan ?? []}>
            {(p) => (
              <div class={`agent-plan-item ${p.status ?? ''}`}>
                <span class="agent-plan-mark">{p.status === 'completed' ? '✓' : p.status === 'in_progress' ? '▸' : '○'}</span>
                {p.content}
              </div>
            )}
          </For>
        </div>
      </Match>
      <Match when={e().kind === 'permission'}>
        <PermissionCard id={props.id} entry={e()} index={props.index} />
      </Match>
      <Match when={e().kind === 'notice'}>
        <div class="agent-notice muted small" data-testid="agent-notice">
          {t(e().text ?? '')}
        </div>
      </Match>
      <Match when={e().kind === 'error'}>
        <div class="agent-notice danger small" data-testid="agent-notice">
          {e().text}
        </div>
      </Match>
    </Switch>
  )
}

function Thought(props: { text: string }) {
  const [open, setOpen] = createSignal(false)
  return (
    <div class="ai-reasoning" classList={{ open: open() }} data-testid="agent-thought">
      <button class="ai-tool-head" onClick={() => setOpen(!open())}>
        <span class="ai-tool-icon">
          <Icon name="sparkle" size={13} />
        </span>
        <span class="ai-tool-name">{t('Thinking')}</span>
        <span class="grow" />
        <span class="ai-chev" classList={{ open: open() }}>
          <Icon name="chevron" size={12} />
        </span>
      </button>
      <div class="ai-reasoning-text">{props.text}</div>
    </div>
  )
}

// Notices written by the pod (internal/acp), translated here.
export const notices = () => [
  t('Stopped.'),
  t('The agent stopped: limit reached.'),
  t('The agent refused to go on.'),
  t('The agent stopped during the turn.'),
  t('The agent ended: it is restarted and its session loaded.'),
  t('The agent could not load the session again: it starts a new one, without the context above.'),
  t('This agent cannot reach the MCP server of the IDE: no kanban tools in this session.'),
]

const kindIcons: Record<string, string> = { read: 'file', edit: 'edit', delete: 'trash', move: 'file', search: 'search', execute: 'terminal', think: 'sparkle', fetch: 'external' }

function ToolView(props: { entry: Entry }) {
  const e = () => props.entry
  const diffs = () => (e().content ?? []).filter((c) => c.type === 'diff')
  const texts = () => (e().content ?? []).filter((c) => c.type !== 'diff' && c.text)
  // Edits show their diff; other tools open on demand.
  const [open, setOpen] = createSignal(diffs().length > 0)
  createEffect(on(() => diffs().length, (n, prev) => n > 0 && !prev && setOpen(true), { defer: true }))
  const status = () => e().status ?? 'pending'
  return (
    <div class={`ai-tool agent-tool ${status()}`} classList={{ ok: status() === 'completed', error: status() === 'failed' }} data-testid="agent-tool" data-status={status()}>
      <button class="ai-tool-head" onClick={() => setOpen(!open())}>
        <span class="ai-tool-icon">
          <Icon name={kindIcons[e().toolKind ?? ''] ?? 'puzzle'} size={13} />
        </span>
        <span class="ai-tool-target">{e().title || e().toolKind || t('Tool')}</span>
        <span class="grow" />
        <Show when={status() !== 'completed'}>
          <span class="ai-tool-sum">{status() === 'failed' ? t('agent|failed') : status() === 'in_progress' ? t('running') : t('agent|pending')}</span>
        </Show>
        <Show when={diffs().length || texts().length || e().locations?.length}>
          <span class="ai-chev" classList={{ open: open() }}>
            <Icon name="chevron" size={12} />
          </span>
        </Show>
      </button>
      <Show when={open()}>
        <For each={e().locations ?? []}>
          {(p) => (
            <div class="ai-tool-detail">
              <button class="link small" onClick={() => void openFile(p)}>
                {relPath(p)}
              </button>
            </div>
          )}
        </For>
        <For each={diffs()}>
          {(d) => (
            <div data-testid="agent-diff">
              <Show when={!e().locations?.includes(d.path ?? '')}>
                <div class="ai-tool-detail">
                  <button class="link small" onClick={() => void openFile(d.path!)}>
                    {relPath(d.path ?? '')}
                  </button>
                </div>
              </Show>
              <DiffBlock lines={diffLines(d.oldText, d.newText ?? '')} />
            </div>
          )}
        </For>
        <For each={texts()}>{(c) => <pre class="ai-tool-out">{c.text}</pre>}</For>
      </Show>
    </div>
  )
}

function PermissionCard(props: { id: string; entry: Entry; index: number }) {
  const e = () => props.entry
  const chosen = () => e().options?.find((o) => o.optionId === e().outcome)
  return (
    <div class="ai-approval agent-permission" data-testid="agent-permission" data-outcome={e().outcome ?? ''}>
      <div class="ai-approval-head">
        <Icon name="lock" size={14} />
        <span class="grow">{t('Allow: {title}?', { title: e().title || t('Tool') })}</span>
      </div>
      <div class="ai-approval-foot">
        <Show
          when={!e().outcome}
          fallback={<span class="muted small">{e().outcome === 'cancelled' ? t('Cancelled') : (chosen()?.name ?? e().outcome)}</span>}
        >
          <For each={e().options ?? []}>
            {(o) => (
              <button
                class="btn small"
                classList={{ primary: o.kind === 'allow_once', danger: o.kind.startsWith('reject') }}
                onClick={() => void answerAgent(props.id, props.index, o.optionId).catch(errorToast)}
                data-testid={`agent-option-${o.optionId}`}
              >
                {o.name}
              </button>
            )}
          </For>
        </Show>
      </div>
    </div>
  )
}
