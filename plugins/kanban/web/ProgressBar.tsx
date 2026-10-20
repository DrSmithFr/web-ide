import { Show } from 'solid-js'
import { t } from './plugin'

// The progress of a milestone or an objective: the finished tickets, then the ones to test, over
// all its tickets.
export function ProgressBar(props: { done: number; toTest: number; total: number; class?: string }) {
  const pct = (n: number) => (props.total ? (n / props.total) * 100 : 0)
  const title = () =>
    t('Tickets finished {done}/{n}', { done: props.done, n: props.total }) + (props.toTest ? ' · ' + t('{n} to test', { n: props.toTest }) : '')
  return (
    <div class={`ms-bar ${props.class ?? ''}`} title={title()} data-testid="progress-bar">
      <span style={{ width: `${pct(props.done)}%` }} />
      <Show when={props.toTest}>
        <span class="ms-bar-totest st-to_test" style={{ width: `${pct(props.toTest)}%` }} />
      </Show>
    </div>
  )
}
