import { Show } from 'solid-js'
import './switch.css'

/** Small on/off switch with a label, for the menus. */
export function Switch(props: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void; testid?: string }) {
  return (
    <label class="ai-switch-row" data-testid={props.testid}>
      <span class="grow">
        <span>{props.label}</span>
        <Show when={props.hint}>
          <span class="ai-switch-hint">{props.hint}</span>
        </Show>
      </span>
      <input type="checkbox" class="ai-switch" checked={props.checked} onChange={(e) => props.onChange(e.currentTarget.checked)} />
    </label>
  )
}
