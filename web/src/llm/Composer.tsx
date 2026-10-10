// Message box of the assistant: a Markdown editor that grows with its content (Enter adds a
// line, Ctrl+S or Ctrl+Enter sends), attachments, dictation, model picker, options menu,
// context gauge, send / stop. The draft survives panel switches.
import { createEffect, createMemo, createResource, createSignal, For, on, onCleanup, onMount, Show, type JSX } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { fuzzy } from '../ui/overlay'
import { request } from '../pod/rpc'
import { relPath } from '../state/project'
import { loadPromptContext, promptContext } from './prompt'
import {
  chat,
  config,
  contextSize,
  contextUsed,
  currentModel,
  incomingDraft,
  live,
  loadModels,
  models,
  modelsLoading,
  prefs,
  savePrefs,
  select,
  serverKind,
  resetChat,
  setChat,
  setIncomingDraft,
  setPrefs,
  type Attachment,
  type Effort,
  type Model,
} from './state'
import { compactNow, currentMode, nextMode, send, setMode, stop, unqueue } from './agent'
import { prepare, type Prepared } from './attachments'
import { prepareDoodle } from './doodle/export'
import { cloneDoc, newDoc, type DoodleDoc } from './doodle/model'
import { doodleSession, openDoodle } from './doodle/session'
import { canCapture, captureScreen, type Picture } from './doodle/background'
import { contextMenu } from '../ui/overlay'
import { registerAction, shortcutOf } from '../keys/bindings'
import { Doc } from '../editor/doc'
import { EditorView } from '../editor/view'
import { settings } from '../state/settings'
import { cancelRecording, canRecord, modelById, speech, startRecording, stopRecording, transcribe } from './transcribe'
import { AttachmentChip, formatSize, formatTokens, Popover, Switch } from './parts'

const modeIcons = { build: 'edit', plan: 'outline', briefing: 'kanban', orchestrator: 'locate' } as const
const modeLabels = { build: () => t('Build'), plan: () => t('Plan'), briefing: () => t('Briefing'), orchestrator: () => t('Orchestrator') }
const modeTitles = {
  orchestrator: () => t('Orchestrator mode: tells what to do next and what was done, proposes actions and opens the right conversation, without changing files (Shift+Tab for Build)'),
  build: () => t('Build mode: acts on the project (Shift+Tab for Plan)'),
  plan: () => t('Plan mode: explores and proposes a plan, without changing files (Shift+Tab for Briefing)'),
  briefing: () => t('Briefing mode: questions you to clarify an idea and writes it in kanban tickets, without changing files (Shift+Tab for Orchestrator)'),
}
import { t, tn } from '../i18n'

const [draft, setDraft] = createSignal('')
const [pending, setPending] = createSignal<Prepared[]>([])
const [preparing, setPreparing] = createSignal(0)
/** The editor of the message box last shown (the doodle modal has its own over the panel's). */
let composerView: EditorView | undefined

/** Converts files (picked, pasted or dropped) into attachments of the next message. */
export async function addFiles(files: Iterable<File>) {
  for (const f of files) {
    setPreparing((n) => n + 1)
    try {
      const p = await prepare(f, currentModel()?.caps)
      if (p.warning) toast(`${f.name}: ${p.warning}`, 'warn')
      setPending([...pending(), p])
    } catch (e) {
      toast(`${f.name}: ${(e as Error).message}`, 'error')
    } finally {
      setPreparing((n) => n - 1)
    }
  }
}

/** Opens a new doodle; Attach joins it to the draft. */
export function newDoodle(background?: Picture) {
  if (doodleSession()) return
  const n = pending().filter((p) => p.attachment.kind === 'doodle').length + 1
  const name = t('Doodle {n}', { n })
  openDoodle({ doc: newDoc(background), name, onAttach: (doc) => attachDoodle(doc, name) })
}

/** Opens an editable copy of a doodle sent earlier: Attach joins it as a new one. */
export function reuseDoodle(a: Attachment) {
  if (doodleSession() || !a.doodle) return
  const n = pending().filter((p) => p.attachment.kind === 'doodle').length + 1
  const name = t('Doodle {n}', { n })
  openDoodle({ doc: cloneDoc(a.doodle), name, onAttach: (doc) => attachDoodle(doc, name) })
}

/** A screenshot chosen by the user, opened as the background of a new doodle. */
async function screenshotDoodle() {
  try {
    newDoodle(await captureScreen())
  } catch (e) {
    if ((e as Error).name !== 'NotAllowedError' && (e as Error).name !== 'AbortError') errorToast(e)
  }
}

/** Opens a doodle of the draft again; Attach replaces it. */
function editDoodle(p: Prepared) {
  const { name, doodle } = p.attachment
  openDoodle({ doc: cloneDoc(doodle!), name, onAttach: (doc) => attachDoodle(doc, name, p) })
}

