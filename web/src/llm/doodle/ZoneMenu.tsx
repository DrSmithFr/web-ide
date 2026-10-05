// Menu of a zone picked with the layout tool: split it in columns, rows, a grid or a border
// layout, merge its parts, name it, delete it (its neighbor takes its place), or delete the
// layout.
import { For, Show } from 'solid-js'
import { t } from '../../i18n'
import { borderZone, gridZone, splitZone, type Zone } from './model'

export function ZoneMenu(props: {
  at: { left: number; top: number; width: number; height: number }
  zone: Zone
  root: boolean
  onChange: (f: (z: Zone) => Zone) => void
  onName: () => void
  onDelete: () => void
  onRemove: () => void
}) {
  const btn = (label: string, title: string, testid: string, f: (z: Zone) => Zone) => (
    <button title={title} onClick={() => props.onChange(f)} data-testid={testid}>
      {label}
    </button>
  )
  return (
    <div class="dd-zone-menu" style={{ left: `${Math.max(4, props.at.left + 4)}px`, top: `${Math.max(4, props.at.top + 4)}px` }} onPointerDown={(e) => e.stopPropagation()} data-testid="dd-zone-menu">
      <div class="dd-zone-row">
        <span class="dd-zone-label">{t('Columns')}</span>
        <For each={[2, 3, 4]}>{(n) => btn(String(n), t('Split in {n} columns', { n }), `dd-cols-${n}`, (z) => splitZone(z, 'cols', n))}</For>
      </div>
      <div class="dd-zone-row">
        <span class="dd-zone-label">{t('Rows')}</span>
        <For each={[2, 3, 4]}>{(n) => btn(String(n), t('Split in {n} rows', { n }), `dd-rows-${n}`, (z) => splitZone(z, 'rows', n))}</For>
      </div>
      <div class="dd-zone-row">
        <span class="dd-zone-label">{t('Grid')}</span>
        {btn('2×2', t('Grid of {n}', { n: '2×2' }), 'dd-grid-2', (z) => gridZone(z, 2, 2))}
        {btn('3×3', t('Grid of {n}', { n: '3×3' }), 'dd-grid-3', (z) => gridZone(z, 3, 3))}
        {btn(t('Border'), t('North, south, west, east and center'), 'dd-border', borderZone)}
      </div>
      <div class="dd-zone-row actions">
        <button title={t('Name the zone (double click)')} onClick={props.onName} data-testid="dd-zone-rename">
          {t('Name…')}
        </button>
        <Show when={props.zone.split}>
          <button title={t('Merge the parts of this zone')} onClick={() => props.onChange((z) => ({ ...z, split: undefined }))} data-testid="dd-unsplit">
            {t('Merge')}
          </button>
        </Show>
        <Show when={!props.root}>
          <button class="danger" title={t('Its neighbor takes its place')} onClick={props.onRemove} data-testid="dd-zone-delete">
            {t('Delete the zone')}
          </button>
        </Show>
        <Show when={props.root}>
          <button class="danger" title={t('Delete the layout')} onClick={props.onDelete} data-testid="dd-layout-delete">
            {t('Delete the layout')}
          </button>
        </Show>
      </div>
    </div>
  )
}
