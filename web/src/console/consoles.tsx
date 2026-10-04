// Console tool: terminals and tasks run by the pod (they survive reloads), one xterm instance
// per console, kept while the page lives. Problems tool: diagnostics and language server output.
import { createEffect, createSignal, For, on, onCleanup, onMount, Show } from 'solid-js'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { notify, on as onPod, request } from '../pod/rpc'
import { diagnostics, mutate, openFile, project, relPath, root, session } from '../state/project'
import { showTool } from '../state/zones'
import { settings } from '../state/settings'
import { themeById } from '../settings/themes'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { shortcutOf } from '../keys/bindings'
import { t } from '../i18n'

export interface ConsoleInfo {
  id: string
  title: string
  kind: 'terminal' | 'task'
  command?: string[]
  exited: boolean
  code: number
}

const [consoles, setConsoles] = createSignal<ConsoleInfo[]>([])
const [logs, setLogs] = createSignal<string[]>([])
export { consoles }

export function setConsoleList(list: ConsoleInfo[]) {
  setConsoles(list)
  // Forget the terminals that do not exist in the pod anymore.
  for (const id of terms.keys()) if (!list.some((c) => c.id === id)) disposeTerm(id)
}

onPod('console.created', (c: ConsoleInfo) => setConsoles((l) => (l.some((x) => x.id === c.id) ? l : [...l, c])))
onPod('console.closed', (e: { id: string }) => {
  setConsoles((l) => l.filter((c) => c.id !== e.id))
  disposeTerm(e.id)
})
onPod('console.renamed', (e: { id: string; title: string }) => setConsoles((l) => l.map((c) => (c.id === e.id ? { ...c, title: e.title } : c))))
onPod('console.exit', (e: { id: string; code: number }) => {
  setConsoles((l) => l.map((c) => (c.id === e.id ? { ...c, exited: true, code: e.code } : c)))
  terms.get(e.id)?.term.write(`\r\n\x1b[2m[${t('process exited, code {code}', { code: e.code })}]\x1b[0m\r\n`)
})
onPod('console.output', (e: { id: string; data: string }) => {
  const t = terms.get(e.id)
  if (t?.ready) t.term.write(b64(e.data))
  else t?.queue.push(e.data)
})
onPod('lsp.log', (e: { lang: string; method: string; params: { type: number; message: string } }) => {
  const kind = t(['', 'error', 'warn.', 'info', 'log'][e.params?.type] ?? '')
  setLogs((l) => [...l.slice(-999), `[${e.lang}] ${kind} ${e.params?.message ?? ''}`])
})
onPod('lsp.status', (e: { lang: string; running: boolean }) => {
  setLogs((l) => [...l.slice(-999), `[${e.lang}] ${e.running ? t('server started') : t('server stopped')}`])
})

function b64(s: string) {
  return Uint8Array.from(atob(s), (c) => c.charCodeAt(0))
}

// ---------- xterm instances ----------

interface TermEntry {
  term: Terminal
  fit: FitAddon
  el: HTMLDivElement
  ready: boolean
  queue: string[]
}
const terms = new Map<string, TermEntry>()

function xtermTheme() {
  const t = themeById(settings.theme).ui
  return { background: t['bg-2'], foreground: t.fg, cursor: t.accent, selectionBackground: t['sel-bg'], black: t['bg-3'] }
}

function getTerm(id: string): TermEntry {
  let te = terms.get(id)
  if (te) return te
  const el = document.createElement('div')
  el.className = 'term-host'
  const term = new Terminal({
    fontFamily: settings.font.family,
    fontSize: settings.font.size,
    theme: xtermTheme(),
    scrollback: 10000,
    allowProposedApi: true,
    cursorBlink: true,
  })
  const fit = new FitAddon()
  term.loadAddon(fit)
  term.open(el)
  term.onData((data) => notify('console.input', { id, data }))
  term.onResize(({ cols, rows }) => notify('console.resize', { id, cols, rows }))
  te = { term, fit, el, ready: false, queue: [] }
  terms.set(id, te)
  const entry = te
  request('console.attach', { id })
    .then((r) => {
      if (r.data) term.write(b64(r.data))
      for (const d of entry.queue.splice(0)) term.write(b64(d))
      entry.ready = true
      if (r.info.exited) term.write(`\r\n\x1b[2m[${t('process exited, code {code}', { code: r.info.code })}]\x1b[0m\r\n`)
    })
    .catch(() => (entry.ready = true))
  return te
}

function disposeTerm(id: string) {
  const t = terms.get(id)
  if (!t) return
  t.term.dispose()
  t.el.remove()
  terms.delete(id)
}

createEffect(
  on(
    () => [settings.theme, settings.font.family, settings.font.size],
    () => {
      for (const t of terms.values()) {
        t.term.options.theme = xtermTheme()
        t.term.options.fontFamily = settings.font.family
        t.term.options.fontSize = settings.font.size
        try {
          t.fit.fit()
        } catch {
          /* hidden */
        }
      }
    },
    { defer: true },
  ),
)

/** Shows a console in a container element and keeps it fitted. */
export function TermView(props: { id: string; focus?: boolean }) {
  let host!: HTMLDivElement
  onMount(() => {
    const t = getTerm(props.id)
    host.appendChild(t.el)
    const fit = () => {
      if (host.offsetWidth > 0 && host.offsetHeight > 0) {
        try {
          t.fit.fit()
        } catch {
          /* not laid out yet */
        }
      }
    }
    const ro = new ResizeObserver(() => requestAnimationFrame(fit))
    ro.observe(host)
    requestAnimationFrame(() => {
      fit()
      if (props.focus) t.term.focus()
    })
    onCleanup(() => {
      ro.disconnect()
      t.el.remove()
    })
  })
  return <div class="term-container" ref={host} />
}