async function attachDoodle(doc: DoodleDoc, name: string, replace?: Prepared) {
  setPreparing((n) => n + 1)
  try {
    const p = await prepareDoodle(doc, name, currentModel()?.caps)
    const list = pending()
    const i = replace ? list.indexOf(replace) : -1
    setPending(i >= 0 ? list.map((x, j) => (j === i ? p : x)) : [...list, p])
  } finally {
    setPreparing((n) => n - 1)
  }
}

export function focusComposer() {
  queueMicrotask(() => composerView?.focus())
}

/** Puts a suggestion in the box (not sent). */
export function suggest(text: string) {
  setDraft(text)
  focusComposer()
}


// ---------- commands and mentions ----------

export const commands: { name: string; args?: string; hint: string }[] = [
  { name: 'compact', args: '[instructions]', hint: 'Summarizes the older messages, with optional instructions' },
  { name: 'clear', hint: 'New conversation' },
  { name: 'model', args: '[name]', hint: 'Changes the model' },
  { name: 'help', hint: 'Lists the commands and the skills' },
]

interface CompletionItem {
  kind: 'command' | 'skill' | 'file' | 'dir'
  label: string
  detail?: string
  insert: string
}

let pathsCache: { at: number; list: string[] } | null = null

/** Files and folders of the project, relative to its root (cached 15 s). */
async function projectPaths(): Promise<string[]> {
  if (pathsCache && Date.now() - pathsCache.at < 15_000) return pathsCache.list
  const files = (await request<string[]>('search.files').catch(() => [])).map((f) => relPath(f))
  const dirs = new Set<string>()
  for (const f of files) {
    const parts = f.split('/')
    for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/') + '/')
  }
  const list = [...dirs, ...files]
  pathsCache = { at: Date.now(), list }
  return list
}

/** Runs a /command; returns false when the text is not a command to run here. */
export async function runCommand(text: string, onSettings: () => void): Promise<boolean> {
  const m = /^\/(\S+)\s*([\s\S]*)$/.exec(text)
  if (!m) return false
  const [, name, args] = m
  if (live.busy && name === 'compact') {
    toast(t('/{command}: available once the answer is finished', { command: name }), 'warn')
    return true
  }
  switch (name) {
    case 'clear':
    case 'new':
      resetChat()
      return true
    case 'compact':
      await compactNow(args.trim())
      return true
    case 'help':
      setHelp(true)
      return true
    case 'model': {
      const q = args.trim().toLowerCase()
      if (!q) {
        toast(t('Models: {list}', { list: models().map((x) => x.id).join(', ') || t('none') }), 'info', undefined, 8000)
        return true
      }
      const found = models().find((x) => x.id.toLowerCase() === q) ?? models().find((x) => x.id.toLowerCase().includes(q))
      if (!found) {
        toast(t('No model “{name}”', { name: args.trim() }), 'warn')
        return true
      }
      await select(config.server, found.id)
      toast(t('Model: {name}', { name: found.id }), 'ok')
      return true
    }
  }
  const skill = promptContext()?.skills.find((x) => x.name === name)
  if (skill) {
    if (!config.server || !config.model) {
      onSettings()
      return true
    }
    const ask = `Use the skill "${skill.name}": load its instructions with load_skill, then apply them.${args.trim() ? `\n\n${args.trim()}` : ''}`
    await send(ask, [], [], text.trim())
    return true
  }
  toast(t('Unknown command: /{command} (see /help)', { command: name }), 'warn')
  return true
}

const [help, setHelp] = createSignal(false)
/** The message box takes the whole assistant tool (the thread hidden) to write a long message. */
export const [fullComposer, setFullComposer] = createSignal(false)

function HelpCard() {
  return (
    <div class="ai-help" data-testid="ai-help">
      <div class="ai-help-head">
        <strong>{t('Commands')}</strong>
        <span class="grow" />
        <button class="icon-btn small" title={t('Close')} onClick={() => setHelp(false)}>
          <Icon name="close" size={12} />
        </button>
      </div>
      <For each={commands}>
        {(c) => (
          <div class="ai-help-row">
            <code>
              /{c.name}
              {c.args ? ` ${c.args}` : ''}
            </code>
            <span>{t(c.hint)}</span>
          </div>
        )}
      </For>
      <Show when={promptContext()?.skills.length}>
        <strong class="ai-help-sub">Skills</strong>
        <For each={promptContext()!.skills}>
          {(sk) => (
            <div class="ai-help-row">
              <code>/{sk.name} [{t('request')}]</code>
              <span>{sk.description}</span>
            </div>
          )}
        </For>
      </Show>
      <div class="ai-help-foot muted">{t('@path designates a file or folder of the project (completion when typing @).')}</div>
      <div class="ai-help-foot muted">{t('Enter adds a line and continues the lists; Ctrl+S or Ctrl+Enter sends.')}</div>
    </div>
  )
}

