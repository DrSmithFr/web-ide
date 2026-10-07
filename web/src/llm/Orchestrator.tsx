// Orchestrator mode in the thread: the action cards the model offers (run only on the user's
// click, then showing what happened), and the link back to a conversation it opened.
import { createSignal, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast } from '../ui/toast'
import { request } from '../pod/rpc'
import { t } from '../i18n'
import { getTicket, openTicket } from '../kanban/state'
import { blockerText } from '../kanban/Lineage'
import { startTicketChat, startWorkSession } from '../kanban/sessions'
import { openChat } from './agent'
import { chat, chatList, type ActionCard as Card, type ChatMessage } from './state'

const kindIcons: Record<Card['kind'], string> = { start_dev: 'play', generate_plan: 'outline', open_ticket: 'kanban', open_conversation: 'comment' }

export function ActionCard(props: { msg: ChatMessage; index: number }) {
  const card = () => props.msg.card!
  const [busy, setBusy] = createSignal(false)
  const record = (state: 'done' | 'failed', result: string) => request('agent.card', { id: chat.id, index: props.index, state, result })
  const run = async () => {
    setBusy(true)
    try {
      const c = card()
      if (c.kind === 'open_conversation') {
        await record('done', t('opened'))
        await openChat(c.chat!)
        return
      }
      const tk = await getTicket(c.ticket!)
      switch (c.kind) {
        case 'open_ticket':
          await record('done', t('opened'))
          openTicket(tk.id)
          return
        case 'generate_plan':
          await record('done', t('plan requested'))
          await startTicketChat(tk, 'plan')
          return
        case 'start_dev':
          if (tk.status !== 'todo' && tk.status !== 'in_progress') {
            await record('failed', t('#{id} is not to do', { id: tk.id }))
            return
          }
          if (tk.blockers?.length) {
            await record('failed', t('Cannot start yet: {blockers}', { blockers: tk.blockers.map(blockerText).join(', ') }))
            return
          }
          await record('done', t('development started'))
          await startWorkSession(tk, 'dev')
      }
    } catch (e) {
      errorToast(e)
      await record('failed', (e as Error).message).catch(() => {})
    } finally {
      setBusy(false)
    }
  }
  return (
    <div class="ai-action" classList={{ used: !!card().state }} data-testid="ai-action">
      <Icon name={kindIcons[card().kind] ?? 'play'} size={14} />
      <div class="grow" style={{ 'min-width': '160px' }}>
        <Show when={card().reason}>
          <div class="muted small">{card().reason}</div>
        </Show>
        <Show when={card().state}>
          <div class={`small ${card().state === 'failed' ? 'danger' : 'ok'}`} data-testid="ai-action-result">
            {card().result}
          </div>
        </Show>
      </div>
      <button class="btn small" classList={{ primary: !card().state }} disabled={busy() || !!card().state} onClick={() => void run()} data-testid="ai-action-run">
        {card().label}
      </button>
    </div>
  )
}

/** The conversation open_conversation moved the user into: a way back to it. */
export function OpenedCard(props: { msg: ChatMessage }) {
  const title = () => chatList().find((c) => c.id === props.msg.opened)?.title ?? ''
  return (
    <div class="ai-action used" data-testid="ai-opened">
      <Icon name="comment" size={14} />
      <span class="grow ellipsis">{title() ? t('Opened: {title}', { title: title() }) : t('A conversation was opened')}</span>
      <button class="btn small" onClick={() => openChat(props.msg.opened!).catch(errorToast)} data-testid="ai-opened-go">
        {t('Open')}
      </button>
    </div>
  )
}
