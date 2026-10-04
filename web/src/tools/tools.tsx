// Tools of the right panel (besides the Database explorer and Infos): conflicts, structure
// of the active file.
import { createEffect, createResource, createSignal, For, on, onCleanup, Show } from 'solid-js'
import { activeTab, conflictedDocs, docsVersion, getDoc, openFile, relPath } from '../state/project'
import { openConflict } from '../conflict/ConflictDialog'
import * as lspc from '../lsp/client'
import { shortcutOf } from '../keys/bindings'
import { t } from '../i18n'

export function ConflictsTool() {
  const list = () => (docsVersion(), conflictedDocs())
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Conflicts')}</span>
      </div>
      <div class="panel-body pad">
        <p class="muted small">
          {t('Files changed on disk (by the AI or another tool) while you were editing them, and whose automatic merge failed. Resolution: {shortcut}.', { shortcut: shortcutOf('conflict.resolve') })}
        </p>
        <For each={list()} fallback={<p class="muted">{t('No conflict.')}</p>}>
          {(d) => (
            <div class="conflict-item">
              <button class="link" onClick={() => openFile(d.path)}>
                {relPath(d.path)}
              </button>
              <span class="muted small">{t('remote rev. {rev}', { rev: d.conflict()?.rev ?? '' })}</span>
              <button class="btn small primary" onClick={() => openConflict(d)}>
                {t('Resolve')}
              </button>
            </div>
          )}
        </For>
      </div>
    </div>
  )
}

export function StructureTool() {
  const path = () => (activeTab()?.kind === 'file' ? activeTab()!.path! : '')
  const [tick, setTick] = createSignal(0)
  let timer: number | undefined
  createEffect(
    on([path, docsVersion], () => {
      const d = path() ? getDoc(path()) : null
      if (!d) return
      const off = d.onChange(() => {
        clearTimeout(timer)
        timer = window.setTimeout(() => setTick((t) => t + 1), 1200)
      })
      onCleanup(off)
    }),
  )
  const [symbols] = createResource(
    () => (path() ? { p: path(), t: tick(), v: docsVersion() } : null),
    async ({ p }) => {
      try {
        return { list: lspc.flatten(await lspc.documentSymbols(p)), error: '' }
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
          ↻
        </button>
      </div>
      <div class="panel-body tree">
        <Show when={path()} fallback={<p class="muted pad">{t('No active file.')}</p>}>
          <Show when={symbols()?.error}>
            <p class="muted pad small">{symbols()!.error}</p>
          </Show>
          <For each={symbols()?.list ?? []}>
            {({ s, depth }) => (
              <div
                class="tree-row"
                style={{ 'padding-left': `${depth * 14 + 8}px` }}
                onClick={() => openFile({ path: path(), line: s.selectionRange.start.line, col: s.selectionRange.start.character })}
              >
                <span class="sym-kind" title={t(lspc.symbolKinds[s.kind]?.[0] ?? "")}>
                  {lspc.symbolKinds[s.kind]?.[1] ?? '·'}
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
