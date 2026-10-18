// Milestones view of the board: the open objectives in their order (date, progress, ↑ ↓), each
// with the cards of the milestones it is the default objective of, then the milestones without
// one. A card shows the progress of a milestone (tickets finished, stories validated, tickets
// to test, open feedback); a click opens its page.
import { createSignal, For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { EmptyState } from '../ui/EmptyState'
import { board, openMilestone, openObjective } from './state'
import { createMilestone, milestoneStateNames, type MilestoneSummary } from './milestones'
import { moveObjective, objectiveStateNames, type ObjectiveSummary } from './objectives'
import { fmtDue, newObjective } from './ObjectiveView'
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
  const openObjectives = () => board.objectives.filter((o) => o.state === 'open')
  const closedObjectives = () => board.objectives.filter((o) => o.state !== 'open')
  const open = () => board.milestones.filter((m) => m.state === 'open')
  const closed = () => board.milestones.filter((m) => m.state !== 'open')
  // A milestone whose default objective is closed or gone goes with those without one.
  const of = (o: number) => open().filter((m) => m.objective === o)
  const lone = () => open().filter((m) => !m.objective || !openObjectives().some((o) => o.id === m.objective))
  const hidden = () => closed().length + closedObjectives().length
  return (
    <div class="ms-list" data-testid="milestones">
      <div class="ms-list-head">
        <button class="btn small primary" onClick={() => void newMilestone()} data-testid="milestone-new">
          <Icon name="plus" size={12} /> {t('New milestone')}
        </button>
        <button class="btn small" onClick={() => void newObjective(true)} data-testid="objective-new">
          <Icon name="target" size={12} /> {t('New objective')}
        </button>
        <span class="muted small">{t('A milestone gathers the tickets of one feature; an objective, a deadline across milestones (MVP, a launch…). You validate both.')}</span>
        <span class="grow" />
        <Show when={hidden()}>
          <button class="btn small" classList={{ on: showClosed() }} onClick={() => setShowClosed(!showClosed())} data-testid="milestones-closed">
            {t('Closed ({n})', { n: hidden() })}
          </button>
        </Show>
      </div>
      <Show
        when={board.milestones.length || board.objectives.length}
        fallback={<EmptyState icon="flag" text={t('No milestone yet.')} action={t('New milestone')} onAction={() => void newMilestone()} />}
      >
        <For each={openObjectives()}>
          {(o, i) => (
            <section class="ob-section" data-testid={`objective-section-${o.id}`}>
              <ObjectiveHead o={o} first={i() === 0} last={i() === openObjectives().length - 1} />
              <Show when={of(o.id).length} fallback={<p class="muted small">{t('No milestone gives it its tickets: choose it as their default objective, or in the tickets.')}</p>}>
                <div class="ms-grid">
                  <For each={of(o.id)}>{(m) => <MilestoneCard m={m} />}</For>
                </div>
              </Show>
            </section>
          )}
        </For>
        <Show when={lone().length}>
          <section class="ob-section" data-testid="objective-section-none">
            <Show when={openObjectives().length}>
              <div class="ob-head">
                <span class="muted">{t('No objective')}</span>
              </div>
            </Show>
            <div class="ms-grid">
              <For each={lone()}>{(m) => <MilestoneCard m={m} />}</For>
            </div>
          </section>
        </Show>
        <Show when={showClosed() && hidden()}>
          <section class="ob-section">
            <div class="ob-head">
              <span class="muted">{t('Closed')}</span>
            </div>
            <For each={closedObjectives()}>{(o) => <ObjectiveHead o={o} first last />}</For>
            <div class="ms-grid">
              <For each={closed()}>{(m) => <MilestoneCard m={m} />}</For>
            </div>
          </section>
        </Show>
      </Show>
    </div>
  )
}

function ObjectiveHead(props: { o: ObjectiveSummary; first: boolean; last: boolean }) {
  const o = () => props.o
  const pct = () => (o().tickets ? Math.round((o().ticketsDone / o().tickets) * 100) : 0)
  const move = (delta: number) => void moveObjective(o().id, delta).catch(errorToast)
  return (
    <div class="ob-head" data-testid={`objective-head-${o().id}`}>
      <Icon name="target" size={14} />
      <button class="link ob-title ellipsis" onClick={() => openObjective(o().id)} data-testid="objective-open">
        {o().title}
      </button>
      <Show when={o().state !== 'open'}>
        <span class={`kb-status ms-${o().state === 'reached' ? 'validated' : o().state}`}>{t(objectiveStateNames[o().state])}</span>
      </Show>
      <Show when={o().due}>
        <span class="badge" title={t('Date of the objective')}>
          <Icon name="clock" size={10} /> {fmtDue(o().due!)}
        </span>
      </Show>
      <div class="ms-bar ob-head-bar" title={t('Tickets finished {done}/{n}', { done: o().ticketsDone, n: o().tickets })}>
        <span style={{ width: `${pct()}%` }} />
      </div>
      <span class="muted small" data-testid="objective-head-progress">
        {t('Tickets finished {done}/{n}', { done: o().ticketsDone, n: o().tickets })}
      </span>
      <Show when={o().toTest}>
        <span class="st-to_test ms-totest-count small">{t('{n} to test', { n: o().toTest })}</span>
      </Show>
      <span class="grow" />
      <Show when={o().state === 'open'}>
        <button class="icon-btn small" title={t('Move up')} disabled={props.first} onClick={() => move(-1)} data-testid="objective-up">
          <Icon name="up" size={11} />
        </button>
        <button class="icon-btn small" title={t('Move down')} disabled={props.last} onClick={() => move(1)} data-testid="objective-down">
          <Icon name="down" size={11} />
        </button>
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
