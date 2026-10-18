// Objectives of the kanban (docs/design/kanban.md): named deadlines (MVP, a launch…) in
// order. A milestone says what, an objective says when: it takes tickets of several milestones
// and lone ones. Only the user reaches, abandons or reorders one.
import { createStore } from 'solid-js/store'
import { request } from '../pod/rpc'
import type { MilestoneSummary } from './milestones'
import type { Note, Summary } from './state'

export type ObjectiveState = 'open' | 'reached' | 'abandoned'

export interface ObjectiveSummary {
  id: number
  title: string
  /** The date aimed at, YYYY-MM-DD. */
  due?: string
  pos: number
  state: ObjectiveState
  created: number
  updated: number
  closed?: number
  /** Tickets: all of them, finished (Done or Abandoned), To test. */
  tickets: number
  ticketsDone: number
  toTest: number
}

export interface Objective extends ObjectiveSummary {
  description: string
  ticketList: Summary[]
  /** The milestones whose default objective it is, or holding one of its tickets. */
  milestoneList: MilestoneSummary[]
  notes: Note[]
}

export const objectiveStateNames: Record<ObjectiveState, string> = { open: 'Ongoing', reached: 'Reached', abandoned: 'Abandoned' }
/** The objective filter of Board and Roadmap: all tickets, those without objective, or one. */
export type ObjectiveFilter = 'all' | 'none' | number
export const objectiveMatch = (f: ObjectiveFilter, objective?: number) => f === 'all' || (f === 'none' ? !objective : objective === f)

/** Bumped for an objective each time it changes: objective views reload it. */
const [versions, setVersions] = createStore<Record<number, number>>({})
export const objectiveVersion = (id: number) => versions[id] ?? 0
export const bumpObjective = (id: number) => setVersions(id, (v) => (v ?? 0) + 1)

export interface ObjectivePatch {
  title?: string
  description?: string
  due?: string
}

export const listObjectives = () => request<ObjectiveSummary[]>('kanban.objective.list')
export const getObjective = (id: number) => request<Objective>('kanban.objective.get', { id })
export const createObjective = (p: ObjectivePatch & { title: string }) => request<Objective>('kanban.objective.create', { ...p, by: 'user' })
export const updateObjective = (id: number, patch: ObjectivePatch) => request<Objective>('kanban.objective.update', { id, patch })
export const deleteObjective = (id: number) => request('kanban.objective.delete', { id })
export const moveObjective = (id: number, delta: number) => request<Objective>('kanban.objective.move', { id, delta })
export const reachObjective = (id: number) => request<Objective>('kanban.objective.reach', { id })
export const setObjectiveState = (id: number, state: 'open' | 'abandoned') => request<Objective>('kanban.objective.state', { id, state })
export const addObjectiveNote = (id: number, text: string) => request<Objective>('kanban.objective.note', { id, text })
export const deleteObjectiveNote = (id: number, noteId: number) => request<Objective>('kanban.objective.note.delete', { id, noteId })
