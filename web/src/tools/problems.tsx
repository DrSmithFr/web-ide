// Problems tool: the diagnostics of the language servers and their output. In the core until
// the language servers become a plugin (Languages).
import { createEffect, createSignal, For, Show } from 'solid-js'
import { on as onPod } from '../pod/rpc'
import { diagnostics, mutate, openFile, relPath, session } from '../state/project'
import { EmptyState } from '../ui/EmptyState'
import { Icon } from '../ui/icons'
import { t } from '../i18n'

const [logs, setLogs] = createSignal<string[]>([])

onPod('lsp.log', (e: { lang: string; method: string; params: { type: number; message: string } }) => {
  const kind = t(['', 'error', 'warn.', 'info', 'log'][e.params?.type] ?? '')
  setLogs((l) => [...l.slice(-999), `[${e.lang}] ${kind} ${e.params?.message ?? ''}`])
})
onPod('lsp.status', (e: { lang: string; running: boolean }) => {
  setLogs((l) => [...l.slice(-999), `[${e.lang}] ${e.running ? t('server started') : t('server stopped')}`])
})

function Problems() {
  const list = () =>
    Object.entries(diagnostics)
      .flatMap(([path, ds]) => (ds ?? []).map((d) => ({ path, d })))
      .sort((a, b) => (a.d.severity ?? 4) - (b.d.severity ?? 4))
  return (
    <div class="problems">
      <For each={list()} fallback={<EmptyState icon="check" text={t('No problem reported by the language servers.')} />}>
        {({ path, d }) => (
          <div class="problem" onClick={() => openFile({ path, line: d.range.start.line, col: d.range.start.character })}>
            <span class={`sev sev-${d.severity ?? 3}`}>{d.severity === 1 ? t('error') : d.severity === 2 ? t('warn.') : t('info')}</span>
            <span class="problem-msg">{d.message}</span>
            <span class="muted small mono">
              {relPath(path)}:{d.range.start.line + 1}
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
  const tab = () => session.bottom.problemsTab
  const setTab = (id: 'problems' | 'output') => mutate((s) => (s.bottom.problemsTab = id))
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