function capsText(m: Model) {
  const c: string[] = []
  if (m.caps.vision) c.push(t('image'))
  if (m.caps.video) c.push(t('video'))
  if (m.caps.audio) c.push(t('audio'))
  if (m.caps.tools) c.push(t('tools'))
  if (m.caps.thinking) c.push(t('thinking'))
  return c
}

function ModelPicker(props: { onSettings: () => void }) {
  const [filter, setFilter] = createSignal('')
  const shown = () => {
    const q = filter().toLowerCase()
    return models().filter((m) => !q || m.id.toLowerCase().includes(q))
  }
  const label = () => config.model || (config.servers.length ? t('Choose a model') : t('No server'))
  return (
    <Popover
      align="right"
      class="ai-model-pop"
      trigger={(toggle, open) => (
        <button class="ai-pill" classList={{ open }} onClick={toggle} title={t('Model')} data-testid="model-pill">
          <Show when={currentModel()?.state === 'loaded'}>
            <span class="ai-loaded-dot" />
          </Show>
          <span class="ellipsis">{label()}</span>
          <span class="ai-chev open-up">
            <Icon name="chevron" size={10} />
          </span>
        </button>
      )}
    >
      {(close) => (
        <>
          <div class="ai-pop-head">
            <select
              class="small grow"
              value={config.server}
              title={t('Server')}
              onChange={(e) => {
                const v = e.currentTarget.value
                if (v === '+') {
                  close()
                  props.onSettings()
                  return
                }
                select(v, '').catch(errorToast)
              }}
            >
              <Show when={!config.server}>
                <option value="">{t('— server —')}</option>
              </Show>
              <For each={config.servers}>{(s) => <option value={s.id}>{s.name}</option>}</For>
              <option value="+">{t('Manage the servers…')}</option>
            </select>
            <button class="icon-btn small" title={`${t('Reload the list')}${serverKind() ? ` (${serverKind() === 'ollama' ? 'Ollama' : 'llama.cpp'})` : ''}`} onClick={() => loadModels()}>
              <Icon name="refresh" size={13} />
            </button>
          </div>
          <Show when={models().length > 8}>
            <input class="ai-pop-filter" placeholder={t('Filter the models…')} value={filter()} onInput={(e) => setFilter(e.currentTarget.value)} />
          </Show>
          <div class="ai-model-list">
            <Show when={shown().length} fallback={<div class="muted small pad">{modelsLoading() ? t('Loading…') : t('No model')}</div>}>
              <For each={shown()}>
                {(m) => (
                  <button
                    class="ai-model-item"
                    classList={{ active: m.id === config.model }}
                    onClick={() => {
                      select(config.server, m.id).catch(errorToast)
                      close()
                    }}
                  >
                    <span class="ai-model-check">{m.id === config.model ? '✓' : ''}</span>
                    <span class="grow">
                      <span class="ai-model-name">
                        {m.id}
                        <Show when={m.state === 'loaded'}>
                          <span class="ai-loaded-dot" title={t('Loaded')} />
                        </Show>
                      </span>
                      <span class="ai-model-meta">
                        {[m.details, m.context ? t('{n} context', { n: formatTokens(m.context) }) : '', m.size ? formatSize(m.size) : ''].filter(Boolean).join(' · ')}
                      </span>
                      <span class="ai-model-caps">
                        <For each={capsText(m)}>{(c) => <span class="badge">{c}</span>}</For>
                        <Show when={!m.caps.known}>
                          <span class="badge" title={t('Capabilities known once the model is loaded')}>?</span>
                        </Show>
                      </span>
                    </span>
                  </button>
                )}
              </For>
            </Show>
          </div>
        </>
      )}
    </Popover>
  )
}

