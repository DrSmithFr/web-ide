// A ticket developed by an ACP agent (kanban setting devAgent): its session of the Agents tool
// in the worktree, started or prompted by the pod (kanban.agent), opened from the ticket.
import { createResource, Show } from 'solid-js'
import { mutate, project, showWorktree } from '../state/project'
import { showTool } from '../state/zones'
import { request } from '../pod/rpc'
import { setActive } from '../agents/state'
import { errorToast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { t } from '../i18n'
import { board, ticketVersion, type Ticket } from './state'
import { Section } from './TicketView'

/** The ACP agent that develops the tickets ('' for the assistant). */
export const devAgent = () => (board.meta.devAgent && board.meta.devAgent !== 'assistant' ? board.meta.devAgent : '')

interface Found {
  project?: string
  session?: string
  name?: string
  state?: string
}

/** Shows a session of a worktree in this window: the worktree, the Agents tool, its tab. */
async function showSession(projectId: string, session: string) {
  if (project()?.id !== projectId) await showWorktree(projectId)
  mutate((s) => showTool(s, 'agents'))
  setActive(session)
}

/** Gives the ticket to the development agent (its session opened with the briefing of the role,
 *  or the text sent to the session it has), then shows the session. */
export async function startAgentSession(tk: Ticket, role: string, text: string, feedback?: number) {
  try {
    const r = await request<{ project: string; session: string }>('kanban.agent', { id: tk.id, role, text, feedback: feedback ?? 0 })
    await showSession(r.project, r.session)
  } catch (e) {
    errorToast(e)
  }
}

export async function openAgentSession(tk: Ticket) {
  try {
    const r = await request<Found>('kanban.agent.find', { id: tk.id })
    if (r.session) await showSession(r.project!, r.session)
  } catch (e) {
    errorToast(e)
  }
}

/** The session of the ticket in its view, when it has one. */
export function TicketAgentSession(props: { tk: Ticket }) {
  const [found] = createResource(
    () => ({ id: props.tk.id, v: ticketVersion(props.tk.id), wt: props.tk.worktree }),
    ({ id }) => request<Found>('kanban.agent.find', { id }).catch(() => ({}) as Found),
  )
  return (
    <Show when={found()?.session}>
      <Section title={t('Agent session')}>
        <div class="tk-row" data-testid="ticket-agent-session">
          <Icon name="comment" size={12} />
          <span class="small">{found()!.name}</span>
          <span class="grow" />
          <button class="btn small" onClick={() => void openAgentSession(props.tk)} data-testid="ticket-agent-open">
            {t('Open the session')}
          </button>
        </div>
      </Section>
    </Show>
  )
}
