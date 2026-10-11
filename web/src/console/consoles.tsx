// Console tool: terminals and tasks run by the pod (they survive reloads), one xterm instance
// per console, kept while the page lives. Problems tool: diagnostics and language server output.
import { createEffect, createRoot, createSignal, For, on, onCleanup, onMount, Show } from 'solid-js'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { notifyIn, on as onPod, request, requestIn } from '../pod/rpc'
import { attached, diagnostics, home, mutate, openFile, project, relPath, root, session } from '../state/project'
import { WorktreeChip } from '../ui/WorktreeChip'
import { showTool } from '../state/zones'
import { dropClasses, dropIndex, moveItem, setDropAt } from '../ui/tabDrop'
import { settings } from '../state/settings'
import { accentOf, themeById } from '../settings/themes'
import { contextMenu, prompt, type MenuItem } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { copyText } from '../ui/clipboard'
import { Icon } from '../ui/icons'
import { EmptyState } from '../ui/EmptyState'
import { shortcutOf } from '../keys/bindings'
import { t } from '../i18n'
import { adHocCommand, commands, flagIcon, loadCommands, openApp, runCommand, runningConsole, servesApp } from './commands'
import { openCommands } from './CommandsModal'

export interface ConsoleInfo {
  id: string
  title: string
  kind: 'terminal' | 'task'
  command?: string[]
  exited: boolean
  code: number
  /** Worktree the console runs in (set by the page). */
  project?: string
  /** Saved command the task runs (see commands.ts). */
  commandId?: string
}

const [consoles, setConsoles] = createSignal<ConsoleInfo[]>([])
const [logs, setLogs] = createSignal<string[]>([])
export { consoles, setConsoles }

/** Consoles of a worktree of the window (its project by default). */
export function setConsoleList(list: ConsoleInfo[], pid = home()?.id) {
  const mine = list.map((c) => ({ ...c, project: pid }))
  setConsoles((l) => [...l.filter((c) => c.project !== pid), ...mine])
  // Forget the terminals that do not exist in the pod anymore.
  for (const id of terms.keys()) if (!consoles().some((c) => c.id === id)) disposeTerm(id)
}

/** Lists the consoles of every worktree attached to the window again (after a reconnection),
 *  and gets the output the open terminals missed: a pod restarted with a keeper still runs them. */
export async function refreshConsoles() {
  for (const pid of Object.keys(attached)) await requestIn<ConsoleInfo[]>(pid, 'console.list').then((l) => setConsoleList(l, pid), () => {})
  for (const [id, entry] of terms) attachTerm(id, entry, entry.next)
}

// The consoles of a worktree come with it and go with it.
createRoot(() =>
  createEffect(
    on(
      () => Object.keys(attached),
      (ids, prev) => {
        if (!prev) return
        for (const pid of ids) if (!prev.includes(pid) && pid !== home()?.id) requestIn<ConsoleInfo[]>(pid, 'console.list').then((l) => setConsoleList(l, pid), () => {})
        for (const pid of prev) if (!ids.includes(pid)) setConsoleList([], pid)
      },
    ),
  ),
)

const projectOf = (id: string) => consoles().find((c) => c.id === id)?.project

