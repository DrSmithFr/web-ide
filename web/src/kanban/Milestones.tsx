// Milestones view of the board: a card per milestone with its progress (tickets finished,
// stories validated, tickets to test, open feedback); a click opens the milestone page.
import { createSignal, For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { EmptyState } from '../ui/EmptyState'
import { board, openMilestone } from './state'
import { createMilestone, milestoneStateNames, type MilestoneSummary } from './milestones'
import { t } from '../i18n'

export async function newMilestone() {
  const title = await prompt({ title: t('New milestone'), label: t('Title of the feature') })
  if (!title?.trim()) return
  try {
    openMilestone((await createMilestone({ title })).id)
  } catch (e) {
    errorToast(e)
  }
}

export function Milestones() {
  const [showClosed, setShowClosed] = createSignal(false)
  const open = () => board.milestones.filter((m) => m.state === 'open')
  const closed = () => board.milestones.filter((m) => m.state !== 'open')
  return (
    <div class="ms-list" data-testid="milestones">
      <div class="ms-list-head">
        <button class="btn small primary" onClick={() => void newMilestone()} data-testid="milestone-new">
          <Icon name="plus" size={12} /> {t('New milestone')}
        </button>
        <span class="muted small">{t('A milestone gathers the tickets of one feature, with its spec and its user stories; you validate it.')}</span>
        <span class="grow" />
        <Show when={closed().length}>
          <button class="btn small" classList={{ on: showClosed() }} onClick={() => setShowClosed(!showClosed())} data-testid="milestones-closed">
            {t('Validated and abandoned ({n})', { n: closed().length })}
          </button>
        </Show>
      </div>
      <Show when={open().length || (showClosed() && closed().length)} fallback={<EmptyState icon="flag" text={t('No milestone yet.')} action={t('New milestone')} onAction={() => void newMilestone()} />}>
        <div class="ms-grid">
          <For each={open()}>{(m) => <MilestoneCard m={m} />}</For>
          <Show when={showClosed()}>
            <For each={closed()}>{(m) => <MilestoneCard m={m} />}</For>
          </Show>
        </div>
      </Show>
    </div>
  )
}

function MilestoneCard(props: { m: MilestoneSummary }) {
  const m = () => props.m
  const pct = () => (m().tickets ? Math.round((m().ticketsDone / m().tickets) * 100) : 0)
  return (
    <button class="ms-card" onClick={() => openMilestone(m().id)} data-testid={`milestone-card-${m().id}`}>
      <div class="ms-card-top">
        <Icon name="flag" size={13} />
        <span class="ms-card-title ellipsis">{m().title}</span>
        <span class={`kb-status ms-${m().state}`}>{t(milestoneStateNames[m().state])}</span>
      </div>
      <div class="ms-bar" title={t('Tickets finished {done}/{n}', { done: m().ticketsDone, n: m().tickets })}>
        <span style={{ width: `${pct()}%` }} />
      </div>
      <div class="ms-card-meta small" data-testid="milestone-card-meta">
        <span>{t('Tickets finished {done}/{n}', { done: m().ticketsDone, n: m().tickets })}</span>
        <span>{t('User stories validated {done}/{n}', { done: m().storiesDone, n: m().stories })}</span>
        <Show when={m().toTest}>
          <span class="st-to_test ms-totest-count">{t('{n} to test', { n: m().toTest })}</span>
        </Show>
        <Show when={m().feedbackOpen}>
          <span class="warn">
            <Icon name="comment" size={11} /> {m().feedbackOpen}
          </span>
        </Show>
      </div>
    </button>
  )
}
