// Chip naming the worktree of a file tab or a console when it is not the main folder:
// the ticket (#12) or the branch, in a colour of its own.
import { Show } from 'solid-js'
import { projectOfPath, worktreeChip } from '../state/project'

export function WorktreeChip(props: { project?: string; path?: string }) {
  const chip = () => worktreeChip(props.project ?? (props.path ? projectOfPath(props.path) : undefined))
  return (
    <Show when={chip()}>
      {(c) => (
        <span class="wt-chip" style={{ '--wt-hue': c().hue }} title={c().title} data-testid="worktree-chip">
          {c().label}
        </span>
      )}
    </Show>
  )
}
