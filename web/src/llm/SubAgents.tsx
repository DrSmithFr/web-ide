// Sub-agents in the thread (pod/internal/agent/subagents.go): the card of each child in its
// parent, the events of a child (note, question, report) or of the parent (message), the
// task and the header of a child conversation.
import { For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast } from '../ui/toast'
import { request } from '../pod/rpc'
import { t } from '../i18n'
import { openChat, runStates } from './agent'
import { formatTokens, Markdown } from './parts'
import { chat, chatList, config, type AgentEvent, type ChatInfo, type ChatMessage } from './state'

/** Status of a sub-agent shown to the user: its run first (waiting for you, queued). */
export const agentLabels: Record<string, string> = {
  running: 'Running',
  queued: 'Waiting for the model server',
  waiting_user: 'Waiting for you',
  waiting_parent: 'Waiting for its parent',
  done: 'Done',
  blocked: 'Blocked',
  stopped: 'Stopped',
  error: 'Error',
  compacting: 'Compacting',
}

export function agentStatus(id: string, stored?: string): string {
  const run = runStates()[id]
  if (run && run !== 'running') return run
  return stored ?? (run ? 'running' : '')
}

/** Model, tokens and cost of a child. */
function usageText(c: ChatInfo): string {
  const parts = [c.model ?? '']
  if (c.tokens) parts.push(t('{n} tokens', { n: formatTokens(c.tokens) }))
  if (c.cost) parts.push(t('cost {cost}', { cost: c.cost < 0.01 ? c.cost.toFixed(4) : c.cost.toFixed(2) }))
  return parts.filter(Boolean).join(' · ')
}

const open = (id?: string) => id && openChat(id).catch(errorToast)

/** The card of a child in the thread of its parent, kept in sight. */
export function ChildCard(props: { id: string }) {
  const info = () => chatList().find((c) => c.id === props.id)
  const status = () => agentStatus(props.id, info()?.status)
  const events = () => chat.messages.filter((m) => m.event?.child === props.id).map((m) => m.event!)
  const note = () => [...events()].reverse().find((e) => e.type === 'note')
  const question = () => (status() === 'waiting_parent' ? [...events()].reverse().find((e) => e.type === 'question') : undefined)
  return (
    <div class={`ai-child ${status()}`} data-testid="ai-child">
      <div class="ai-child-head">
        <Icon name="sparkle" size={13} />
        <b class="ellipsis">{info()?.title ?? props.id}</b>
        <span class={`badge ai-child-status ${status()}`} data-testid="ai-child-status">
          {t(agentLabels[status()] ?? 'Running')}
        </span>
        <span class="grow" />
        <Show when={runStates()[props.id]}>
          <button class="btn small" onClick={() => request('agent.stop', { id: props.id }).catch(errorToast)} data-testid="ai-child-stop">
            <Icon name="stop" size={12} /> {t('Stop')}
          </button>
        </Show>
        <button class="btn small" onClick={() => open(props.id)} data-testid="ai-child-open">
          <Icon name="external" size={12} /> {t('Open')}
        </button>
      </div>
      <Show when={info()?.model}>
        <div class="ai-child-line small ai-child-usage" data-testid="ai-child-usage">
          {usageText(info()!)}
        </div>
      </Show>
      <Show when={note()}>
        {(n) => (
          <div class="ai-child-line small" data-testid="ai-child-note">
            <span class="muted">{t('Latest note')}</span> {n().head}
          </div>
        )}
      </Show>
      <Show when={question()}>
        {(q) => (
          <div class="ai-child-line small" data-testid="ai-child-question">
            <span class="muted">{t('Asks')}</span> {q().text}
          </div>
        )}
      </Show>
    </div>
  )
}

/** A note, a question or a report of a child (in its parent), or a message of the parent (in the child). */
export function EventCard(props: { msg: ChatMessage }) {
  const e = () => props.msg.event as AgentEvent
  const head = () => {
    switch (e().type) {
      case 'note':
        return t('Note of “{title}”', { title: e().title })
      case 'question':
        return t('“{title}” asks', { title: e().title })
      case 'report':
        return t('Report of “{title}”', { title: e().title })
    }
    return e().from === 'user' ? t('Your message') : t('Message of the parent conversation')
  }
  const target = () => (e().type === 'message' ? chat.parent : e().child)
  return (
    <div class={`ai-event ${e().type}`} data-testid={`ai-event-${e().type}`}>
      <div class="ai-event-head">
        <Icon name={e().type === 'question' ? 'info' : e().type === 'report' ? 'check' : e().type === 'note' ? 'pen' : 'comment'} size={13} />
        <button class="link" onClick={() => open(target())} title={t('Open the conversation')}>
          {head()}
        </button>
        <Show when={e().type === 'report' && e().status}>
          <span class={`badge ai-child-status ${e().status}`}>{t(agentLabels[e().status!] ?? e().status!)}</span>
        </Show>
      </div>
      <Show when={e().text || e().head}>
        <div class="ai-event-body">
          <Show when={e().type === 'note' && e().head}>
            <b>{e().head}</b>
          </Show>
          <Markdown text={e().text} final />
        </div>
      </Show>
      <Show when={e().files?.length}>
        <div class="ai-event-files small mono">
          <For each={e().files}>{(f) => <span>{f}</span>}</For>
        </div>
      </Show>
    </div>
  )
}

/** First message of a child: its task. */
export function TaskCard(props: { msg: ChatMessage }) {
  return (
    <div class="ai-event task" data-testid="ai-task">
      <div class="ai-event-head">
        <Icon name="kanban" size={13} />
        <span>{t('Task given by the parent conversation')}</span>
      </div>
      <div class="ai-event-body">
        <Markdown text={typeof props.msg.content === 'string' ? props.msg.content.replace(/^Task given by the parent conversation:\s*/, '') : ''} final />
      </div>
    </div>
  )
}

/** Header of a child conversation: its parent and its status. */
export function ChildHeader() {
  const parent = () => chatList().find((c) => c.id === chat.parent)
  const status = () => agentStatus(chat.id, chat.agent?.status)
  return (
    <div class="ai-child-header" data-testid="ai-child-header">
      <Icon name="sparkle" size={13} />
      <span>{chat.agent?.adopted ? t('Followed by') : t('Sub-agent of')}</span>
      <button class="link ellipsis" onClick={() => open(chat.parent)} data-testid="ai-child-parent">
        {parent()?.title ?? t('the parent conversation')}
      </button>
      <span class="grow" />
      <span class="muted small ellipsis" data-testid="ai-child-model">
        {config.servers.find((s) => s.id === chat.server)?.name ?? chat.server} · {chat.model}
      </span>
      <span class={`badge ai-child-status ${status()}`}>{t(agentLabels[status()] ?? status())}</span>
    </div>
  )
}
