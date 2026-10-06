// Roadmap of the kanban: the lineages still to merge as rows of blocks, like the tracks of
// a video editor, to see at a glance what can start and how big the work ahead is. Read
// only: a click opens the ticket.
import { createMemo, For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { EmptyState } from '../ui/EmptyState'
import { layoutRoadmap, type Block, type BlockState } from './roadmap'
import { openTicket, sizeNames, statusLabels, type Summary } from './state'
import { blockerText } from './Lineage'
import { t } from '../i18n'

/** Pixels of one size unit and of one row. */
const UNIT = 56
const ROW = 40

const stateLabels: Record<BlockState, string> = {
  ready: 'Ready to start',
  blocked: 'Blocked',
  new: 'No plan yet',
  active: 'In progress or to test',
  done: 'Done',
}

function tooltip(b: Block) {
  const s = b.ticket
  const lines = [`#${s.id} ${s.title}`, `${statusLabels[s.status]} · ${s.size ? t('Size {size}', { size: sizeNames[s.size] }) : t('Size not estimated')}`]
  if (s.blockers?.length) lines.push(t('Cannot start yet: {blockers}', { blockers: s.blockers.map(blockerText).join(', ') }))
  return lines.join('\n')
}

export function Roadmap(props: { tickets: Summary[]; query: string }) {
  const layout = createMemo(() => layoutRoadmap(props.tickets))
  const match = (s: Summary) => {
    const q = props.query.trim().toLowerCase()
    return !q || s.title.toLowerCase().includes(q) || `#${s.id}` === q || String(s.id) === q
  }
  // A row is shown when one of its blocks matches the filter; arrows only between shown rows.
  const shown = createMemo(() => layout().rows.map((r, i) => ({ r, i })).filter(({ r }) => r.blocks.some((b) => match(b.ticket))))
  const place = createMemo(() => new Map(shown().map(({ i }, k) => [i, k])))
  const arrows = createMemo(() => layout().arrows.filter((a) => place().has(a.from.row) && place().has(a.to.row)))
  const width = () => Math.max(...shown().map(({ r }) => r.blocks[r.blocks.length - 1].x + r.blocks[r.blocks.length - 1].w), 4) * UNIT + 24

  // Arrows move between blocks: ←/→ in a row, ↑/↓ to the nearest block of the next row.
  const onKey = (e: KeyboardEvent) => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('[data-row]')
    if (!el || !['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(e.key)) return
    const row = Number(el.dataset.row)
    const x = Number(el.dataset.x)
    const root = el.closest('.rm')!
    const inRow = (r: number) => [...root.querySelectorAll<HTMLElement>(`[data-row="${r}"]`)]
    let next: HTMLElement | undefined
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      const list = inRow(row)
      next = list[list.indexOf(el) + (e.key === 'ArrowLeft' ? -1 : 1)]
    } else {
      const list = inRow(row + (e.key === 'ArrowUp' ? -1 : 1))
      next = list.reduce<HTMLElement | undefined>((best, b) => (!best || Math.abs(Number(b.dataset.x) - x) < Math.abs(Number(best.dataset.x) - x) ? b : best), undefined)
    }
    if (next) {
      e.preventDefault()
      next.focus()
    }
  }

  return (
    <div class="rm" data-testid="roadmap" onKeyDown={onKey}>
      <div class="rm-legend small">
        <For each={['ready', 'active', 'blocked', 'new', 'done'] as BlockState[]}>
          {(s) => (
            <span class="rm-legend-item">
              <span class={`rm-swatch ${s}`} /> {t(stateLabels[s])}
            </span>
          )}
        </For>
        <span class="muted">{t('Width: estimated size (S, M, L, XL)')}</span>
      </div>
      <Show when={shown().length} fallback={<EmptyState icon="kanban" text={t('Nothing to plan: no open ticket.')} />}>
        <div class="rm-body">
          <div class="rm-labels">
            <For each={shown()}>
              {({ r }) => (
                <button class="rm-label link ellipsis" style={{ height: `${ROW}px` }} title={`#${r.root.id} ${r.root.title}`} onClick={() => openTicket(r.root.id)}>
                  <span class="mono">#{r.root.id}</span> <span class="rm-label-title">{r.root.title}</span>
                </button>
              )}
            </For>
          </div>
          <div class="rm-scroll">
            <div class="rm-tracks" style={{ width: `${width()}px`, height: `${shown().length * ROW}px` }}>
              <svg class="rm-arrows" width={width()} height={shown().length * ROW}>
                <defs>
                  <marker id="rm-head" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
                    <path d="M0 0L8 4L0 8z" fill="currentColor" />
                  </marker>
                </defs>
                <For each={arrows()}>
                  {(a) => {
                    const x1 = a.from.x * UNIT
                    const y1 = place().get(a.from.row)! * ROW + ROW / 2
                    const x2 = a.to.x * UNIT + 2
                    const y2 = place().get(a.to.row)! * ROW + ROW / 2
                    // From the end of the lineage waited for, down (or up) to the start of the block.
                    const ya = y1 + (y2 > y1 ? ROW / 2 - 6 : -ROW / 2 + 6)
                    return <path class="rm-arrow" d={`M${x1 - 6} ${ya}C${x1 - 6} ${y2} ${x1 - 6} ${y2} ${x2} ${y2}`} marker-end="url(#rm-head)" data-testid="roadmap-arrow" />
                  }}
                </For>
              </svg>
              <For each={shown()}>
                {({ r }, k) => (
                  <For each={r.blocks}>
                    {(b) => (
                      <button
                        class={`rm-block ${b.state} st-${b.ticket.status}`}
                        classList={{ dim: !match(b.ticket), unsized: !b.estimated }}
                        style={{ left: `${b.x * UNIT + 2}px`, top: `${k() * ROW + 4}px`, width: `${b.w * UNIT - 4}px`, height: `${ROW - 8}px` }}
                        title={tooltip(b)}
                        data-row={k()}
                        data-x={b.x}
                        data-testid={`roadmap-block-${b.ticket.id}`}
                        data-state={b.state}
                        onClick={() => openTicket(b.ticket.id)}
                      >
                        <Show when={b.state === 'ready'}>
                          <Icon name="play" size={10} />
                        </Show>
                        <Show when={b.state === 'blocked'}>
                          <Icon name="lock" size={10} />
                        </Show>
                        <Show when={b.state === 'done'}>
                          <Icon name="check" size={10} />
                        </Show>
                        <span class="mono">#{b.ticket.id}</span>
                        <span class="rm-block-title ellipsis">{b.ticket.title}</span>
                        <Show when={b.ticket.size}>
                          <span class="rm-size">{sizeNames[b.ticket.size!]}</span>
                        </Show>
                      </button>
                    )}
                  </For>
                )}
              </For>
            </div>
          </div>
        </div>
      </Show>
    </div>
  )
}
