// Workflow of a ticket: the buttons of each status, its linked conversations and its git
// state (branch, worktree, changes).
import { For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { moveTicket, type ChatRole, type Status, type Ticket } from './state'
import { openTicketChat, startTicketChat } from './sessions'
import { Section, type Apply } from './TicketView'

export interface ActionButton {
  label: string
  run: () => void
  primary?: boolean
  danger?: boolean
  disabled?: boolean
  title?: string
  testid?: string
}

interface Ctx {
  move: (s: Status, comment?: string) => Promise<void>
  apply: Apply
  focusFeedback: () => void
}

/** Buttons of the header of a ticket for its status. */
export function ticketActions(t: Ticket, ctx: Ctx): ActionButton[] {
  switch (t.status) {
    case 'new':
      return [
        { label: 'Briefing', run: () => void startTicketChat(t, 'briefing'), title: 'Conversation (mode Plan) pour préciser le ticket', testid: 'ticket-briefing' },
        {
          label: t.plan.trim() ? 'Refaire le plan' : 'Générer le plan',
          primary: !t.plan.trim(),
          run: () => void startTicketChat(t, 'plan'),
          title: 'Le modèle écrit le plan et les goals, puis passe le ticket à développer',
          testid: 'ticket-plan-generate',
        },
        {
          label: 'Passer à développer',
          primary: !!t.plan.trim(),
          disabled: !t.plan.trim() && !t.goals,
          title: !t.plan.trim() && !t.goals ? 'Il faut d’abord un plan ou des goals' : undefined,
          run: () => void ctx.move('ready'),
          testid: 'ticket-to-ready',
        },
      ]
    case 'ready':
      return [
        { label: 'Revenir à « Nouveau »', run: () => void ctx.move('new') },
        { label: 'Commencer le développement', primary: true, run: () => void ctx.move('in_progress'), testid: 'ticket-start' },
      ]
    case 'in_progress':
    case 'fix':
      return [
        {
          label: t.status === 'fix' ? 'Session de correction' : 'Nouvelle session de dev',
          run: () => void startTicketChat(t, t.status === 'fix' ? 'correction' : 'dev'),
          testid: 'ticket-session',
        },
        { label: 'Envoyer en test', primary: true, run: () => void ctx.move('review'), testid: 'ticket-to-review' },
      ]
    case 'review':
      return [
        { label: 'Ajouter un retour', run: ctx.focusFeedback, testid: 'ticket-feedback' },
        { label: 'Fermer le ticket', primary: true, run: () => void ctx.apply(moveTicket(t.id, 'done')), testid: 'ticket-close' },
      ]
    case 'done':
      return [{ label: 'Rouvrir (→ Correction)', run: () => void ctx.move('fix'), testid: 'ticket-reopen' }]
    case 'abandoned':
      return [{ label: 'Rouvrir', run: () => void ctx.move('new'), testid: 'ticket-reopen' }]
  }
}

export function TicketChats(props: { t: Ticket; apply: Apply; onUnlink: (chatId: string) => void; roleLabels: Record<ChatRole, string> }) {
  return (
    <Section title="Conversations">
      <For each={props.t.chatList} fallback={<p class="muted small">Aucune conversation liée.</p>}>
        {(c) => (
          <div class="tk-row">
            <Icon name="sparkle" size={12} />
            <span class={`kb-role r-${c.role}`}>{props.roleLabels[c.role]}</span>
            <button class="link ellipsis small" title={c.title} onClick={() => void openTicketChat(c.chatId)} data-testid="ticket-chat">
              {c.title || 'Conversation'}
            </button>
            <span class="grow" />
            <button class="icon-btn small" title="Délier" onClick={() => props.onUnlink(c.chatId)}>
              <Icon name="close" size={11} />
            </button>
          </div>
        )}
      </For>
    </Section>
  )
}

export function TicketGit(props: { t: Ticket; apply: Apply }) {
  return (
    <Show when={props.t.branch}>
      <Section title="Git">
        <p class="small">
          Branche <span class="mono">{props.t.branch}</span>
        </p>
      </Section>
    </Show>
  )
}