function ContextMenu() {
  const ctx = () => contextSize()
  const used = () => contextUsed()
  const ratio = () => (ctx() ? Math.min(1, used() / ctx()) : 0)
  const level = () => (ratio() > prefs.compactAt / 100 ? 'danger' : ratio() > (prefs.compactAt / 100) * 0.85 ? 'warn' : '')
  const active = () => chat.messages.filter((m) => !m.compacted).length
  const compacted = () => chat.messages.filter((m) => m.compacted).length
  const r = 7
  const c = 2 * Math.PI * r
  return (
    <Popover
      align="right"
      class="ai-ctx-pop"
      trigger={(toggle, open) => (
        <button
          class={`ai-ring ${level()}`}
          classList={{ on: open }}
          onClick={toggle}
          title={ctx() ? `Contexte : ${used().toLocaleString()} / ${ctx().toLocaleString()} jetons (${Math.round(ratio() * 100)} %)` : 'Contexte : taille inconnue'}
          data-testid="ai-gauge"
        >
          <svg width="18" height="18" viewBox="0 0 18 18">
            <circle cx="9" cy="9" r={r} class="ai-ring-bg" />
            <circle cx="9" cy="9" r={r} class="ai-ring-fg" stroke-dasharray={`${c * ratio()} ${c}`} transform="rotate(-90 9 9)" />
          </svg>
        </button>
      )}
    >
      {(close) => (
        <div class="ai-ctx" data-testid="ai-context-menu">
          <div class="ai-ctx-title">{t('Context')}</div>
          <Show when={ctx()} fallback={<p class="muted small">{t('Unknown context size: it is known once the model is loaded. About {n} tokens used.', { n: formatTokens(used()) })}</p>}>
            <div class="ai-ctx-num">
              <strong>{formatTokens(used())}</strong> / {t('{n} tokens', { n: formatTokens(ctx()) })} <span class="muted">· {Math.round(ratio() * 100)} %</span>
            </div>
            <div class={`ai-ctx-bar ${level()}`}>
              <span style={{ width: `${ratio() * 100}%` }} />
              <i style={{ left: `${prefs.compactAt}%` }} title={t('Compaction threshold: {n} %', { n: prefs.compactAt })} />
            </div>
          </Show>
          <div class="ai-ctx-rows small">
            <span>{t('Messages sent to the model')}</span>
            <span>{active()}</span>
            <Show when={compacted()}>
              <span>{t('Compacted messages')}</span>
              <span>{compacted()}</span>
            </Show>
          </div>
          <div class="ai-pop-sep" />
          <Switch label={t('Automatic compaction')} hint={t('Beyond {n} % of the context', { n: prefs.compactAt })} checked={prefs.autoCompact} onChange={(v) => (setPrefs('autoCompact', v), savePrefs())} />
          <label class="ai-ctx-range small">
            <span>{t('Threshold')}</span>
            <input type="range" min="40" max="95" step="5" value={prefs.compactAt} onInput={(e) => (setPrefs('compactAt', Number(e.currentTarget.value)), savePrefs())} />
            <span>{prefs.compactAt} %</span>
          </label>
          <button
            class="btn small primary ai-ctx-compact"
            disabled={live.busy || chat.messages.length < 2}
            onClick={() => {
              close()
              compactNow().catch(errorToast)
            }}
          >
            <Icon name="history" size={13} /> {t('Compact now')}
          </button>
          <p class="muted small ai-ctx-tip">{t('Or type /compact followed by instructions for the summary.')}</p>
        </div>
      )}
    </Popover>
  )
}

const efforts = [
  { id: 'auto', label: () => t('Dynamic'), hint: () => t('Maximum after your message, medium between the tools, low after simple edits, maximum again after a failure') },
  { id: 'xhigh', label: () => t('Max'), hint: () => t('Maximum effort at every step') },
  { id: 'medium', label: () => t('Medium'), hint: () => t('Medium effort at every step') },
  { id: 'low', label: () => t('Low'), hint: () => t('Low effort at every step') },
] as const

function Options() {
  // A ticket session has its own effort (from the complexity), else the one of the options.
  const effort = () => chat.ticket?.effort ?? prefs.effort
  const setEffort = (e: 'auto' | Effort) => {
    if (chat.ticket?.effort) setChat('ticket', 'effort', e)
    else (setPrefs('effort', e), savePrefs())
  }
  const set = (k: 'tools' | 'autoApply' | 'think' | 'effortTool', v: boolean) => {
    setPrefs(k, v)
    savePrefs()
  }
  return (
    <Popover
      class="ai-options-pop"
      align="right"
      trigger={(toggle, open) => (
        <button class="ai-icon" classList={{ on: open }} onClick={toggle} title={t('Assistant options')} data-testid="ai-options">
          <Icon name="sliders" size={16} />
        </button>
      )}
    >
      {() => (
        <>
          <Switch label={t('Tools')} hint={t('Files, search, language servers, consoles')} checked={prefs.tools} onChange={(v) => set('tools', v)} testid="opt-tools" />
          <Switch label={t('Apply without asking')} hint={t('Changes outside the project and /tmp without confirmation')} checked={prefs.autoApply} onChange={(v) => set('autoApply', v)} testid="opt-auto" />
          <Show when={currentModel()?.caps.thinking}>
            <Switch label={t('Thinking')} hint={t('The model thinks before answering')} checked={prefs.think} onChange={(v) => set('think', v)} testid="opt-think" />
            <Show when={prefs.think}>
              <div class="ai-effort-row">
                <span>
                  <span>{t('Effort')}</span>
                  <span class="ai-switch-hint">
                    {effort() === 'auto' ? t('The most after your message, less between the tools') : t('The same at every step')}
                    {chat.ticket?.effort ? ` · ${t('chosen for this ticket session')}` : ''}
                  </span>
                </span>
                <div class="segmented" role="radiogroup" data-testid="opt-effort">
                  <For each={efforts}>
                    {(e) => (
                      <button type="button" role="radio" aria-checked={effort() === e.id} classList={{ on: effort() === e.id }} title={e.hint()} onClick={() => setEffort(e.id)}>
                        {e.label()}
                      </button>
                    )}
                  </For>
                </div>
              </div>
              <Show when={effort() === 'auto'}>
                <Switch label={t('Effort tool')} hint={t('The model can change its effort itself')} checked={prefs.effortTool} onChange={(v) => set('effortTool', v)} testid="opt-effort-tool" />
              </Show>
            </Show>
          </Show>
        </>
      )}
    </Popover>
  )
}

