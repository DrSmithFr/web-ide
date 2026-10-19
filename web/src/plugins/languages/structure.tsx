// Structure tool: the symbols of the active file (classes, functions, methods), from its server.
import { createEffect, createResource, createSignal, For, on, onCleanup, Show } from 'solid-js'
import { api, EmptyState, Icon, t } from './plugin'
import { documentSymbols, flatten, symbolKinds } from './client'

export function StructureTool() {
  const path = () => (api.activeFile() ?? '')
  const [tick, setTick] = createSignal(0)
  let timer: number | undefined
  createEffect(
    on([path, api.buffers.version], () => {
      const d = path() ? api.buffers.get(path()) : null
      if (!d) return
      const off = d.onChange(() => {
        clearTimeout(timer)
        timer = window.setTimeout(() => setTick((t) => t + 1), 1200)
      })
      onCleanup(off)
    }),
  )
  const [symbols] = createResource(
    () => (path() ? { p: path(), t: tick(), v: api.buffers.version() } : null),
    async ({ p }) => {
      try {
        return { list: flatten(await documentSymbols(p)), error: '' }
      } catch (e) {
        return { list: [], error: (e as Error).message }
      }
    },
  )
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Structure')}</span>
        <span class="grow" />
        <button class="icon-btn" title={t('Refresh')} onClick={() => setTick((t) => t + 1)}>
          <Icon name="refresh" size={14} />
        </button>
      </div>
      <div class="panel-body tree">
        <Show when={path()} fallback={<EmptyState icon="outline" text={t('No active file.')} />}>
          <Show when={symbols()?.error}>
            <p class="muted pad small">{symbols()!.error}</p>
          </Show>
          <For each={symbols()?.list ?? []}>
            {({ s, depth }) => (
              <div
                class="tree-row"
                style={{ 'padding-left': `${depth * 14 + 8}px` }}
                onClick={() => api.openFile({ path: path(), line: s.selectionRange.start.line, col: s.selectionRange.start.character })}
              >
                <span class="sym-kind" title={t(symbolKinds[s.kind]?.[0] ?? "")}>
                  {symbolKinds[s.kind]?.[1] ?? '·'}
                </span>
                <span class="tree-name">{s.name}</span>
                <Show when={s.detail}>
                  <span class="tree-detail">{s.detail}</span>
                </Show>
              </div>
            )}
          </For>
        </Show>
      </div>
    </div>
  )
}
