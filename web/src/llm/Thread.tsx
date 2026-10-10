// Messages of the conversation: welcome screen, user messages, answers with their reasoning
// and tool steps, the answer being written with its live counters, edit confirmation.
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { activeTab, openFile, project, relPath } from '../state/project'
import { approval, chat, config, live, liveSpeed, savePrefs, setPrefs, type Attachment, type ChatMessage, type Effort, type Part, type ToolCall } from './state'
import { answerApproval, currentMode, dismiss, otherModel, resume, retry, stepsSinceUser } from './agent'
import { AttachmentChip, callLabel, DiffBlock, formatDuration, formatTokens, Markdown, safeArgs, summaryText, toolIcons, toolVerbs } from './parts'
import { absPath } from './uiTools'
import { focusComposer, reuseDoodle, runCommand } from './Composer'
import { openDiagram } from './DiagramViewer'
import { toSVG } from './doodle/render'
import { doodleSession } from './doodle/session'
import { pages, showPage } from './board/pages'
import { captureScreen, canCapture, type Picture } from './doodle/background'
import { drawPage } from './uiTools'
import { request } from '../pod/rpc'
import { answerQuestions, dismissPlan, executePlan, send } from './agent'
import { AskCard } from './AskCard'
import { PreviewCard } from './PreviewCard'
import { ChildCard, ChildHeader, EventCard, TaskCard } from './SubAgents'
import { ActionCard, OpenedCard } from './Orchestrator'
import { t, tn } from '../i18n'
import { parseAnsi } from '../docker/ansi'

const textOf = (m: ChatMessage) =>
  m.display !== undefined
    ? m.display
    : typeof m.content === 'string' ? m.content : (m.content ?? []).filter((p): p is Extract<Part, { type: 'text' }> => p.type === 'text').slice(0, 1).map((p) => p.text).join('')

function copy(text: string) {
  navigator.clipboard?.writeText(text).then(
    () => toast(t('Copied'), 'ok'),
    () => toast(t('Copy failed'), 'error'),
  )
}

/** A clock that ticks while something is running. */
function useNow(active: () => boolean, ms = 250) {
  const [now, setNow] = createSignal(Date.now())
  const t = setInterval(() => active() && setNow(Date.now()), ms)
  onCleanup(() => clearInterval(t))
  return now
}

// ---------- welcome ----------

function Welcome(props: { onSuggest: (t: string) => void; onSettings: () => void }) {
  const active = () => (activeTab()?.kind === 'file' ? relPath(activeTab()!.path!) : '')
  const orchestrator = () => currentMode() === 'orchestrator'
  const suggestions = () =>
    orchestrator()
      ? [
          { icon: 'play', text: t('What do we work on today?') },
          { icon: 'history', text: t('What did we do yesterday?') },
          { icon: 'sparkle', text: t('I have an idea: ') },
        ]
      : [
    { icon: 'outline', text: t('Explain the architecture of this project to me') },
    active() ? { icon: 'file', text: t('Review {file} and suggest improvements', { file: active() }) } : { icon: 'search', text: t('Where is the configuration of the project handled?') },
    { icon: 'conflict', text: t('Find and fix the errors reported by the language servers') },
    active() ? { icon: 'check', text: t('Write tests for {file}', { file: active() }) } : { icon: 'terminal', text: t('Run the tests and explain the failures') },
        ]
  return (
    <div class="ai-empty ai-welcome">
      <div class="ai-welcome-icon">
        <Icon name="sparkle" size={26} />
      </div>
      <h2>{project()?.name ? t('What shall we do on {project}?', { project: project()!.name }) : t('What shall we do on this project?')}</h2>
      <p class="muted">
        {orchestrator()
          ? t('The Orchestrator tells what to work on next and what was done, proposes the actions and opens the right conversation. Shift+Tab changes the mode.')
          : t('The assistant reads and changes the files, searches the code, asks the language servers and runs commands.')}
      </p>
      <Show
        when={config.servers.length}
        fallback={
          <button class="btn primary" onClick={props.onSettings}>
            {t('Add a model server')}
          </button>
        }
      >
        <div class="ai-suggestions">
          <For each={suggestions()}>
            {(s) => (
              <button class="ai-suggestion" onClick={() => props.onSuggest(s.text)}>
                <Icon name={s.icon} size={14} />
                <span>{s.text}</span>
              </button>
            )}
          </For>
        </div>
      </Show>
      <p class="ai-hint">
        {t('Drop or paste images, videos, PDF')} · <kbd>Ctrl</kbd>+<kbd>{t('Space')}</kbd> {t('to dictate')} · <kbd>Ctrl</kbd>+<kbd>S</kbd> {t('to send')}
      </p>
    </div>
  )
}

// ---------- user ----------

/** Index of the user message being edited in place. */
const [editing, setEditing] = createSignal<number | null>(null)

/** Re-sends an edited message: the conversation restarts from it, attachments kept. */
async function resend(index: number, text: string) {
  const m = chat.messages[index]
  // Attachments: every part except the typed text (the first text part).
  const parts = Array.isArray(m.content) ? m.content.filter((p, i) => !(i === 0 && p.type === 'text' && m.display !== undefined && p.text === m.display)) : []
  const attachments = m.attachments
  setEditing(null)
  if (text.startsWith('/') && !parts.length && (await runCommand(text, () => {}))) return
  await send(text, parts, attachments, undefined, index)
}