export function Composer(props: {
  onSettings: () => void
  onSent: () => void
  /** In the doodle modal: no new doodle from here, the doodle being drawn can be sent. */
  inDoodle?: boolean
  sendable?: () => boolean
  beforeSend?: () => Promise<void>
  /** Above the box, inside its frame (the bar of the linked ticket). */
  head?: JSX.Element
}) {
  let fileInput!: HTMLInputElement
  let host!: HTMLDivElement
  const [view, setView] = createSignal<EditorView>()
  // The editor edits its own Doc; the draft signal mirrors it both ways.
  const doc = new Doc('', draft(), { lang: 'markdown' })
  onCleanup(doc.onChange(() => setDraft(doc.text)))
  createEffect(
    on(draft, (text) => {
      if (text === doc.text) return
      doc.replace(0, doc.text.length, text, 'composer')
      const v = view()
      if (v?.hasFocus()) v.setSelection(text.length)
      else v?.restoreSelection({ anchor: text.length, head: text.length })
    }),
  )
  // A message prepared by the Orchestrator for the conversation it opened.
  createEffect(() => {
    const text = incomingDraft()
    if (!text) return
    setDraft(text)
    setIncomingDraft('')
    queueMicrotask(() => view()?.focus())
  })

  const insertText = (t: string) => {
    const v = view()
    if (!t || !v) return
    const sel = v.getSelection()
    const from = Math.min(sel.anchor, sel.head)
    const before = doc.text.slice(0, from)
    v.edit(from, Math.max(sel.anchor, sel.head), (before && !/\s$/.test(before) ? ' ' : '') + t)
  }

  const dictate = async () => {
    try {
      if (speech.phase === 'recording') {
        const blob = await stopRecording()
        if (blob) insertText(await transcribe(blob))
      } else if (speech.phase === 'idle') await startRecording()
    } catch (e) {
      toast(t('Dictation: {message}', { message: (e as Error).message }), 'error')
    }
  }
  onCleanup(cancelRecording)

  const [now, setNow] = createSignal(Date.now())
  const tick = setInterval(() => speech.phase === 'recording' && setNow(Date.now()), 250)
  onCleanup(() => clearInterval(tick))
  const speechLabel = () => {
    switch (speech.phase) {
      case 'recording': {
        const s = Math.max(0, Math.floor((now() - speech.startedAt) / 1000))
        return t('Recording {time} · click the microphone to finish', { time: `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}` })
      }
      case 'loading':
        return speech.total
          ? t('Downloading the model {name}: {pct} % ({done} / {total})', { name: modelById(prefs.whisperModel).repo.split('/')[1], pct: Math.round((speech.loaded / speech.total) * 100), done: formatSize(speech.loaded), total: formatSize(speech.total) })
          : t('Loading the transcription model…')
      case 'transcribing':
        return t('Local transcription…')
    }
    return ''
  }

  // Completion of /commands (at the start) and @paths (anywhere), from the caret.
  const [caret, setCaret] = createSignal(0)
  const [selIndex, setSelIndex] = createSignal(0)
  const [dismissed, setDismissed] = createSignal('')
  onMount(() => {
    if (!promptContext()) loadPromptContext().catch(() => {})
  })
  const query = createMemo(() => {
    const v = draft()
    const before = v.slice(0, caret())
    let m = /^\/(\S*)$/.exec(before)
    if (m) return { mode: 'slash' as const, q: m[1], start: 0, end: caret() }
    m = /(^|\s)@([^\s@]*)$/.exec(before)
    if (m) return { mode: 'mention' as const, q: m[2], start: caret() - m[2].length - 1, end: caret() }
    return null
  })
  const [paths] = createResource(() => (query()?.mode === 'mention' ? true : null), projectPaths)
  const items = createMemo<CompletionItem[]>(() => {
    const qy = query()
    if (!qy) return []
    if (qy.mode === 'slash') {
      const all: CompletionItem[] = [
        ...commands.map((c) => ({ kind: 'command' as const, label: `/${c.name}`, detail: t(c.hint), insert: `/${c.name} ` })),
        ...(promptContext()?.skills ?? []).map((sk) => ({ kind: 'skill' as const, label: `/${sk.name}`, detail: sk.description, insert: `/${sk.name} ` })),
      ]
      return all
        .map((it) => ({ it, score: fuzzy(qy.q, it.label.slice(1)) }))
        .filter((x) => x.score > 0)
        .sort((a, b) => b.score - a.score)
        .map((x) => x.it)
    }
    return (paths() ?? [])
      .map((p) => ({ p, score: fuzzy(qy.q, p) - (p.endsWith('/') ? 0.5 : 0) }))
      .filter((x) => x.score > 0)
      .sort((a, b) => b.score - a.score || a.p.length - b.p.length)
      .slice(0, 40)
      .map(({ p }) => ({ kind: p.endsWith('/') ? ('dir' as const) : ('file' as const), label: p, insert: p.endsWith('/') ? `@${p}` : `@${p} ` }))
  })
  const queryKey = () => (query() ? `${query()!.mode}:${query()!.start}` : '')
  const completionOpen = () => !!query() && items().length > 0 && dismissed() !== queryKey()
  createEffect(on(items, () => setSelIndex(0)))
  const accept = (it: CompletionItem) => {
    const qy = query()
    if (!qy) return
    view()?.edit(qy.start, qy.end, it.insert)
  }

  const canSend = () => (!!draft().trim() || pending().length > 0 || !!props.sendable?.()) && !preparing()

  const submit = async () => {
    if (!canSend()) return
    if (props.beforeSend) await props.beforeSend()
    const text = draft().trim()
    const atts = pending()
    if (!canSend()) return
    if (text.startsWith('/') && !atts.length) {
      setDraft('')
      setHelp(false)
      try {
        if (await runCommand(text, props.onSettings)) {
          props.onSent()
          return
        }
      } catch (e) {
        errorToast(e)
        return
      }
    }
    if (!config.server || !config.model) {
      props.onSettings()
      return
    }
    setHelp(false)
    setDraft('')
    setPending([])
    setFullComposer(false)
    props.onSent()
    try {
      await send(
        text,
        atts.flatMap((a) => a.parts),
        atts.map((a) => a.attachment),
      )
    } catch (e) {
      errorToast(e)
    }
  }

  /** Keys seen before the editor; true when consumed. */
  const onKey = (e: KeyboardEvent): boolean => {
    if (completionOpen()) {
      const n = items().length
      if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !e.shiftKey && !e.ctrlKey && !e.altKey) {
        setSelIndex((i) => (i + (e.key === 'ArrowDown' ? 1 : n - 1)) % n)
        return true
      }
      const it = items()[selIndex()]
      const qy = query()!
      // Enter on a token already complete (e.g. "/clear") goes to the next line.
      const complete = it.insert.trimEnd() === draft().slice(qy.start, qy.end)
      if ((e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !complete) || (e.key === 'Tab' && !e.shiftKey)) {
        accept(it)
        return true
      }
      if (e.key === 'Escape') {
        setDismissed(queryKey())
        return true
      }
    }
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && !e.isComposing) {
      submit()
      return true
    }
    if (e.key === 'Escape' && live.busy) {
      stop()
      return true
    }
    if (e.key === 'Escape' && fullComposer() && !props.inDoodle && !view()?.hasCarets()) {
      setFullComposer(false)
      return true
    }
    if (e.key === 'Tab' && e.shiftKey && !e.ctrlKey && !e.altKey) {
      // Shift+Tab cycles through the modes.
      setMode(nextMode())
      return true
    }
    if (e.key === ' ' && e.ctrlKey && !e.shiftKey && !e.altKey) {
      dictate()
      return true
    }
    return false
  }

  // Pasted files become attachments (capture: before the editor pastes text).
  const onPaste = (e: ClipboardEvent) => {
    const files = [...(e.clipboardData?.files ?? [])]
    if (files.length) {
      e.preventDefault()
      e.stopPropagation()
      addFiles(files)
    }
  }

  const placeholder = () => (!config.model ? t('Choose a model…') : live.busy ? t('Queued message…') : t('Message…'))
  onMount(() => {
    const v = new EditorView(doc, {
      tabSize: settings.editor.tabSize,
      insertSpaces: true,
      highlightLine: false,
      wrap: true,
      free: true,
      placeholder: placeholder(),
      onKey,
      onSelection: (sel) => setCaret(sel.head),
    })
    v.mount(host)
    host.addEventListener('paste', onPaste, true)
    setView(v)
    const prev = composerView
    composerView = v
    onCleanup(() => {
      if (composerView === v) composerView = prev
      v.destroy()
    })
  })
  createEffect(() => view()?.setOptions({ placeholder: placeholder(), tabSize: settings.editor.tabSize }))
  // @path and /command in the text.
  createEffect(() => {
    const text = draft()
    const spans: [number, number][] = []
    const cmd = /^\/\S+/.exec(text)
    if (cmd) spans.push([0, cmd[0].length])
    for (const m of text.matchAll(/(^|\s)(@[^\s@]+)/g)) spans.push([m.index! + m[1].length, m.index! + m[0].length])
    view()?.setLiveRanges('ai-ref', spans)
  })
  // The editor actions of the editor tabs, on this box when it has the focus.
  const own = (f: (v: EditorView) => void) => () => {
    const v = view()
    if (!v?.hasFocus()) return false
    f(v)
  }
  const offs = [
    registerAction('file.save', own(() => void submit())),
    ...(props.inDoodle ? [] : [registerAction('assistant.fullComposer', own(() => setFullComposer(!fullComposer())))]),
    registerAction('edit.undo', own((v) => v.undo())),
    registerAction('edit.redo', own((v) => v.redo())),
    registerAction('edit.duplicateLine', own((v) => v.duplicateLine())),
    registerAction('edit.deleteLine', own((v) => v.deleteLine())),
    registerAction('edit.nextOccurrence', own((v) => v.addNextOccurrence())),
    registerAction('edit.unselectOccurrence', own((v) => v.removeLastOccurrence())),
    registerAction('edit.allOccurrences', own((v) => v.selectAllOccurrences())),
    registerAction('nav.subwordLeft', own((v) => v.moveSubword(-1, false))),
    registerAction('nav.subwordRight', own((v) => v.moveSubword(1, false))),
    registerAction('nav.subwordLeftSelect', own((v) => v.moveSubword(-1, true))),
    registerAction('nav.subwordRightSelect', own((v) => v.moveSubword(1, true))),
  ]
  onCleanup(() => offs.forEach((off) => off()))

  return (
    <div
      class="ai-composer-wrap"
      classList={{ full: fullComposer() && !props.inDoodle, [currentMode()]: true, busy: live.busy && live.state !== 'waiting_user', compacting: live.compacting }}
    >
      <div class="ai-led" data-testid="ai-led" />
      {props.head}
      <div class="ai-led" />
      <div class="ai-composer">
        <Show when={chat.queue?.length}>
          <div class="ai-queue" data-testid="ai-queue">
            <div class="ai-queue-title">
              <Icon name="history" size={12} /> {live.busy ? t('Queued: sent at the next step of the answer') : t('Queued: sent with the next message')}
            </div>
            <For each={chat.queue}>
              {(q) => (
                <div class="ai-queue-item">
                  <span class="ellipsis">{q.display ?? q.text}</span>
                  <Show when={q.attachments?.length}>
                    <span class="muted small">+{tn(q.attachments!.length, '{n} attachment', '{n} attachments')}</span>
                  </Show>
                  <span class="grow" />
                  <button
                    class="ai-act"
                    title={t('Back to the message box')}
                    onClick={() => {
                      unqueue(q.id)
                      setDraft(q.display ?? q.text)
                      focusComposer()
                    }}
                  >
                    <Icon name="edit" size={12} />
                  </button>
                  <button class="ai-act" title={t('Remove from the queue')} onClick={() => unqueue(q.id)}>
                    <Icon name="close" size={12} />
                  </button>
                </div>
              )}
            </For>
          </div>
        </Show>
        <Show when={pending().length || preparing()}>
          <div class="ai-atts">
            <For each={pending()}>{(p, i) => <AttachmentChip a={p.attachment} onOpen={p.attachment.doodle && !props.inDoodle ? () => editDoodle(p) : undefined} onRemove={() => setPending(pending().filter((_, j) => j !== i()))} />}</For>
            <Show when={preparing()}>
              <span class="ai-att muted">
                <span class="spinner" /> {t('preparing…')}
              </span>
            </Show>
          </div>
        </Show>
        <Show when={speech.phase !== 'idle'}>
          <div class="ai-speech small" classList={{ rec: speech.phase === 'recording' }} data-testid="ai-speech">
            {speech.phase === 'recording' ? <span class="rec-dot" /> : <span class="spinner" />}
            <span class="ellipsis">{speechLabel()}</span>
          </div>
        </Show>
        <Show when={help()}>
          <HelpCard />
        </Show>
        <Show when={completionOpen()}>
          <div class="ai-complete" role="listbox" data-testid="ai-complete">
            <For each={items()}>
              {(it, i) => (
                <div
                  class="ai-complete-item"
                  classList={{ active: i() === selIndex() }}
                  role="option"
                  onMouseDown={(e) => {
                    e.preventDefault()
                    accept(it)
                  }}
                  onMouseEnter={() => setSelIndex(i())}
                  ref={(el) => createEffect(() => i() === selIndex() && el.scrollIntoView({ block: 'nearest' }))}
                >
                  <Icon name={it.kind === 'dir' ? 'folder' : it.kind === 'file' ? 'file' : it.kind === 'skill' ? 'puzzle' : 'chevron'} size={13} />
                  <span class="ai-complete-label">{it.label}</span>
                  <Show when={it.detail}>
                    <span class="ai-complete-detail ellipsis">{it.detail}</span>
                  </Show>
                </div>
              )}
            </For>
          </div>
        </Show>
        <div class="ai-editor" ref={host} data-testid="ai-editor" />
        <Show when={!props.inDoodle}>
          <button
            class="ai-act ai-full-btn"
            title={fullComposer() ? t('Leave the full screen (Esc)') : `${t('Full screen')} (${shortcutOf('assistant.fullComposer')})`}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => (setFullComposer(!fullComposer()), view()?.focus())}
            data-testid="ai-full"
          >
            <Icon name={fullComposer() ? 'minimize' : 'maximize'} size={13} />
          </button>
        </Show>
        <div class="ai-composer-bar">
          <button
            class="ai-mode"
            classList={{ plan: currentMode() === 'plan', briefing: currentMode() === 'briefing', orchestrator: currentMode() === 'orchestrator' }}
            title={modeTitles[currentMode()]()}
            onClick={() => setMode(nextMode())}
            data-testid="ai-mode"
          >
            <Icon name={modeIcons[currentMode()]} size={13} />
            <span class="ai-mode-label">{modeLabels[currentMode()]()}</span>
            <Show when={currentMode() === 'plan' && prefs.planServer && prefs.planModel}>
              <span class="ai-mode-model ellipsis">· {prefs.planModel}</span>
            </Show>
          </button>
          <button
            class="ai-icon ai-mic"
            classList={{ rec: speech.phase === 'recording' }}
            title={canRecord() ? (speech.phase === 'recording' ? t('End the dictation (Ctrl+Space)') : t('Dictate: local transcription, the sound stays on this machine (Ctrl+Space)')) : t('Microphone unavailable (https or localhost required)')}
            disabled={!canRecord() || speech.phase === 'loading' || speech.phase === 'transcribing'}
            onClick={dictate}
          >
            <Icon name="mic" size={16} />
          </button>
          <button
            class="ai-icon"
            title={t('Attach a file or a doodle')}
            aria-haspopup="menu"
            data-testid="ai-attach"
            onClick={(e) => {
              const r = e.currentTarget.getBoundingClientRect()
              contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.top - 4 }), [
                { label: t('File…'), hint: t('image, video, audio, PDF, text'), action: () => fileInput.click() },
                ...(props.inDoodle ? [] : [{ label: t('Doodle…'), hint: shortcutOf('assistant.doodle'), action: () => newDoodle() }]),
                ...(props.inDoodle || !canCapture() ? [] : [{ label: t('Screenshot…'), hint: t('to annotate'), action: () => void screenshotDoodle() }]),
              ])
            }}
          >
            <Icon name="paperclip" size={16} />
          </button>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              addFiles([...(e.currentTarget.files ?? [])])
              e.currentTarget.value = ''
            }}
          />
          <Options />
          <span class="grow" />
          <Show when={chat.messages.length}>
            <ContextMenu />
          </Show>
          <ModelPicker onSettings={props.onSettings} />
          <Show when={live.busy && canSend()}>
            <button class="ai-send queue" onClick={submit} aria-label={t('Queue')} title={t('Queue (Ctrl+S or Ctrl+Enter): sent at the next step')} data-testid="enqueue">
              <Icon name="arrowUp" size={16} />
            </button>
          </Show>
          <Show
            when={live.busy}
            fallback={
              <button class="ai-send" disabled={!canSend()} onClick={submit} aria-label={t('Send')} title={t('Send (Ctrl+S or Ctrl+Enter)')} data-testid="send">
                <Icon name="arrowUp" size={16} />
              </button>
            }
          >
            <button class="ai-send stop" onClick={stop} aria-label={t('Stop')} title={t('Stop (Esc)')} data-testid="stop">
              <span class="ai-stop-square" />
            </button>
          </Show>
        </div>
      </div>
    </div>
  )
}
