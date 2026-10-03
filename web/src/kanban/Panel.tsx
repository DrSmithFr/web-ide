// Side panel of the kanban: the open tickets of the project, grouped by status.
import { For, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { Card } from './Board'
import { board, ensureBoard, openBoard, refreshBoard, setNewTicketOpen, statusLabels, type Status } from './state'
import './kanban.css'

const shown: Status[] = ['in_progress', 'fix', 'review', 'ready', 'new']

export function KanbanPanel() {
  onMount(ensureBoard)
  const of = (s: Status) => board.tickets.filter((t) => t.status === s)
  return (
    <div class="panel" data-testid="kanban-panel">
      <div class="panel-head">
        <span class="panel-title">Kanban</span>
        <span class="grow" />
        <button class="icon-btn" title="Nouveau ticket" onClick={() => setNewTicketOpen(true)}>
          <Icon name="plus" size={14} />
        </button>
        <button class="icon-btn" title="Rafraîchir" onClick={() => refreshBoard()}>
          <Icon name="refresh" size={14} />
        </button>
        <button class="icon-btn" title="Ouvrir le tableau" onClick={openBoard} data-testid="kanban-open-board">
          <Icon name="kanban" size={14} />
        </button>
      </div>
      <div class="panel-body kb-panel">
        <Show when={board.error}>
          <p class="danger pad small">{board.error}</p>
        </Show>
        <Show when={board.loaded && !shown.some((s) => of(s).length)}>
          <p class="muted pad small">Aucun ticket ouvert.</p>
        </Show>
        <For each={shown}>
          {(s) => (
            <Show when={of(s).length}>
              <div class="kb-panel-group">
                <span class={`kb-dot st-${s}`} /> {statusLabels[s]} <span class="kb-count">{of(s).length}</span>
              </div>
              <For each={of(s)}>{(t) => <Card t={t} compact />}</For>
            </Show>
          )}
        </For>
      </div>
    </div>
  )
}