onPod('console.created', (c: ConsoleInfo, from) => setConsoles((l) => (l.some((x) => x.id === c.id) ? l : [...l, { ...c, project: from ?? home()?.id }])))
onPod('console.closed', (e: { id: string }) => {
  setConsoles((l) => l.filter((c) => c.id !== e.id))
  disposeTerm(e.id)
})
onPod('console.renamed', (e: { id: string; title: string }) => setConsoles((l) => l.map((c) => (c.id === e.id ? { ...c, title: e.title } : c))))
onPod('console.exit', (e: { id: string; code: number }) => {
  setConsoles((l) => l.map((c) => (c.id === e.id ? { ...c, exited: true, code: e.code } : c)))
  const entry = terms.get(e.id)
  if (entry) termExited(entry, e.code)
})
onPod('console.output', (e: { id: string; data: string; offset: number }) => {
  const t = terms.get(e.id)
  if (t?.ready) writeAt(t, e.offset, b64(e.data))
  else t?.queue.push(e)
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
  /** Output received while the scrollback is asked. */
  queue: { data: string; offset: number }[]
  /** Offset of the next byte of output expected. */
  next: number
  /** The end of the process is written. */
  exited?: boolean
}
const terms = new Map<string, TermEntry>()

/** Writes output at its offset: what the terminal already shows is skipped. */
function writeAt(entry: TermEntry, offset: number, data: Uint8Array) {
  if (offset < entry.next) data = data.subarray(Math.min(data.length, entry.next - offset))
  if (data.length) entry.term.write(data)
  entry.next = Math.max(entry.next, offset + data.length)
}

/** Gets the scrollback of a console (from the offset the terminal has, on a reconnection). */
function attachTerm(id: string, entry: TermEntry, from?: number) {
  entry.ready = false
  requestIn(projectOf(id), 'console.attach', from === undefined ? { id } : { id, from })
    .then((r) => {
      if (r.data) writeAt(entry, r.start ?? entry.next, b64(r.data))
      entry.next = Math.max(entry.next, r.offset ?? entry.next)
      for (const d of entry.queue.splice(0)) writeAt(entry, d.offset ?? entry.next, b64(d.data))
      entry.ready = true
      if (r.info.exited) termExited(entry, r.info.code)
    })
    .catch(() => (entry.ready = true))
}

function termExited(entry: TermEntry, code: number) {
  if (entry.exited) return
  entry.exited = true
  entry.term.write(`\r\n\x1b[2m[${t('process exited, code {code}', { code })}]\x1b[0m\r\n`)
}

function xtermTheme() {
  const th = themeById(settings.theme)
  const t = th.ui
  return { background: t['bg-2'], foreground: t.fg, cursor: accentOf(th, settings.accent)[0], selectionBackground: t['sel-bg'], black: t['bg-3'] }
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
  // Ctrl+click (Cmd+click) opens a link; a plain click stays a click in the terminal.
  term.loadAddon(new WebLinksAddon((e, uri) => (e.ctrlKey || e.metaKey) && window.open(uri, '_blank', 'noopener')))
  // Ctrl+C copies the selection instead of interrupting the command; Ctrl+Shift+C always copies.
  term.attachCustomKeyEventHandler((e) => {
    if (!e.ctrlKey || e.altKey || e.metaKey || e.key.toLowerCase() !== 'c') return true
    if (!term.hasSelection()) return !e.shiftKey
    if (e.type === 'keydown') {
      copyText(term.getSelection())
      term.clearSelection()
    }
    e.preventDefault()
    return false
  })
  term.open(el)
  term.onData((data) => notifyIn(projectOf(id), 'console.input', { id, data }))
  term.onResize(({ cols, rows }) => notifyIn(projectOf(id), 'console.resize', { id, cols, rows }))
  te = { term, fit, el, ready: false, queue: [], next: 0 }
  terms.set(id, te)
  attachTerm(id, te)
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
    () => [settings.theme, settings.accent, settings.font.family, settings.font.size],
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
    const info: ConsoleInfo = { ...(await request('console.create', { kind: o.kind ?? 'terminal', title: o.title, command: o.command, cwd: o.cwd, cols: 120, rows: 30 })), project: project()?.id }
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

/** Menu of the ▶ button: the saved commands, an ad hoc command, the modal. */
async function commandsMenu(button: HTMLElement) {
  await loadCommands()
  const r = button.getBoundingClientRect()
  const at = { clientX: r.left, clientY: r.bottom + 2, preventDefault() {}, stopPropagation() {} } as MouseEvent
  const adHoc = adHocCommand()
  const items: MenuItem[] = commands().map((c) => ({ label: c.name, hint: (runningConsole(c.id) ? '● ' : '') + c.flag, action: () => void runCommand(c) }))
  // The apps of the serve commands with a port, opened as previews.
  for (const c of commands().filter(servesApp)) items.push({ label: t('Open “{name}” in a preview', { name: c.name }), hint: `:${c.port}`, action: () => void openApp(c) })
  if (items.length) items.push({ label: '', separator: true })
  items.push({ label: t('Run a command…'), action: () => void runTask() })
  if (adHoc) items.push({ label: t('Save “{command}” as a command…', { command: adHoc.length > 40 ? adHoc.slice(0, 40) + '…' : adHoc }), action: () => openCommands({ draft: { name: adHoc.split('\n')[0].slice(0, 40), command: adHoc } }) })
  items.push({ label: t('Edit commands…'), action: () => openCommands() })
  contextMenu(at, items)
}

/** Empty Console: the saved commands at hand (arrows + Enter), a new terminal. */
function QuickCommands() {
  onMount(() => void loadCommands())
  const focusAt = (el: HTMLElement, dir: number) => {
    const list = [...(el.closest('.quick-cmds')?.querySelectorAll<HTMLButtonElement>('.quick-cmd') ?? [])]
    list[(list.indexOf(el as HTMLButtonElement) + dir + list.length) % list.length]?.focus()
  }
  return (
    <div class="quick-cmds" data-testid="quick-commands">
      <Show when={commands().length} fallback={<EmptyState icon="terminal" text={t('No open console.')} action={t('New terminal')} onAction={() => newConsole()} />}>
        <p class="muted small">{t('No open console. Run a command of the project:')}</p>
        <For each={commands()}>
          {(c) => (
            <button
              class="quick-cmd"
              title={c.description || c.command}
              onClick={() => void runCommand(c)}
              onKeyDown={(e) => (e.key === 'ArrowDown' || e.key === 'ArrowUp') && (e.preventDefault(), focusAt(e.currentTarget, e.key === 'ArrowDown' ? 1 : -1))}
              data-testid="quick-command"
            >
              <Icon name={flagIcon[c.flag]} size={13} />
              <span class="quick-cmd-name">{c.name}</span>
              <span class="muted small">{c.flag}</span>
            </button>
          )}
        </For>
      </Show>
      <div class="quick-cmds-actions">
        <Show when={commands().length}>
          <button class="btn small" onClick={() => newConsole()}>
            {t('New terminal')}
          </button>
        </Show>
        <button class="btn small" onClick={() => openCommands(commands().length ? {} : { draft: {} })} data-testid="quick-edit-commands">
          {commands().length ? t('Edit commands…') : t('Create a command…')}
        </button>
      </div>
    </div>
  )
}

async function closeConsole(id: string) {
  try {
    await requestIn(projectOf(id), 'console.close', { id })
  } catch {
    /* already gone */
  }
  setConsoles((l) => l.filter((c) => c.id !== id))
  disposeTerm(id)
  if (session.bottom.active === id) mutate((s) => (s.bottom.active = consoles()[0]?.id ?? null))
}

async function renameConsole(c: ConsoleInfo) {
  const title = await prompt({ title: t('Rename the console'), value: c.title })
  if (title) requestIn(c.project, 'console.rename', { id: c.id, title }).catch(errorToast)
}

// ---------- tools ----------

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

/** Consoles in the order of their tabs (session), the new ones last. */
export function ordered() {
  const rank = (id: string) => {
    const i = session.bottom.order.indexOf(id)
    return i < 0 ? Infinity : i
  }
  return [...consoles()].sort((a, b) => rank(a.id) - rank(b.id))
}

// Console tabs shown before (back) and after (forward) the active one, for the mouse buttons:
// every change of the active console is recorded, but the moves of navigateConsoles.
const consoleTrail = { back: [] as string[], forward: [] as string[] }
let replayed: string | null = null
createRoot(() =>
  createEffect(
    on(
      () => session.bottom.active,
      (cur, prev) => {
        if (cur === replayed) return void (replayed = null)
        if (!prev || prev === cur) return
        const b = consoleTrail.back
        if (b[b.length - 1] !== prev) b.push(prev)
        if (b.length > 50) b.shift()
        consoleTrail.forward.length = 0
      },
      { defer: true },
    ),
  ),
)

/** Shows the console tab shown before (-1) or after (1); one closed since is skipped. */
export function navigateConsoles(dir: -1 | 1) {
  const from = consoleTrail[dir < 0 ? 'back' : 'forward']
  const to = consoleTrail[dir < 0 ? 'forward' : 'back']
  const cur = session.bottom.active
  for (let id = from.pop(); id; id = from.pop()) {
    if (id === cur || !consoles().some((c) => c.id === id)) continue
    if (cur) to.push(cur)
    replayed = id
    mutate((s) => (s.bottom.active = id))
    return
  }
}

function moveConsole(id: string, index: number) {
  const ids = ordered().map((c) => c.id)
  mutate((s) => (s.bottom.order = moveItem(ids, ids.indexOf(id), index)))
}

let dragged: string | null = null

export function ConsoleTool() {
  // A console that no longer exists (pod restarted) falls back to the first one.
  const active = () => {
    const a = session.bottom.active
    return consoles().some((c) => c.id === a) ? a : (ordered()[0]?.id ?? null)
  }
  const setActive = (id: string) => mutate((s) => (s.bottom.active = id))

  return (
    <div class="panel">
      <div class="tabbar tool-tabbar console-tabs" role="tablist" onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDropAt(null)}>
        <For each={ordered()}>
          {(c, i) => (
            <div
              class="tab"
              role="tab"
              aria-selected={active() === c.id}
              classList={{ active: active() === c.id, exited: c.exited, ...dropClasses('consoles', i(), consoles().length) }}
              draggable="true"
              onDragStart={() => (dragged = c.id)}
              onDragEnd={() => ((dragged = null), setDropAt(null))}
              onDragOver={(e) => {
                if (!dragged) return
                e.preventDefault()
                setDropAt({ bar: 'consoles', index: dropIndex(e, i()) })
              }}
              onDrop={(e) => {
                if (!dragged) return
                e.preventDefault()
                moveConsole(dragged, dropIndex(e, i()))
                dragged = null
                setDropAt(null)
              }}
              title={c.command?.join(' ') ?? c.title}
              onClick={() => setActive(c.id)}
              onDblClick={() => renameConsole(c)}
              onMouseDown={(e) => e.button === 1 && (e.preventDefault(), closeConsole(c.id))}
            >
              <Icon name={c.kind === 'task' ? 'play' : 'terminal'} size={13} />
              <span class="tab-title">{c.title}</span>
              <WorktreeChip project={c.project} />
              <Show when={c.exited}>
                <span class={c.code === 0 ? 'ok' : 'danger'}>{c.code}</span>
              </Show>
              <button class="tab-close" title={t('Close')} onMouseDown={(e) => e.stopPropagation()} onClick={(e) => (e.stopPropagation(), closeConsole(c.id))}>
                <span class="x">✕</span>
              </button>
            </div>
          )}
        </For>
        <button class="icon-btn small" title={`${t('New terminal')} (${shortcutOf('console.new')})`} onClick={() => newConsole()}>
          <Icon name="plus" />
        </button>
        <button class="icon-btn small" title={t('Commands of the project')} onClick={(e) => void commandsMenu(e.currentTarget)} data-testid="console-commands">
          <Icon name="play" />
        </button>
      </div>
      <div class="tool-body">
        <Show when={consoles().length === 0}>
          <QuickCommands />
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