function UserText(props: { text: string }) {
  const open = (e: MouseEvent, token: string) => {
    if (!(e.ctrlKey || e.metaKey) || !token.startsWith('@') || token.endsWith('/')) return
    e.preventDefault()
    openFile(absPath(token.slice(1))).catch(() => toast(t('Cannot open {path}', { path: token.slice(1) }), 'error'))
  }
  return (
    <div class="ai-user-text">
      {/* @paths and the /command of the start shown as chips; Ctrl+click opens a file */}
      <For each={props.text.split(/(^\/\S+|@[^\s@]+)/)}>
        {(part) =>
          part.startsWith('@') || /^\/\S+$/.test(part) ? (
            <span class="ai-mention" classList={{ file: part.startsWith('@') && !part.endsWith('/') }} title={part.startsWith('@') ? t('Ctrl+click: open in the editor') : undefined} onClick={(e) => open(e, part)}>
              {part}
            </span>
          ) : (
            part
          )
        }
      </For>
    </div>
  )
}

function EditBox(props: { index: number; text: string }) {
  const [value, setValue] = createSignal(props.text)
  let el!: HTMLTextAreaElement
  const grow = () => {
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 360)}px`
  }
  const submit = () => {
    const t = value().trim()
    if (t) resend(props.index, t).catch(errorToast)
  }
  return (
    <div class="ai-edit" data-testid="ai-edit">
      <textarea
        ref={(e) => {
          el = e
          queueMicrotask(() => {
            grow()
            e.focus()
            e.setSelectionRange(e.value.length, e.value.length)
          })
        }}
        value={value()}
        onInput={(e) => {
          setValue(e.currentTarget.value)
          grow()
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
            e.preventDefault()
            submit()
          } else if (e.key === 'Escape') {
            e.preventDefault()
            setEditing(null)
          }
        }}
      />
      <div class="ai-edit-bar">
        <span class="muted small">{t('The answer will be generated again from this message.')}</span>
        <span class="grow" />
        <button class="btn small" onClick={() => setEditing(null)}>
          {t('Cancel')}
        </button>
        <button class="btn small primary" disabled={!value().trim()} onClick={submit}>
          {t('Send')}
        </button>
      </div>
    </div>
  )
}

/**
 * board_draw on a capture of the screen: the browser needs a click of the user to capture it.
 * Share draws the page here on the capture and gives it to the agent; Refuse tells it.
 */
function CaptureCard(props: { msg: ChatMessage; call?: ToolCall }) {
  const [busy, setBusy] = createSignal(false)
  const send = (body: Record<string, unknown>) => request('agent.capture', { id: chat.id, index: chat.messages.indexOf(props.msg), ...body })
  const share = async () => {
    let pic: Picture
    try {
      pic = await captureScreen()
    } catch (e) {
      // Closed without choosing: the card stays.
      if ((e as Error).name !== 'NotAllowedError' && (e as Error).name !== 'AbortError') errorToast(e)
      return
    }
    setBusy(true)
    try {
      const res = await drawPage({ title: String(safeArgs(props.call).title ?? ''), elements: [], number: pages().length + 1 }, pic, 'a capture of the screen of the user')
      await send({ content: res.content, summary: res.summary, page: res.page })
    } catch (e) {
      await send({ error: (e as Error).message }).catch(errorToast)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div class="ai-capture" data-testid="ai-capture">
      <Icon name="screen" size={14} />
      <span class="grow">
        {canCapture()
          ? t('The assistant wants a capture of your screen to draw on it.')
          : window.isSecureContext
            ? t('The assistant wants a capture of your screen, which this browser cannot take.')
            : t('The assistant wants a capture of your screen: the browser allows it only on a secure address (https, or localhost).')}
      </span>
      <button class="btn small" disabled={busy()} onClick={() => send({ refused: true }).catch(errorToast)} data-testid="ai-capture-refuse">
        {t('Refuse')}
      </button>
      <Show when={canCapture()}>
        <button class="btn small primary" disabled={busy()} onClick={() => void share()} data-testid="ai-capture-share">
          {t('Share the screen')}
        </button>
      </Show>
    </div>
  )
}

/** A doodle sent: preview (click to enlarge), the description the model read, and a copy to reuse. */
function DoodleCard(props: { a: Attachment }) {
  const doc = () => props.a.doodle!
  const svg = (max: number) => {
    const { w, h } = doc().frame
    const k = max / Math.max(w, h)
    return toSVG(doc(), Math.round(w * k), Math.round(h * k))
  }
  const preview = createMemo(() => 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg(220)))
  return (
    <div class="ai-doodle" data-testid="ai-doodle">
      <button class="ai-doodle-img" title={t('Enlarge')} onClick={() => openDiagram(svg(1600), props.a.description ?? '', t('Copy the description'))}>
        <img src={preview()} alt={props.a.name} />
      </button>
      <div class="ai-doodle-side">
        <span class="ai-doodle-name">{props.a.name}</span>
        <Show when={!doodleSession()}>
          <button class="btn small" title={t('Opens an editable copy, joined to the next message')} onClick={() => reuseDoodle(props.a)} data-testid="ai-doodle-reuse">
            <Icon name="edit" size={13} /> {t('Reuse the doodle')}
          </button>
        </Show>
        <button
          class="btn small"
          onClick={() => {
            const p = pages().find((x) => x.doc === props.a.doodle) ?? pages().find((x) => x.name === props.a.name)
            if (p) showPage(p.key)
          }}
          data-testid="ai-doodle-board"
        >
          <Icon name="layout" size={13} /> {t('Show on the board')}
        </button>
        <Show when={props.a.description}>
          <details class="ai-doodle-desc">
            <summary>{t('Description sent to the model')}</summary>
            <pre>{props.a.description}</pre>
          </details>
        </Show>
      </div>
    </div>
  )
}

function UserMessage(props: { msg: ChatMessage; index: number }) {
  return (
    <div class="ai-msg user" classList={{ editing: editing() === props.index }}>
      <Show
        when={editing() === props.index}
        fallback={
          <>
            <Show when={props.msg.author === 'claude'}>
              <span class="badge ai-author" title={t('Written by Claude Code through the kanban tools')} data-testid="ai-author-claude">
                {t('Claude Code')}
              </span>
            </Show>
            <div class="ai-user-bubble">
              <Show when={props.msg.attachments?.length}>
                <div class="ai-atts">
                  <For each={props.msg.attachments}>{(a) => (a.kind === 'doodle' && a.doodle ? <DoodleCard a={a} /> : <AttachmentChip a={a} />)}</For>
                </div>
              </Show>
              <Show when={textOf(props.msg)}>
                <UserText text={textOf(props.msg)} />
              </Show>
            </div>
            <div class="ai-actions">
              <button class="ai-act" title={t('Copy')} onClick={() => copy(textOf(props.msg))}>
                <Icon name="copy" size={13} />
              </button>
              <Show when={!live.busy && !props.msg.compacted}>
                <button class="ai-act" title={t('Edit')} onClick={() => setEditing(props.index)}>
                  <Icon name="edit" size={13} />
                </button>
              </Show>
            </div>
          </>
        }
      >
        <EditBox index={props.index} text={textOf(props.msg)} />
      </Show>
    </div>
  )
}

// ---------- answers ----------

/** A block open while it runs and folded when it ends, unless the user toggled it. */
function useFold(running: () => boolean) {
  const [user, setUser] = createSignal<boolean>()
  const open = () => user() ?? running()
  return [open, () => setUser(!open())] as const
}

/** Duration of a block: counted while it runs, then the final one. */
function Elapsed(props: { running: boolean; start?: number; ms?: number }) {
  const now = useNow(() => props.running && !!props.start, 500)
  const ms = () => (props.running ? (props.start ? now() - props.start : undefined) : props.ms)
  return (
    <Show when={ms() !== undefined}>
      <span class="ai-dur">{formatDuration(Math.max(0, ms()!))}</span>
    </Show>
  )
}

/** A box that follows the end of its text while it streams, unless the user scrolled up in it. */
function follow(el: HTMLElement, streaming: () => boolean, text: () => unknown) {
  let stick = true
  el.addEventListener('scroll', () => (stick = el.scrollHeight - el.scrollTop - el.clientHeight < 30))
  createEffect(() => {
    text()
    if (streaming() && stick) el.scrollTop = el.scrollHeight
  })
}

const effortLabels = { xhigh: () => t('Max'), medium: () => t('Medium'), low: () => t('Low') }

function Reasoning(props: { text: string; live?: boolean; ms?: number; effort?: Effort; hidden?: boolean }) {
  const [open, toggle] = useFold(() => !!props.live)
  return (
    <div class="ai-reasoning" classList={{ live: !!props.live, open: open(), earlier: props.hidden }}>
      <button class="ai-tool-head" onClick={toggle}>
        <span class="ai-tool-icon">
          <Show when={!props.live} fallback={<span class="spinner" />}>
            <Icon name="sparkle" size={13} />
          </Show>
        </span>
        <span class={props.live ? 'ai-tool-name ai-shimmer' : 'ai-tool-name'}>{props.live ? `${t('Thinking')}…` : t('Thinking')}</span>
        <Show when={props.effort && effortLabels[props.effort]}>
          {(label) => (
            <span class="ai-effort" title={t('Reasoning effort')}>
              {label()()}
            </span>
          )}
        </Show>
        <span class="grow" />
        <Elapsed running={!!props.live} start={live.thinkStart || undefined} ms={props.ms} />
        <span class="ai-chev" classList={{ open: open() }}>
          <Icon name="chevron" size={11} />
        </span>
      </button>
      {/* Its own scroll: while the model thinks, it follows the end of the text. */}
      <div class="ai-reasoning-text" data-testid="ai-reasoning-text" ref={(el) => follow(el, () => !!props.live && open(), () => props.text)}>
        {props.text}
      </div>
    </div>
  )
}

function ToolRow(props: { msg: ChatMessage; call?: ToolCall; hidden?: boolean }) {
  const label = () => callLabel(props.call, props.msg.name ?? '')
  const pending = () => !props.msg.status
  const [open, toggle] = useFold(pending)
  const path = () => {
    const a = safeArgs(props.call)
    return typeof a.path === 'string' && props.msg.status === 'ok' && props.msg.name !== 'list_dir' ? absPath(a.path) : ''
  }
  return (
    <div class={`ai-tool ${props.msg.status ?? 'running'}`} classList={{ earlier: props.hidden }}>
      <button class="ai-tool-head" onClick={toggle} title={t('{tool}: show the result', { tool: props.msg.name ?? '' })}>
        <span class="ai-tool-icon">
          <Show when={!pending()} fallback={<span class="spinner" />}>
            <Icon name={toolIcons[props.msg.name ?? ''] ?? 'puzzle'} size={13} />
          </Show>
        </span>
        <span class="ai-tool-name">{toolVerbs[label().name] ? t(toolVerbs[label().name]) : label().name}</span>
        <span class="ai-tool-target ellipsis">
          {label().target}
          {label().extra}
        </span>
        <span class="grow" />
        <Show when={props.msg.exit !== undefined} fallback={<span class="ai-tool-sum ellipsis">{pending() ? '' : summaryText(props.msg.summary)}</span>}>
          <span class="ai-exit" classList={{ fail: props.msg.exit !== 0 }} title={t('Exit code')} data-testid="ai-exit">
            {props.msg.exit}
          </span>
        </Show>
        {/* elapsedMs is left out when under a millisecond. */}
        <Elapsed running={pending()} start={props.msg.startedAt} ms={props.msg.startedAt ? props.msg.elapsedMs ?? 0 : undefined} />
        <span class="ai-chev" classList={{ open: open() }}>
          <Icon name="chevron" size={11} />
        </span>
      </button>
      <Show when={props.msg.diff?.length && open()}>
        <DiffBlock lines={props.msg.diff!} />
      </Show>
      <Show when={props.msg.page}>
        {(pg) => (
          <div class="ai-page-card" data-testid="ai-page-card">
            <img src={pg().thumb} alt="" />
            <span class="ellipsis">{pg().name}</span>
            <button class="btn small" onClick={() => { const p = pages().find((x) => x.doc === pg().doc) ?? pages().find((x) => x.from === 'model' && x.name === pg().name); if (p) showPage(p.key) }} data-testid="ai-page-show">
              <Icon name="layout" size={13} /> {t('Show on the board')}
            </button>
          </div>
        )}
      </Show>
      <Show when={open()}>
        <div class="ai-tool-detail">
          <Show when={path()}>
            <button class="link small" onClick={() => openFile(path())}>
              {t('Open')} {relPath(path())}
            </button>
          </Show>
          <Show
            when={props.msg.name === 'bash'}
            fallback={
              <>
                <Show when={props.call}>
                  <div class="ai-tool-args mono">
                    {props.msg.name}({props.call!.function.arguments.length > 300 ? props.call!.function.arguments.slice(0, 300) + '…' : props.call!.function.arguments})
                  </div>
                </Show>
                <pre class="ai-tool-out" ref={(el) => follow(el, pending, () => props.msg.content)}>{typeof props.msg.content === 'string' ? props.msg.content : ''}</pre>
              </>
            }
          >
            <Term command={safeArgs(props.call).command ?? ''} msg={props.msg} running={pending()} />
          </Show>
        </div>
      </Show>
    </div>
  )
}

/** Output of a command as in a console: the prompt line, then the output with its colors. */
function Term(props: { command: string; msg: ChatMessage; running: boolean }) {
  // A finished call starts with its state ("Exit code 0"), shown in the head of the row.
  const text = () => {
    if (props.msg.term) return props.msg.term
    const c = typeof props.msg.content === 'string' ? props.msg.content : ''
    return props.msg.exit !== undefined ? c.slice(c.indexOf('\n') + 1) : c
  }
  return (
    <div class="ai-term-box">
      <div class="ai-term-prompt">
        <span class="ai-term-sign">$</span>
        <span class="ai-term-cmd grow">{props.command}</span>
        <button class="ai-term-copy" title={t('Copy the command')} onClick={() => copy(props.command)}>
          <Icon name="copy" size={12} />
        </button>
      </div>
      <pre class="ai-term" ref={(el) => follow(el, () => props.running, text)}>
        <For each={parseAnsi(text())}>{(s) => (s.style ? <span style={s.style}>{s.text}</span> : s.text)}</For>
      </pre>
    </div>
  )
}

/** Part of a chain of blocks (reasoning, tool calls) joined in one, across the answers of a
 *  turn until a text or a card: all its rows but the last are folded behind a label. */
export interface Seg {
  key: string
  joinUp: boolean
  joinDown: boolean
  /** Leading rows hidden; all of them: gone. */
  hidden: number
  gone: boolean
  /** The label of the folded rows, on the first block shown. */
  more?: { n: number; failed: number; open: boolean; ms: number }
  /** Last block of the chain left open at the end: the reasoning running joins it. */
  openEnd?: boolean
}

const [expanded, setExpanded] = createSignal(new Set<string>())
/** The reasoning running is shown (it joins the chain left open). */
const liveJoins = () => live.busy && !approval() && !!live.reasoning

/** A block of a chain: the reasoning of an answer and its tool calls, one row each. */
function Steps(props: { seg?: Seg; reasoning?: ChatMessage; items: { msg: ChatMessage; call?: ToolCall }[] }) {
  const hidden = () => props.seg?.hidden ?? 0
  const offset = () => (props.reasoning ? 1 : 0)
  const toggle = () => {
    const s = new Set(expanded())
    if (!s.delete(props.seg!.key)) s.add(props.seg!.key)
    setExpanded(s)
  }
  return (
    <>
      <Show when={props.seg?.more}>
        {(more) => (
          <button class="ai-steps-more" onClick={toggle} aria-expanded={more().open} data-testid="ai-steps-more">
            <span class="ai-chev" classList={{ open: more().open }}>
              <Icon name="chevron" size={11} />
            </span>
            <span>{tn(more().n, '{n} earlier action', '{n} earlier actions')}</span>
            <Show when={more().failed}>
              <span class="badge danger">{t('{n} failed', { n: more().failed })}</span>
            </Show>
            <Show when={more().ms}>
              <span class="ai-dur" title={t('Time spent on the whole block')}>· {formatDuration(more().ms)}</span>
            </Show>
          </button>
        )}
      </Show>
      <div class="ai-steps" classList={{ 'join-up': props.seg?.joinUp, 'join-down': props.seg?.joinDown || (props.seg?.openEnd && liveJoins()), gone: props.seg?.gone }}>
        <Show when={props.reasoning}>
          {(m) => <Reasoning text={m().reasoning_content!} ms={m().thinkMs} effort={m().effort} hidden={hidden() > 0} />}
        </Show>
        <For each={props.items}>{(i, k) => <ToolRow msg={i.msg} call={i.call} hidden={k() + offset() < hidden()} />}</For>
      </div>
    </>
  )
}

/** Tool results shown as rows under the answer that asked for them (plans and questions are cards). */
function toolRows(index: number) {
  const msg = chat.messages[index]
  const out: { msg: ChatMessage; call?: ToolCall }[] = []
  for (let i = index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) {
    const m = chat.messages[i]
    if (!m.plan && !m.questions) out.push({ msg: m, call: msg.tool_calls?.find((c) => c.id === m.tool_call_id) })
  }
  return out
}

/** Does an answer show cards after its tool calls (they end a chain of blocks)? */
function hasCards(index: number) {
  for (let i = index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) {
    const m = chat.messages[i]
    if (m.plan || m.questions || m.capture === 'pending' || m.card || m.opened || m.preview) return true
    if ((m.name === 'spawn_agent' || m.name === 'agent_adopt' || m.name === 'agent_resume') && m.child) return true
  }
  return false
}

function Stats(props: { msg: ChatMessage }) {
  const u = () => props.msg.usage
  const parts = () => {
    const out: string[] = []
    if (props.msg.model) out.push(props.msg.model)
    if (u()?.perSecond) out.push(t('{n} tokens/s', { n: u()!.perSecond!.toFixed(1) }))
    const ms = props.msg.elapsedMs ?? u()?.durationMs
    if (ms) out.push(formatDuration(ms))
    if (u()) {
      const cached = u()!.cached && u()!.prompt ? ` (${t('{n} cached, {pct} %', { n: formatTokens(u()!.cached!), pct: Math.round((u()!.cached! / u()!.prompt) * 100) })})` : ''
      out.push(`${formatTokens(u()!.prompt)} → ${t('{n} tokens', { n: formatTokens(u()!.completion) })}${cached}`)
    }
    if (u()?.promptPerSecond) out.push(t('prompt {n} tokens/s', { n: Math.round(u()!.promptPerSecond!) }))
    return out
  }
  return (
    <Show when={parts().length}>
      <Show when={props.msg.mode === 'plan'}>
        <span class="badge ai-plan-badge">{t('Plan')}</span>
      </Show>
      <Show when={props.msg.mode === 'briefing'}>
        <span class="badge ai-briefing-badge">{t('Briefing')}</span>
      </Show>
      <span class="ai-usage">{parts().join(' · ')}</span>
    </Show>
  )
}

function AssistantMessage(props: { msg: ChatMessage; index: number; lastOfTurn: boolean; lastTurn: boolean; turnStart: number; head?: Seg; tail?: Seg }) {
  // Tool results follow the message that asked for them.
  const results = () => toolRows(props.index)
  const text = () => typeof props.msg.content === 'string' && !!props.msg.content
  const plans = () => {
    const out: number[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) if (chat.messages[i].plan) out.push(i)
    return out
  }
  const asks = () => {
    const out: number[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) if (chat.messages[i].questions) out.push(i)
    return out
  }
  // A capture of the screen waiting for the user, out of the folded steps.
  const captures = () => {
    const out: number[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) if (chat.messages[i].capture === 'pending') out.push(i)
    return out
  }
  // Actions offered by the Orchestrator, and the conversations it opened: in sight.
  const orchestration = () => {
    const out: number[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) if (chat.messages[i].card || chat.messages[i].opened) out.push(i)
    return out
  }
  // Sub-agents started by this answer: their card stays in sight.
  const childCards = () => {
    const out: string[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) {
      const m = chat.messages[i]
      if ((m.name === 'spawn_agent' || m.name === 'agent_adopt' || m.name === 'agent_resume') && m.child) out.push(m.child)
    }
    return out
  }
  // Apps offered by share_preview: their card stays in sight.
  const previewCards = () => {
    const out: number[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) if (chat.messages[i].preview) out.push(i)
    return out
  }
  const turnText = () =>
    chat.messages
      .slice(props.turnStart, props.index + 1)
      .filter((m) => m.role === 'assistant' && typeof m.content === 'string' && m.content)
      .map((m) => m.content as string)
      .join('\n\n')
  return (
    <div class="ai-msg assistant" classList={{ 'turn-end': props.lastOfTurn }}>
      {/* Without text, the reasoning and the tool calls form one block. */}
      <Show when={text() && props.msg.reasoning_content}>
        <Steps seg={props.head} reasoning={props.msg} items={[]} />
      </Show>
      <Show when={text()}>
        <Markdown text={props.msg.content as string} final />
      </Show>
      <Show when={results().length || (!text() && props.msg.reasoning_content)}>
        <Steps seg={props.tail} reasoning={text() || !props.msg.reasoning_content ? undefined : props.msg} items={results()} />
      </Show>
      <For each={plans()}>{(i) => <PlanCard msg={chat.messages[i]} index={i} />}</For>
      <For each={captures()}>{(i) => <CaptureCard msg={chat.messages[i]} call={props.msg.tool_calls?.find((c) => c.id === chat.messages[i].tool_call_id)} />}</For>
      <For each={orchestration()}>{(i) => (chat.messages[i].card ? <ActionCard msg={chat.messages[i]} index={i} /> : <OpenedCard msg={chat.messages[i]} />)}</For>
      <For each={childCards()}>{(id) => <ChildCard id={id} />}</For>
      <For each={previewCards()}>{(i) => <PreviewCard spec={chat.messages[i].preview!} />}</For>
      <For each={asks()}>{(i) => <AskCard msg={chat.messages[i]} index={i} onSend={(idx, answers, notes, path, off) => answerQuestions(idx, answers, notes, path, off).catch(errorToast)} />}</For>
      <Show when={props.msg.error}>
        <div class="ai-error">
          <Icon name="conflict" size={13} /> {t(props.msg.error!)}
        </div>
      </Show>
      <Show when={props.lastOfTurn && !(live.busy && props.lastTurn)}>
        <div class="ai-actions">
          <Show when={turnText()}>
            <button class="ai-act" title={t('Copy the answer')} onClick={() => copy(turnText())}>
              <Icon name="copy" size={13} />
            </button>
          </Show>
          <Show when={props.lastTurn && !props.msg.compacted}>
            <button class="ai-act" title={t('Generate the answer again')} onClick={() => retry().catch(errorToast)}>
              <Icon name="refresh" size={13} />
            </button>
          </Show>
          <Stats msg={props.msg} />
        </div>
      </Show>
    </div>
  )
}

function SummaryCard(props: { msg: ChatMessage }) {
  return (
    <details class="ai-summary" data-testid="ai-summary">
      <summary>
        <Icon name="history" size={13} />
        <span>
          {props.msg.model ? t('Conversation compacted: {n} messages summarized by {model}', { n: props.msg.summarized ?? 0, model: props.msg.model }) : t('Conversation compacted: {n} messages summarized', { n: props.msg.summarized ?? 0 })}
        </span>
      </summary>
      <Markdown text={typeof props.msg.content === 'string' ? props.msg.content : ''} final />
    </details>
  )
}

// ---------- live ----------

function LiveStats() {
  const now = useNow(() => live.busy)
  const text = () => {
    const at = now()
    const parts: string[] = []
    if (live.compacting) return t('Compacting the conversation…')
    if (live.state === 'queued') return live.ahead ? tn(live.ahead, 'Waiting for the model server ({n} conversation before)', 'Waiting for the model server ({n} conversations before)') : t('Waiting for the model server')
    if (!live.stream) return t('Running the tools…')
    if (!live.firstAt) {
      if (live.promptTotal) {
        const pct = Math.round((live.promptDone / live.promptTotal) * 100)
        parts.push(t('Reading the prompt · {pct} % ({done} / {total})', { pct, done: formatTokens(live.promptDone), total: formatTokens(live.promptTotal) }))
        // Its speed and the part found in the cache of the server (kept in the final stats).
        if (live.promptSpeed) parts.push(t('prompt {n} tokens/s', { n: Math.round(live.promptSpeed) }))
        parts.push(t('cache {pct} %', { pct: Math.round((live.promptCache / live.promptTotal) * 100) }))
      } else parts.push(t('Waiting for the model'))
    } else {
      parts.push(live.tool ? t('Preparing the call to {tool}', { tool: live.tool }) : live.content ? t('Writing') : t('Thinking'))
      const speed = liveSpeed(at)
      if (speed) parts.push(t('{n} tokens/s', { n: speed.toFixed(1) }))
      if (live.tokens) parts.push(t('{n} tokens', { n: live.tokens }))
    }
    if (live.startedAt) parts.push(formatDuration(Math.max(0, at - live.startedAt)))
    return parts.join(' · ')
  }
  return (
    <div class="ai-live-stats" data-testid="ai-live-stats">
      <span class="ai-dots">
        <i />
        <i />
        <i />
      </span>
      <span>{text()}</span>
    </div>
  )
}

function ApprovalCard() {
  const a = () => approval()!
  let always = false
  return (
    <div class="ai-approval" data-testid="ai-approval">
      <Show
        when={a().kind === 'command'}
        fallback={
          <>
            <div class="ai-approval-head">
              <Icon name="edit" size={14} />
              <strong>{a().created ? t('Create') : t('Edit')}</strong>
              <span class="mono ellipsis">{relPath(a().path ?? '')}</span>
            </div>
            <DiffBlock lines={a().diff ?? []} />
          </>
        }
      >
        <div class="ai-approval-head">
          <Icon name="terminal" size={14} />
          <strong>{t('Run this command?')}</strong>
          <span class="muted small">{t('Plan mode: it may change something.')}</span>
        </div>
        <pre class="ai-term ai-approval-cmd">
          <span class="ai-term-cmd">$ {a().command}</span>
        </pre>
      </Show>
      <div class="ai-approval-foot">
        <Show when={a().kind === 'edit'}>
          <label class="check small">
            <input type="checkbox" onChange={(e) => (always = e.currentTarget.checked)} />
            {t('Do not ask again')}
          </label>
        </Show>
        <span class="grow" />
        <button class="btn" onClick={() => answerApproval(false)}>
          {t('Refuse')}
        </button>
        <button
          class="btn primary"
          onClick={() => {
            if (always) {
              setPrefs('autoApply', true)
              savePrefs()
            }
            answerApproval(true, always)
          }}
        >
          {a().kind === 'command' ? t('Run') : t('Apply')}
        </button>
      </div>
    </div>
  )
}

/** Plan proposed by the model with exit_plan_mode. */
function PlanCard(props: { msg: ChatMessage; index: number }) {
  const state = () => props.msg.planState
  return (
    <div class="ai-plan" classList={{ done: state() !== 'pending' }} data-testid="ai-plan">
      <div class="ai-plan-head">
        <Icon name="outline" size={14} />
        <strong>{t('Proposed plan')}</strong>
        <span class="grow" />
        <Show when={state() === 'accepted'}>
          <span class="badge ok">{t('carried out')}</span>
        </Show>
        <Show when={state() === 'dismissed'}>
          <span class="badge">{t('to review')}</span>
        </Show>
      </div>
      <Markdown text={props.msg.plan ?? ''} final />
      <Show when={state() === 'pending'}>
        <div class="ai-plan-foot">
          <span class="muted small">{t('Switches to Build mode to carry it out.')}</span>
          <span class="grow" />
          <button class="btn" disabled={live.busy} onClick={() => (dismissPlan(props.index), focusComposer())}>
            {t('Keep planning')}
          </button>
          <button class="btn primary" disabled={live.busy} onClick={() => executePlan(props.index).catch(errorToast)}>
            {t('Execute this plan')}
          </button>
        </div>
      </Show>
    </div>
  )
}


// ---------- thread ----------

export function Thread(props: { onSuggest: (t: string) => void; onSettings: () => void }) {
  const [showCompacted, setShowCompacted] = createSignal(false)
  // For each assistant message: is it the last of its turn, where does the turn start.
  const turns = createMemo(() => {
    const msgs = chat.messages
    const info: { lastOfTurn: boolean; turnStart: number }[] = []
    let start = 0
    for (let i = 0; i < msgs.length; i++) {
      if (msgs[i].role === 'user') start = i + 1
      let j = i + 1
      while (j < msgs.length && msgs[j].role === 'tool') j++
      info.push({ lastOfTurn: msgs[i].role === 'assistant' && (j >= msgs.length || msgs[j].role === 'user'), turnStart: start })
    }
    return info
  })
  const lastUser = createMemo(() => {
    for (let i = chat.messages.length - 1; i >= 0; i--) if (chat.messages[i].role === 'user') return i
    return -1
  })
  // The chains of blocks: reasoning and tool calls joined across the answers until a text,
  // a card or the end of the turn; all their rows but the last folded behind a label.
  const chains = createMemo(() => {
    const msgs = chat.messages
    const out: { head?: Seg; tail?: Seg; hide?: boolean }[] = []
    let chain: { seg: Seg; errors: boolean[]; ms: number }[] = []
    const add = (i: number, part: string, errors: boolean[], ms: number) => {
      const seg: Seg = { key: chain.length ? chain[0].seg.key : `${chat.id}:${i}:${part}`, joinUp: false, joinDown: false, hidden: 0, gone: false }
      chain.push({ seg, errors, ms })
      return seg
    }
    const close = () => {
      const rows = chain.flatMap((u) => u.errors)
      const open = expanded().has(chain[0].seg.key)
      const total = rows.length > 1 && !open ? rows.length - 1 : 0
      const failed = rows.slice(0, -1).filter(Boolean).length
      let left = total
      let first = true
      chain.forEach((u, k) => {
        u.seg.hidden = Math.min(left, u.errors.length)
        u.seg.gone = u.seg.hidden === u.errors.length
        left -= u.seg.hidden
        u.seg.joinDown = k < chain.length - 1
        if (u.seg.gone) return
        u.seg.joinUp = !first
        if (first && rows.length > 1) u.seg.more = { n: rows.length - 1, failed, open, ms: chain.reduce((n, c) => n + c.ms, 0) }
        first = false
      })
      chain = []
    }
    for (let i = 0; i < msgs.length; i++) {
      const m = msgs[i]
      const o: (typeof out)[number] = {}
      out.push(o)
      if (m.role === 'tool') continue
      if (m.role !== 'assistant' || m.kind || (m.compacted && !showCompacted())) {
        if (chain.length) close()
        continue
      }
      const text = typeof m.content === 'string' && !!m.content
      const tools = toolRows(i)
      const errors = tools.map((r) => r.msg.status === 'error')
      // Time of the block: the generation of the answers and the run of their tool calls.
      const gen = m.elapsedMs ?? m.usage?.durationMs ?? 0
      const run = tools.reduce((n, r) => n + (r.msg.elapsedMs ?? 0), 0)
      if (text) {
        if (m.reasoning_content) o.head = add(i, 'head', [false], gen)
        if (chain.length) close()
        if (errors.length) o.tail = add(i, 'tail', errors, run)
      } else if (m.reasoning_content || errors.length) o.tail = add(i, 'tail', m.reasoning_content ? [false, ...errors] : errors, gen + run)
      const actions = turns()[i]?.lastOfTurn && !(live.busy && i > lastUser())
      const ends = hasCards(i) || !!m.error || actions
      if (ends && chain.length) close()
      if (!text && !ends && o.tail) o.hide = true
    }
    // The reasoning running joins the chain left open.
    const open = chain.length > 0
    if (open) {
      chain[chain.length - 1].seg.openEnd = true
      close()
    }
    for (const o of out) if (o.hide) o.hide = o.tail!.gone
    return { rows: out, open }
  })
  const compactedCount = () => chat.messages.filter((m) => m.compacted).length
  // The turn ended without an answer: an error, a stop, or tools interrupted (not a plan
  // or questions waiting for the user).
  const lastFailed = () => {
    const m = chat.messages[chat.messages.length - 1]
    if (live.busy || !m) return false
    // A sub-agent waiting for its parent, or ended, did not fail.
    if (chat.agent && chat.agent.status !== 'running') return false
    if (m.role === 'tool') return !m.plan && !m.questions && m.capture !== 'pending' && m.wait !== 'parent'
    return (m.role === 'user' && m.kind !== 'agent_event') || (m.role === 'assistant' && !!m.error && !m.tool_calls?.length)
  }
  return (
    <div class="ai-thread">
      <Show when={chat.parent}>
        <ChildHeader />
      </Show>
      <Show when={!chat.messages.length && !live.busy}>
        <Welcome onSuggest={props.onSuggest} onSettings={props.onSettings} />
      </Show>
      <Show when={compactedCount()}>
        <button class="ai-compacted-toggle" onClick={() => setShowCompacted(!showCompacted())}>
          <Icon name="history" size={12} /> {showCompacted() ? t('Hide the {n} compacted messages', { n: compactedCount() }) : t('Show the {n} compacted messages', { n: compactedCount() })}
        </button>
      </Show>
      <For each={chat.messages}>
        {(m, i) => (
          <Show when={m.role !== 'tool' && (!m.compacted || showCompacted())}>
            <div class="ai-row" classList={{ 'ai-old': !!m.compacted, 'ai-gone': !!chains().rows[i()]?.hide }}>
              <Show
                when={m.kind === 'summary' || m.kind?.startsWith('agent_')}
                fallback={
                  <Show
                    when={m.role === 'user'}
                    fallback={<AssistantMessage msg={m} index={i()} lastOfTurn={turns()[i()]?.lastOfTurn ?? false} lastTurn={i() > lastUser()} turnStart={turns()[i()]?.turnStart ?? 0} head={chains().rows[i()]?.head} tail={chains().rows[i()]?.tail} />}
                  >
                    <UserMessage msg={m} index={i()} />
                  </Show>
                }
              >
                <Show when={m.kind === 'summary'}>
                  <SummaryCard msg={m} />
                </Show>
                <Show when={m.kind === 'agent_event' && m.event}>
                  <EventCard msg={m} />
                </Show>
                <Show when={m.kind === 'agent_task'}>
                  <TaskCard msg={m} />
                </Show>
                <Show when={m.kind === 'agent_nudge'}>
                  <div class="ai-nudge muted small">{t('Reminded to end with a report')}</div>
                </Show>
              </Show>
            </div>
          </Show>
        )}
      </For>
      <Show when={live.busy && !approval()}>
        <div class="ai-msg assistant live">
          <Show when={live.reasoning}>
            <div class="ai-steps" classList={{ 'join-up': chains().open }}>
              <Reasoning text={live.reasoning} live={!live.content && !live.tool} ms={live.thinkEnd ? live.thinkEnd - live.thinkStart : undefined} />
            </div>
          </Show>
          <Show when={live.content}>
            <Markdown text={live.content} final={false} />
          </Show>
          <LiveStats />
        </div>
      </Show>
      <Show when={approval()}>
        <ApprovalCard />
      </Show>
      <Show when={lastFailed() && !chat.dismissed}>
        <div class="ai-retry">
          <Show when={stepsSinceUser()}>
            <button class="btn small primary" data-testid="ai-resume" title={t('Go on from the last completed step: the steps already done are kept')} onClick={() => resume().catch(errorToast)}>
              <Icon name="play" size={12} /> {t('Resume')}
            </button>
          </Show>
          <button class="btn small" title={t('Start the answer again from your message')} onClick={() => retry().catch(errorToast)}>
            <Icon name="refresh" size={12} /> {t('Retry')}
          </button>
          <button class="btn small" data-testid="ai-dismiss" title={t('Leave it: the conversation goes to the history (a sub-agent is stopped)')} onClick={() => dismiss().catch(errorToast)}>
            <Icon name="close" size={12} /> {t('Abandon')}
          </button>
          <Show when={otherModel()}>
            <div class="ai-retry-other">
              <button
                class="btn small"
                data-testid="ai-resume-other"
                title={stepsSinceUser() ? t('Go on from the last completed step with the model chosen') : t('Start the answer again from your message with the model chosen')}
                onClick={() => (stepsSinceUser() ? resume(true) : retry(true)).catch(errorToast)}
              >
                <Icon name="play" size={12} /> {t('Resume with {model}', { model: config.model })}
              </button>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  )
}
