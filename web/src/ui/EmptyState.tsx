// Empty state of a tool: an icon, a sentence and, optionally, the action that fills it.
import { Show, type JSX } from 'solid-js'
import { Icon } from './icons'

export function EmptyState(props: { icon: string; text: JSX.Element; action?: string; onAction?: () => void; testid?: string }) {
  return (
    <div class="empty-state" data-testid={props.testid}>
      <Icon name={props.icon} size={28} />
      <p>{props.text}</p>
      <Show when={props.action}>
        <button class="btn small" onClick={() => props.onAction?.()}>
          {props.action}
        </button>
      </Show>
    </div>
  )
}