// ---------- actions ----------

export async function newConsole(o: { cwd?: string; command?: string[]; title?: string; kind?: 'terminal' | 'task' } = {}) {
  try {
    const info: ConsoleInfo = await request('console.create', { kind: o.kind ?? 'terminal', title: o.title, command: o.command, cwd: o.cwd, cols: 120, rows: 30 })
    setConsoles((l) => (l.some((x) => x.id === info.id) ? l : [...l, info]))
    mutate((s) => {
      showTool(s, 'console')
      s.bottom.active = info.id
    })
    return info
  } catch (e) {
    errorToast(e)
  }
}

export async function runTask() {
  const cmd = await prompt({ title: t('Run a command'), label: `Dans ${root()}`, placeholder: 'npm run build, go test ./…, make…' })
  if (!cmd?.trim()) return
  await newConsole({ kind: 'task', command: ['sh', '-c', cmd], title: cmd })
}

async function closeConsole(id: string) {
  try {
    await request('console.close', { id })
  } catch {
    /* already gone */
  }
  setConsoles((l) => l.filter((c) => c.id !== id))
  disposeTerm(id)
  if (session.bottom.active === id) mutate((s) => (s.bottom.active = consoles()[0]?.id ?? null))
}

async function renameConsole(c: ConsoleInfo) {
  const title = await prompt({ title: t('Rename the console'), value: c.title })
  if (title) request('console.rename', { id: c.id, title }).catch(errorToast)
}

function detach(id: string) {
  window.open(`/project/${project()!.id}/console/${id}`, `console-${id}`, 'popup,width=900,height=500')
}

// ---------- tools ----------

function Problems() {
  const list = () =>
    Object.entries(diagnostics)
      .flatMap(([path, ds]) => (ds ?? []).map((d) => ({ path, d })))
      .sort((a, b) => (a.d.severity ?? 4) - (b.d.severity ?? 4))
  return (
    <div class="problems">
      <For each={list()} fallback={<div class="muted pad">{t('No problem reported by the language servers.')}</div>}>
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

export function ConsoleTool() {
  // A console that no longer exists (pod restarted) falls back to the first one.
  const active = () => {
    const a = session.bottom.active
    return consoles().some((c) => c.id === a) ? a : (consoles()[0]?.id ?? null)
  }
  const setActive = (id: string) => mutate((s) => (s.bottom.active = id))

  return (
    <div class="panel">
      <div class="tool-tabs" role="tablist">
        <For each={consoles()}>
          {(c) => (
            <div
              class="btab"
              role="tab"
              aria-selected={active() === c.id}
              classList={{ active: active() === c.id, exited: c.exited }}
              onClick={() => setActive(c.id)}
              onDblClick={() => renameConsole(c)}
              onMouseDown={(e) => e.button === 1 && closeConsole(c.id)}
            >
              <Icon name={c.kind === 'task' ? 'play' : 'terminal'} size={13} />
              <span>{c.title}</span>
              <Show when={c.exited}>
                <span class={c.code === 0 ? 'ok' : 'danger'}>{c.code}</span>
              </Show>
              <button class="icon-btn tiny" title={t('Detach in a window')} onClick={(e) => (e.stopPropagation(), detach(c.id))}>
                <Icon name="external" size={11} />
              </button>
              <button class="icon-btn tiny" title={t('Close')} onClick={(e) => (e.stopPropagation(), closeConsole(c.id))}>
                ✕
              </button>
            </div>
          )}
        </For>
        <button class="icon-btn" title={`${t('New terminal')} (${shortcutOf('console.new')})`} onClick={() => newConsole()}>
          <Icon name="plus" />
        </button>
        <button class="icon-btn" title={t('Run a command (build output)')} onClick={runTask}>
          <Icon name="play" />
        </button>
      </div>
      <div class="tool-body">
        <Show when={consoles().length === 0}>
          <div class="muted pad">{t('No open console.')}</div>
        </Show>
        <For each={consoles()}>
          {(c) => (
            <div class="term-slot" style={{ display: active() === c.id ? 'block' : 'none' }}>
              <TermView id={c.id} focus={active() === c.id} />
            </div>
          )}
        </For>
      </div>
    </div>
  )
}

/** Number of errors reported by the language servers. */
export const problemCount = () => Object.values(diagnostics).reduce((n, l) => n + (l?.filter((d) => d.severity === 1).length ?? 0), 0)

export function ProblemsTool() {
  const tab = () => session.bottom.problemsTab
  const setTab = (id: 'problems' | 'output') => mutate((s) => (s.bottom.problemsTab = id))
  return (
    <div class="panel">
      <div class="tool-tabs" role="tablist">
        <div class="btab" role="tab" aria-selected={tab() === 'problems'} classList={{ active: tab() === 'problems' }} onClick={() => setTab('problems')}>
          {t('Problems')}
          <Show when={problemCount()}>
            <span class="badge danger">{problemCount()}</span>
          </Show>
        </div>
        <div class="btab" role="tab" aria-selected={tab() === 'output'} classList={{ active: tab() === 'output' }} onClick={() => setTab('output')}>
          {t('Output')}
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
