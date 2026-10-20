// Problems tool: the diagnostics of the language servers and their output.
import { createEffect, createSignal, For, Show } from 'solid-js'
import type { LspDiagnostic } from '@ide/capabilities'
import { createStore } from 'solid-js/store'
import { api, desk, EmptyState, Icon, t } from './plugin'

const [logs, setLogs] = createSignal<string[]>([])

// The diagnostics of each file, as the servers publish them.
const [diagnostics, setDiagnostics] = createStore<Record<string, LspDiagnostic[]>>({})
export { diagnostics }

/** Starts following the servers: their diagnostics, their logs and their state. */
export function watchServers() {
  api.on('lsp.diagnostics', (e: { params: { uri: string; diagnostics: LspDiagnostic[] } }) => {
    setDiagnostics(api.project.pathFromUri(e.params.uri), e.params.diagnostics ?? [])
  })
  api.on('lsp.log', (e: { lang: string; method: string; params: { type: number; message: string } }) => {
    const kind = t(['', 'error', 'warn.', 'info', 'log'][e.params?.type] ?? '')
    setLogs((l) => [...l.slice(-999), `[${e.lang}] ${kind} ${e.params?.message ?? ''}`])
  })
  api.on('lsp.status', (e: { lang: string; running: boolean }) => {
    setLogs((l) => [...l.slice(-999), `[${e.lang}] ${e.running ? t('server started') : t('server stopped')}`])
  })
}

function Problems() {
  const list = () =>
    Object.entries(diagnostics)
      .flatMap(([path, ds]) => (ds ?? []).map((d) => ({ path, d })))
      .sort((a, b) => (a.d.severity ?? 4) - (b.d.severity ?? 4))
  return (
    <div class="problems">
      <For each={list()} fallback={<EmptyState icon="check" text={t('No problem reported by the language servers.')} />}>
        {({ path, d }) => (
          <div class="problem" onClick={() => api.openFile({ path, line: d.range.start.line, col: d.range.start.character })}>
            <span class={`sev sev-${d.severity ?? 3}`}>{d.severity === 1 ? t('error') : d.severity === 2 ? t('warn.') : t('info')}</span>
            <span class="problem-msg">{d.message}</span>
            <span class="muted small mono">
              {api.project.relPath(path)}:{d.range.start.line + 1}
            </span>
          </div>
        )}
      </For>
    </div>
  )
}

function Output() {
  let el!: HTMLPreElement
  createEffect(() => {
    logs()
    queueMicrotask(() => el && (el.scrollTop = el.scrollHeight))
  })
  return (
    <pre class="output-log" ref={el}>
      {logs().join('\n') || t('Output of the language servers.')}
    </pre>
  )
}

/** Number of errors reported by the language servers. */
export const problemCount = () => Object.values(diagnostics).reduce((n, l) => n + (l?.filter((d) => d.severity === 1).length ?? 0), 0)

export function ProblemsTool() {
  const tab = () => desk.get().tab
  const setTab = (id: 'problems' | 'output') => desk.set((s) => (s.tab = id))
  return (
    <div class="panel">
      <div class="tabbar tool-tabbar" role="tablist">
        <div class="tab" role="tab" aria-selected={tab() === 'problems'} classList={{ active: tab() === 'problems' }} onClick={() => setTab('problems')}>
          <Icon name="problems" size={13} />
          <span class="tab-title">{t('Problems')}</span>
          <Show when={problemCount()}>
            <span class="badge danger">{problemCount()}</span>
          </Show>
        </div>
        <div class="tab" role="tab" aria-selected={tab() === 'output'} classList={{ active: tab() === 'output' }} onClick={() => setTab('output')}>
          <Icon name="outline" size={13} />
          <span class="tab-title">{t('Output')}</span>
        </div>
      </div>
      <div class="tool-body">
        <Show when={tab() === 'output'} fallback={<Problems />}>
          <Output />
        </Show>
      </div>
    </div>
  )
}
