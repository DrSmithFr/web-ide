// The Kanban plugin (pod/internal/plugins/kanban): its tool, the board, ticket, milestone and
// objective views, the New ticket modal, and the tickets service the assistant, the Orchestrator
// and the project bar use.
import type { PluginModule } from '../types'
import type { TicketsService } from '../capabilities'
import { bindApi, t } from './plugin'
import { KanbanPanel } from './Panel'
import { Board, NewTicketHost } from './Board'
import { TicketView } from './TicketView'
import { MilestoneView } from './MilestoneView'
import { ObjectiveView } from './ObjectiveView'
import { blockerText } from './Lineage'
import { viewStatus } from './status'
import { startTicketChat, startWorkSession } from './sessions'
import { board, ensureBoard, getTicket, openBoard, openMilestone, openObjective, openTicket, reloadBoard, roleLabels, watchBoard, statusLabels, summary, worktreeProject, type Ticket } from './state'

const short = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

/** The title of a tab: the ticket number and title, the name of the milestone or objective. */
function viewTitle(tab: { kind: string; state?: unknown }) {
  const st = (tab.state ?? {}) as { ticket?: number; milestone?: number; objective?: number }
  if (tab.kind.endsWith('/board')) return 'Kanban'
  if (st.ticket) {
    const s = summary(st.ticket)
    return s ? `#${s.id} ${short(s.title, 28)}` : `#${st.ticket}`
  }
  if (st.milestone) {
    const m = board.milestones.find((x) => x.id === st.milestone)
    return m ? short(m.title, 32) : t('Milestone')
  }
  if (st.objective) {
    const o = board.objectives.find((x) => x.id === st.objective)
    return o ? short(o.title, 32) : t('Objective')
  }
  return undefined
}

const tickets: TicketsService = {
  tickets: () => board.tickets,
  ensure: ensureBoard,
  summary,
  get: getTicket,
  open: openTicket,
  openBoard: () => openBoard(),
  openMilestone,
  openObjective,
  startChat: (tk, role) => startTicketChat(tk as Ticket, role),
  startWork: (tk, role, adoptedBy) => startWorkSession(tk as Ticket, role, undefined, false, adoptedBy),
  blockerText: (b) => blockerText(b as Parameters<typeof blockerText>[0]),
  statusLabel: (s) => statusLabels[s],
  roleLabel: (r) => roleLabels[r],
  worktreeProject,
}

export const kanban: PluginModule = {
  activate(api) {
    bindApi(api)
    watchBoard()
    api.provide('tickets', tickets)
    return {
      tools: { kanban: { component: KanbanPanel } },
      views: { board: Board, ticket: TicketView, milestone: MilestoneView, objective: ObjectiveView },
      viewTitle,
      aloneViews: ['board'],
      viewStatus,
      hosts: [NewTicketHost],
      commands: { 'kanban.open': () => openBoard() },
      opened: reloadBoard,
    }
  },
}
