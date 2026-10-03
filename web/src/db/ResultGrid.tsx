import { createSignal, For, Show } from 'solid-js'
import type { Result } from './api'
import { t } from '../i18n'

function cell(v: unknown) {
  if (v === null || v === undefined) return null
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

/** Result grid: sticky header, row numbers, NULL shown apart, Ctrl+C copies the cell. */
export function ResultGrid(props: { result: Result; offset?: number }) {
  const [sel, setSel] = createSignal<[number, number] | null>(null)
  const copy = (e: KeyboardEvent) => {
    const s = sel()
    if (!s || !(e.ctrlKey || e.metaKey) || e.code !== 'KeyC') return
    const v = cell(props.result.rows?.[s[0]]?.[s[1]])
    navigator.clipboard.writeText(v ?? 'NULL')
    e.preventDefault()
  }
  return (
    <div class="grid-wrap" tabIndex={0} onKeyDown={copy}>
      <Show when={props.result.columns?.length} fallback={<div class="muted pad">{t('No column returned.')}</div>}>
        <table class="grid">
          <thead>
            <tr>
              <th class="rownum">#</th>
              <For each={props.result.columns ?? []}>{(c) => <th title={c}>{c}</th>}</For>
            </tr>
          </thead>
          <tbody>
            <For each={props.result.rows ?? []}>
              {(row, r) => (
                <tr>
                  <td class="rownum">{(props.offset ?? 0) + r() + 1}</td>
                  <For each={row}>
                    {(v, c) => {
                      const text = cell(v)
                      return (
                        <td
                          classList={{ null: text === null, selected: sel()?.[0] === r() && sel()?.[1] === c(), num: typeof v === 'number' }}
                          title={text ?? 'NULL'}
                          onClick={() => setSel([r(), c()])}
                        >
                          {text === null ? 'NULL' : text.length > 300 ? text.slice(0, 300) + '…' : text}
                        </td>
                      )
                    }}
                  </For>
                </tr>
              )}
            </For>
          </tbody>
        </table>
      </Show>
    </div>
  )
}
