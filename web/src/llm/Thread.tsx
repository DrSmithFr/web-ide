// Messages of the conversation: welcome screen, user messages, answers with their reasoning
// and tool steps, the answer being written with its live counters, edit confirmation.
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { activeTab, openFile, project, relPath } from '../state/project'
import { approval, chat, config, live, liveSpeed, savePrefs, setChat, setPrefs, type ChatMessage, type Part, type ToolCall } from './state'
import { retry } from './agent'
import { AttachmentChip, callLabel, DiffBlock, formatDuration, formatTokens, Markdown, safeArgs, toolIcons, toolVerbs } from './parts'
import { absPath } from './tools'
import { focusComposer, runCommand } from './Composer'
import { answerQuestions, dismissPlan, executePlan, send } from './agent'
import { produce } from 'solid-js/store'
import { t, tn } from '../i18n'

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
  const suggestions = () => [
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
        {t('The assistant reads and changes the files, searches the code, asks the language servers and runs commands.')}
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
        {t('Drop or paste images, videos, PDF')} · <kbd>Ctrl</kbd>+<kbd>{t('Space')}</kbd> {t('to dictate')} · <kbd>{t('Shift')}</kbd>+<kbd>{t('Enter')}</kbd> {t('for a new line')}
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
  setChat(produce((c) => c.messages.splice(index)))
  if (text.startsWith('/') && !parts.length && (await runCommand(text, () => {}))) return
  await send(text, parts, attachments)
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

function UserMessage(props: { msg: ChatMessage; index: number }) {
  return (
    <div class="ai-msg user" classList={{ editing: editing() === props.index }}>
      <Show
        when={editing() === props.index}
        fallback={
          <>
            <div class="ai-user-bubble">
              <Show when={props.msg.attachments?.length}>
                <div class="ai-atts">
                  <For each={props.msg.attachments}>{(a) => <AttachmentChip a={a} />}</For>
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

function Reasoning(props: { text: string; live?: boolean; ms?: number }) {
  const now = useNow(() => !!props.live, 500)
  const label = () => {
    if (props.live) return `${t('Thinking')}${live.thinkStart ? ` · ${formatDuration(now() - live.thinkStart)}` : ''}…`
    return props.ms ? `${t('Thinking')} · ${formatDuration(props.ms)}` : t('Thinking')
  }
  // The box has its own scroll: while the model thinks, it follows the end of the text
  // unless the user scrolled up in it.
  let box: HTMLDivElement | undefined
  let stick = true
  createEffect(() => {
    props.text
    if (props.live && stick && box) box.scrollTop = box.scrollHeight
  })
  return (
    <details class="ai-reasoning" classList={{ live: !!props.live }} open={props.live}>
      <summary>
        <Icon name="chevron" size={11} />
        <span class={props.live ? 'ai-shimmer' : ''}>{label()}</span>
      </summary>
      <div
        ref={box}
        class="ai-reasoning-text"
        data-testid="ai-reasoning-text"
        onScroll={() => (stick = box!.scrollHeight - box!.scrollTop - box!.clientHeight < 30)}
      >
        {props.text}
      </div>
    </details>
  )
}

function ToolRow(props: { msg: ChatMessage; call?: ToolCall }) {
  const [open, setOpen] = createSignal(false)
  const label = () => callLabel(props.call, props.msg.name ?? '')
  const pending = () => !props.msg.status
  const path = () => {
    const a = safeArgs(props.call)
    return typeof a.path === 'string' && props.msg.status === 'ok' && props.msg.name !== 'list_dir' ? absPath(a.path) : ''
  }
  return (
    <div class={`ai-tool ${props.msg.status ?? 'running'}`}>
      <button class="ai-tool-head" onClick={() => setOpen(!open())} title={t('{tool}: show the result', { tool: props.msg.name ?? '' })}>
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
        <span class="ai-tool-sum ellipsis">{pending() ? '' : props.msg.summary}</span>
        <span class="ai-chev" classList={{ open: open() }}>
          <Icon name="chevron" size={11} />
        </span>
      </button>
      <Show when={props.msg.diff?.length && (open() || props.msg.status === 'ok')}>
        <DiffBlock lines={props.msg.diff!} />
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
                <pre class="ai-tool-out">{typeof props.msg.content === 'string' ? props.msg.content : ''}</pre>
              </>
            }
          >
            {/* A terminal-like block: the command, then its output. */}
            <pre class="ai-term">
              <span class="ai-term-cmd">$ {safeArgs(props.call).command}</span>
              {'\n' + (typeof props.msg.content === 'string' ? props.msg.content : '')}
            </pre>
          </Show>
        </div>
      </Show>
    </div>
  )
}

function ToolSteps(props: { items: { msg: ChatMessage; call?: ToolCall }[] }) {
  const running = () => props.items.some((i) => !i.msg.status)
  const failed = () => props.items.filter((i) => i.msg.status === 'error').length
  const names = () => [...new Set(props.items.map((i) => (toolVerbs[i.msg.name ?? ''] ? t(toolVerbs[i.msg.name ?? '']) : i.msg.name)))].slice(0, 3).join(', ')
  return (
    <Show when={props.items.length > 2} fallback={<div class="ai-steps-flat">{<For each={props.items}>{(i) => <ToolRow msg={i.msg} call={i.call} />}</For>}</div>}>
      <details class="ai-steps" open={running() || undefined}>
        <summary>
          <Show when={running()} fallback={<Icon name="check" size={12} />}>
            <span class="spinner" />
          </Show>
          <span>
            {tn(props.items.length, '{n} action', '{n} actions')} <span class="muted">· {names()}</span>
          </span>
          <Show when={failed()}>
            <span class="badge danger">{t('{n} failed', { n: failed() })}</span>
          </Show>
          <span class="grow" />
          <span class="ai-chev ai-steps-chev">
            <Icon name="chevron" size={11} />
          </span>
        </summary>
        <For each={props.items}>{(i) => <ToolRow msg={i.msg} call={i.call} />}</For>
      </details>
    </Show>
  )
}

function Stats(props: { msg: ChatMessage }) {
  const u = () => props.msg.usage
  const parts = () => {
    const out: string[] = []
    if (props.msg.model) out.push(props.msg.model)
    if (u()?.perSecond) out.push(t('{n} tokens/s', { n: u()!.perSecond!.toFixed(1) }))
    const ms = props.msg.elapsedMs ?? u()?.durationMs
    if (ms) out.push(formatDuration(ms))
    if (u()) out.push(`${formatTokens(u()!.prompt)} → ${t('{n} tokens', { n: formatTokens(u()!.completion) })}${u()!.cached ? ` (${t('{n} cached', { n: formatTokens(u()!.cached!) })})` : ''}`)
    return out
  }
  return (
    <Show when={parts().length}>
      <Show when={props.msg.mode === 'plan'}>
        <span class="badge ai-plan-badge">Plan</span>
      </Show>
      <span class="ai-usage">{parts().join(' · ')}</span>
    </Show>
  )
}

function AssistantMessage(props: { msg: ChatMessage; index: number; lastOfTurn: boolean; lastTurn: boolean; turnStart: number }) {
  // Tool results follow the message that asked for them.
  const results = () => {
    const out: { msg: ChatMessage; call?: ToolCall }[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) {
      const m = chat.messages[i]
      if (!m.plan && !m.questions) out.push({ msg: m, call: props.msg.tool_calls?.find((c) => c.id === m.tool_call_id) })
    }
    return out
  }
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
  const turnText = () =>
    chat.messages
      .slice(props.turnStart, props.index + 1)
      .filter((m) => m.role === 'assistant' && typeof m.content === 'string' && m.content)
      .map((m) => m.content as string)
      .join('\n\n')
  return (
    <div class="ai-msg assistant" classList={{ 'turn-end': props.lastOfTurn }}>
      <Show when={props.msg.reasoning_content}>
        <Reasoning text={props.msg.reasoning_content!} ms={props.msg.thinkMs} />
      </Show>
      <Show when={typeof props.msg.content === 'string' && props.msg.content}>
        <Markdown text={props.msg.content as string} final />
      </Show>
      <Show when={results().length}>
        <ToolSteps items={results()} />
      </Show>
      <For each={plans()}>{(i) => <PlanCard msg={chat.messages[i]} index={i} />}</For>
      <For each={asks()}>{(i) => <AskCard msg={chat.messages[i]} index={i} />}</For>
      <Show when={props.msg.error}>
        <div class="ai-error">
          <Icon name="conflict" size={13} /> {props.msg.error}
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
    if (live.watching && !live.stream) return t('Step running in another window…')
    if (!live.firstAt) {
      if (live.promptTotal) {
        const pct = Math.round((live.promptDone / live.promptTotal) * 100)
        parts.push(t('Reading the prompt · {pct} % ({done} / {total})', { pct, done: formatTokens(live.promptDone), total: formatTokens(live.promptTotal) }))
      } else parts.push(live.watching ? t('Answer running in another window') : t('Waiting for the model'))
    } else {
      parts.push(live.tool ? t('Preparing the call to {tool}', { tool: live.tool }) : live.content ? t('Writing') : t('Thinking'))
      const speed = liveSpeed(at)
      if (speed) parts.push(t('{n} tokens/s', { n: speed.toFixed(1) }))
      if (live.tokens) parts.push(t('{n} tokens', { n: live.tokens }))
    }
    if (live.startedAt) parts.push(formatDuration(at - live.startedAt))
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
            <input type="checkbox" onChange={(e) => (setPrefs('autoApply', e.currentTarget.checked), savePrefs())} />
            {t('Do not ask again')}
          </label>
        </Show>
        <span class="grow" />
        <button class="btn" onClick={() => a().resolve(false)}>
          {t('Refuse')}
        </button>
        <button class="btn primary" onClick={() => a().resolve(true)}>
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

/**
 * Questions of ask_user: one at a time (choices and a free answer), then a recap before
 * sending. Answered or skipped questions stay folded with their answers.
 */
function AskCard(props: { msg: ChatMessage; index: number }) {
  const qs = () => props.msg.questions ?? []
  const [step, setStep] = createSignal(0)
  const [answers, setAnswers] = createSignal<string[][]>(qs().map(() => []))
  const [free, setFree] = createSignal<string[]>(qs().map(() => ''))
  const pending = () => props.msg.askState === 'pending'
  const recap = () => step() >= qs().length
  const answerOf = (i: number) => [...(answers()[i] ?? []), ...(free()[i]?.trim() ? [free()[i].trim()] : [])]
  const answered = (i: number) => answerOf(i).length > 0
  const toggle = (i: number, label: string) => {
    const q = qs()[i]
    setAnswers((a) => {
      const cur = a[i] ?? []
      const next = q.multiple ? (cur.includes(label) ? cur.filter((x) => x !== label) : [...cur, label]) : cur.includes(label) ? [] : [label]
      return a.map((x, k) => (k === i ? next : x))
    })
    // A single choice moves on by itself.
    if (!q.multiple && qs().length > 1 && !free()[i]?.trim() && (answers()[i] ?? []).length) setTimeout(() => setStep(i + 1), 120)
  }
  const submit = () => answerQuestions(props.index, qs().map((_, i) => answerOf(i))).catch(errorToast)
  return (
    <div class="ai-ask" classList={{ done: !pending() }} data-testid="ai-ask">
      <div class="ai-ask-head">
        <Icon name="info" size={14} />
        <strong>{qs().length > 1 ? t('{n} questions', { n: qs().length }) : t('Question')}</strong>
        <span class="grow" />
        <Show when={pending() && !recap() && qs().length > 1}>
          <span class="muted small">
            {step() + 1} / {qs().length}
          </span>
        </Show>
        <Show when={props.msg.askState === 'answered'}>
          <span class="badge ok">{t('answered')}</span>
        </Show>
        <Show when={props.msg.askState === 'skipped'}>
          <span class="badge">{t('not answered')}</span>
        </Show>
      </div>
      <Show
        when={pending()}
        fallback={
          <ol class="ai-ask-recap">
            <For each={qs()}>
              {(q, i) => (
                <li>
                  <span>{q.question}</span>
                  <strong>{(props.msg.answers?.[i()] ?? []).join(' ; ') || '—'}</strong>
                </li>
              )}
            </For>
          </ol>
        }
      >
        <Show
          when={!recap()}
          fallback={
            <>
              <ol class="ai-ask-recap">
                <For each={qs()}>
                  {(q, i) => (
                    <li>
                      <button class="link" onClick={() => setStep(i())}>
                        {q.question}
                      </button>
                      <strong>{answerOf(i()).join(' ; ') || '—'}</strong>
                    </li>
                  )}
                </For>
              </ol>
              <div class="ai-plan-foot">
                <button class="btn" onClick={() => setStep(qs().length - 1)}>
                  {t('Previous')}
                </button>
                <span class="grow" />
                <button class="btn primary" disabled={live.busy} onClick={submit} data-testid="ai-ask-send">
                  {t('Send the answers')}
                </button>
              </div>
            </>
          }
        >
          <Show when={qs()[step()]} keyed>
            {(q) => {
              const i = step()
              return (
                <div class="ai-ask-q" data-testid="ai-ask-question">
                  <Show when={q.header}>
                    <span class="badge">{q.header}</span>
                  </Show>
                  <p class="ai-ask-text">{q.question}</p>
                  <div class="ai-ask-options">
                    <For each={q.options}>
                      {(o) => (
                        <button class="ai-ask-option" classList={{ on: (answers()[i] ?? []).includes(o.label) }} onClick={() => toggle(i, o.label)}>
                          <span class={q.multiple ? 'ai-ask-box' : 'ai-ask-radio'} />
                          <span>
                            <strong>{o.label}</strong>
                            <Show when={o.description}>
                              <span class="muted small"> — {o.description}</span>
                            </Show>
                          </span>
                        </button>
                      )}
                    </For>
                  </div>
                  <input
                    class="input"
                    placeholder={t('Other answer…')}
                    value={free()[i] ?? ''}
                    onInput={(e) => setFree((f) => f.map((x, k) => (k === i ? e.currentTarget.value : x)))}
                    onKeyDown={(e) => e.key === 'Enter' && answered(i) && setStep(i + 1)}
                    data-testid="ai-ask-free"
                  />
                  <div class="ai-plan-foot">
                    <Show when={i > 0}>
                      <button class="btn" onClick={() => setStep(i - 1)}>
                        {t('Previous')}
                      </button>
                    </Show>
                    <span class="grow" />
                    <Show
                      when={qs().length > 1}
                      fallback={
                        <button class="btn primary" disabled={!answered(i) || live.busy} onClick={submit} data-testid="ai-ask-send">
                          {t('Answer')}
                        </button>
                      }
                    >
                      <button class="btn primary" disabled={!answered(i)} onClick={() => setStep(i + 1)} data-testid="ai-ask-next">
                        {i === qs().length - 1 ? t('Summary') : t('Next')}
                      </button>
                    </Show>
                  </div>
                </div>
              )
            }}
          </Show>
        </Show>
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
  const compactedCount = () => chat.messages.filter((m) => m.compacted).length
  const lastFailed = () => {
    const m = chat.messages[chat.messages.length - 1]
    return !live.busy && m && (m.role === 'user' || (m.role === 'assistant' && !!m.error && !m.content))
  }
  return (
    <div class="ai-thread">
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
            <div class="ai-row" classList={{ 'ai-old': !!m.compacted }}>
              <Show
                when={m.kind === 'summary'}
                fallback={
                  <Show
                    when={m.role === 'user'}
                    fallback={<AssistantMessage msg={m} index={i()} lastOfTurn={turns()[i()]?.lastOfTurn ?? false} lastTurn={i() > lastUser()} turnStart={turns()[i()]?.turnStart ?? 0} />}
                  >
                    <UserMessage msg={m} index={i()} />
                  </Show>
                }
              >
                <SummaryCard msg={m} />
              </Show>
            </div>
          </Show>
        )}
      </For>
      <Show when={live.busy && !approval()}>
        <div class="ai-msg assistant live">
          <Show when={live.reasoning}>
            <Reasoning text={live.reasoning} live={!live.content && !live.tool} ms={live.thinkEnd ? live.thinkEnd - live.thinkStart : undefined} />
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
      <Show when={lastFailed()}>
        <div class="ai-retry">
          <button class="btn small" onClick={() => retry().catch(errorToast)}>
            <Icon name="refresh" size={12} /> {t('Retry')}
          </button>
        </div>
      </Show>
    </div>
  )
}
