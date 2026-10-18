// Milestones of the kanban (docs/design/kanban.md): the tickets of one feature, across
// lineages, with its spec, user stories and feedback; only the user validates one.
import { createStore } from 'solid-js/store'
import { request } from '../pod/rpc'
import type { By, ChatRole, Complexity, Feedback, FeedbackKind, Note, Summary } from './state'

export type MilestoneState = 'open' | 'validated' | 'abandoned'

export interface MilestoneSummary {
  id: number
  title: string
  state: MilestoneState
  created: number
  updated: number
  closed?: number
  /** Tickets: all of them, finished (Done or Abandoned), To test. */
  tickets: number
  ticketsDone: number
  toTest: number
  stories: number
  storiesDone: number
  /** Feedback not handled yet. */
  feedbackOpen: number
}

/** A user story: what a user can do once the milestone is done; checked by the user. */
export interface Story {
  id: number
  text: string
  done: boolean
}

export interface Milestone extends MilestoneSummary {
  description: string
  /** A path of the repository or an address. */
  spec: string
  storyList: Story[]
  /** ticket: the ticket made from it, which handles it once Done. */
  feedbackList: (Feedback & { ticket?: number })[]
  chatList: { chatId: string; role: ChatRole; title: string; created: number }[]
  notes: Note[]
  ticketList: Summary[]
}

export const milestoneStateNames: Record<MilestoneState, string> = { open: 'Ongoing', validated: 'Validated', abandoned: 'Abandoned' }
/** The milestone filter of Board and Roadmap: all tickets, those without milestone, or one. */
export type MilestoneFilter = 'all' | 'none' | number
export const milestoneMatch = (f: MilestoneFilter, milestone?: number) => f === 'all' || (f === 'none' ? !milestone : milestone === f)

/** Longest user story, in characters (checked by the pod too). */
export const MAX_STORY = 500

/** Bumped for a milestone each time it changes: milestone views reload it. */
const [versions, setVersions] = createStore<Record<number, number>>({})
export const milestoneVersion = (id: number) => versions[id] ?? 0
export const bumpMilestone = (id: number) => setVersions(id, (v) => (v ?? 0) + 1)

export interface MilestonePatch {
  title?: string
  description?: string
  spec?: string
}

export const listMilestones = () => request<MilestoneSummary[]>('kanban.milestone.list')
export const getMilestone = (id: number) => request<Milestone>('kanban.milestone.get', { id })
export const createMilestone = (p: MilestonePatch & { title: string }) => request<Milestone>('kanban.milestone.create', { ...p, by: 'user' })
export const updateMilestone = (id: number, patch: MilestonePatch) => request<Milestone>('kanban.milestone.update', { id, patch })
export const deleteMilestone = (id: number) => request('kanban.milestone.delete', { id })
export const storyOp = (id: number, story: { op: 'add' | 'edit' | 'check' | 'move' | 'delete'; id?: number; text?: string; done?: boolean; delta?: number }, by: By = 'user') =>
  request<Milestone>('kanban.milestone.story', { id, story, by })
export const milestoneFeedbackOp = (
  id: number,
  feedback: { op: 'add' | 'check' | 'chat' | 'delete' | 'complexity'; id?: number; kind?: FeedbackKind; text?: string; done?: boolean; chatId?: string; complexity?: Complexity | '' },
  by: By = 'user',
) => request<Milestone>('kanban.milestone.feedback', { id, feedback, by })
/** Makes a ticket of the milestone from a feedback. */
export const feedbackTicket = (id: number, feedback: number) => request<Milestone>('kanban.milestone.feedback.ticket', { id, feedback })
export const validateMilestone = (id: number) => request<Milestone>('kanban.milestone.validate', { id })
export const setMilestoneState = (id: number, state: 'open' | 'abandoned') => request<Milestone>('kanban.milestone.state', { id, state })
export const linkMilestoneChat = (id: number, chatId: string, title = '') => request<Milestone>('kanban.milestone.chat.link', { id, chatId, role: 'briefing', title })
export const unlinkMilestoneChat = (id: number, chatId: string) => request<Milestone>('kanban.milestone.chat.unlink', { id, chatId })
export const addMilestoneNote = (id: number, text: string) => request<Milestone>('kanban.milestone.note', { id, text })
export const deleteMilestoneNote = (id: number, noteId: number) => request<Milestone>('kanban.milestone.note.delete', { id, noteId })
