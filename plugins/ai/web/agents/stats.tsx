// Work time and cost of the agents (pod: acp/stats.go): per session (its info), per ticket
// (the sessions of its worktree) and per project, over a period.
import { createResource, createSignal, For, onCleanup, Show } from 'solid-js'
import { onPod, request, t } from '../plugin'

export interface Totals {
  turns: number
  /** Work time (the waits for the user excluded) and waits, ms. */
  work: number
  wait: number
  tokens: number
  cost: number
  /** Turns without a price (a local or unknown model). */
  unpriced?: number
  /** Some of the cost was spent on a subscription: an API equivalent. */
  apiEquivalent?: boolean
}

export const agentStats = (o: { ticket?: number; since?: number } = {}) => request<Totals>('agents.stats', { ticket: o.ticket ?? 0, since: o.since ?? 0 })

export function fmtWork(ms: number) {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s} s`
  if (s < 3600) return `${Math.round(s / 60)} min`
  return `${Math.floor(s / 3600)} h ${String(Math.round((s % 3600) / 60)).padStart(2, '0')}`
}

export function fmtCost(c: number) {
  return '$' + (c > 0 && c < 0.01 ? c.toFixed(4) : c.toFixed(2))
}

/** "12 min · $0.42": the cost left out when no turn has a price. */
export function statsText(s: Totals) {
  const priced = s.turns - (s.unpriced ?? 0) > 0
  return priced ? `${fmtWork(s.work)} · ${fmtCost(s.cost)}` : fmtWork(s.work)
}

export function statsTitle(s: Totals) {
  const lines = [
    t('Work: {time}', { time: fmtWork(s.work) }),
    t('Waiting for you: {time}', { time: fmtWork(s.wait) }),
    t('{n} turns, {tokens} tokens', { n: s.turns, tokens: s.tokens.toLocaleString() }),
  ]
  if (s.turns - (s.unpriced ?? 0) > 0) lines.push(s.apiEquivalent ? t('Cost: {cost} (API equivalent)', { cost: fmtCost(s.cost) }) : t('Cost: {cost}', { cost: fmtCost(s.cost) }))
  if (s.unpriced) lines.push(t('{n} turns without a price (local or unknown model)', { n: s.unpriced }))
  return lines.join('\n')
}

/** A signal bumped a moment after the agents change: the totals are asked again. */
function useAgentsTick() {
  const [tick, setTick] = createSignal(0)
  let timer: number | undefined
  onCleanup(
    onPod('agents.changed', () => {
      clearTimeout(timer)
      timer = window.setTimeout(() => setTick((n) => n + 1), 1000)
    }),
  )
  onCleanup(() => clearTimeout(timer))
  return tick
}

/** Badge of a ticket: the time and cost of the agents in its worktree, hidden before any. */
export function TicketAgentStats(props: { ticket: number }) {
  const tick = useAgentsTick()
  const [s] = createResource(
    () => ({ id: props.ticket, n: tick() }),
    ({ id }) => agentStats({ ticket: id }).catch(() => null),
  )
  return (
    <Show when={s()?.turns}>
      <span class="badge" title={statsTitle(s()!)} data-testid="ticket-agent-stats">
        {t('Agents: {stats}', { stats: statsText(s()!) })}
      </span>
    </Show>
  )
}

const periods = () => [
  { id: 'today', label: t('Today') },
  { id: 'week', label: t('7 days') },
  { id: 'all', label: t('All') },
]

function since(period: string) {
  if (period === 'today') return new Date().setHours(0, 0, 0, 0)
  if (period === 'week') return Date.now() - 7 * 24 * 3600 * 1000
  return 0
}

/** Line of the project (its worktrees included), with a period. */
export function ProjectAgentStats() {
  const tick = useAgentsTick()
  const [period, setPeriod] = createSignal('week')
  const [s] = createResource(
    () => ({ p: period(), n: tick() }),
    ({ p }) => agentStats({ since: since(p) }).catch(() => null),
  )
  return (
    <div class="agent-stats" data-testid="project-agent-stats">
      <span class="muted small">{t('Agents in this project')}</span>
      <select class="small" value={period()} onChange={(e) => setPeriod(e.currentTarget.value)}>
        <For each={periods()}>{(p) => <option value={p.id}>{p.label}</option>}</For>
      </select>
      <span class="small" title={s() ? statsTitle(s()!) : ''} data-testid="project-agent-stats-value">
        {s()?.turns ? statsText(s()!) : t('nothing yet')}
      </span>
    </div>
  )
}
