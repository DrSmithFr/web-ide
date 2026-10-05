// Agent loop: send the conversation, stream the answer, run the tool calls it asks for and
// send their results back, until the model answers without tools. Long conversations are
// compacted: the oldest messages are replaced by a summary written by a model.
import { produce } from 'solid-js/store'
import { on, request, RpcError } from '../pod/rpc'
import { runTool, toolsFor, writeTools, type Confirm } from './tools'
import { buildSystemPrompt, loadPromptContext, loadTicketPrompt, promptContext } from './prompt'
import {
  approval,
  chat,
  openChat,
  config,
  contextSize,
  contextUsed,
  currentModel,
  estimateTokens,
  live,
  loadModels,
  newId,
  prefs,
  pushMessage,
  saveChat,
  setApproval,
  setChat,
  setLive,
  updateLast,
  type ChatMessage,
  type Mode,
  type Part,
  type Question,
} from './state'
import { MAX_QUESTIONS } from './kanbanTools'
import { t, tn } from '../i18n'

let ctrl: AbortController | null = null

interface DeltaEvent {
  stream: string
  /** First event of llm.attach: everything written so far. */
  snapshot?: boolean
  startedAt?: number
  content?: string
  reasoning?: string
  tool?: string
  tokens?: number
  speed?: number
  promptDone?: number
  promptTotal?: number
  promptCache?: number
  promptSpeed?: number
}

on('llm.delta', (d: DeltaEvent) => {
  if (d.stream !== live.stream) return
  const now = Date.now()
  if (d.snapshot) {
    setLive({
      content: d.content ?? '',
      reasoning: d.reasoning ?? '',
      tool: d.tool ?? '',
      tokens: d.tokens ?? 0,
      speed: d.speed ?? 0,
      promptDone: d.promptDone ?? 0,
      promptTotal: d.promptTotal ?? 0,
      promptCache: d.promptCache ?? 0,
      promptSpeed: d.promptSpeed ?? 0,
      startedAt: d.startedAt || now,
      firstAt: d.content || d.reasoning || d.tool ? d.startedAt || now : 0,
      thinkStart: d.reasoning ? d.startedAt || now : 0,
      thinkEnd: d.reasoning && (d.content || d.tool) ? now : 0,
    })
    return
  }
  setLive(
    produce((l) => {
      if (d.reasoning) {
        if (!l.thinkStart) l.thinkStart = now
        l.reasoning += d.reasoning
      }
      if (d.content || d.tool) {
        if (l.thinkStart && !l.thinkEnd) l.thinkEnd = now
        if (d.content) l.content += d.content
        if (d.tool) l.tool = d.tool
      }
      if (!l.firstAt && (d.content || d.reasoning || d.tool)) l.firstAt = now
      if (d.tokens) l.tokens = d.tokens
      if (d.speed) l.speed = d.speed
      if (d.promptTotal) {
        l.promptDone = d.promptDone ?? 0
        l.promptTotal = d.promptTotal
        l.promptCache = d.promptCache ?? 0
      }
      if (d.promptSpeed) l.promptSpeed = d.promptSpeed
    }),
  )
})

const resetLive = { content: '', reasoning: '', tool: '', startedAt: 0, firstAt: 0, thinkStart: 0, thinkEnd: 0, tokens: 0, speed: 0, promptDone: 0, promptTotal: 0, promptCache: 0, promptSpeed: 0 }

/** Timing of the answer just finished, kept on its message. */
function timing() {
  const now = Date.now()
  return {
    thinkMs: live.thinkStart ? (live.thinkEnd || now) - live.thinkStart : undefined,
    elapsedMs: live.startedAt ? now - live.startedAt : undefined,
  }
}

function useTools() {
  return prefs.tools && currentModel()?.caps.tools !== false
}

export function currentMode(): Mode {
  return chat.mode ?? 'build'
}

/** Server and model of the next request: the Plan model in Plan mode when one is chosen. */
function target() {
  if (currentMode() === 'plan' && prefs.planServer && prefs.planModel) return { server: prefs.planServer, model: prefs.planModel }
  return { server: config.server, model: config.model }
}

const modeCycle: Mode[] = ['build', 'plan', 'briefing']

/** Mode after the current one (Shift+Tab). */
export const nextMode = (): Mode => modeCycle[(modeCycle.indexOf(currentMode()) + 1) % modeCycle.length]

export function setMode(mode: Mode) {
  if (currentMode() === mode) return
  setChat('mode', mode)
  if (chat.messages.length) saveChat()
}

const SUMMARY_PREFIX = 'Summary of the earlier conversation (automatic compaction, the summarized messages are not visible anymore):\n\n'

/** Messages as the API expects them (fields of the page and compacted messages removed). */
function apiMessages(): any[] {
  const out: any[] = [{ role: 'system', content: buildSystemPrompt(promptContext(), useTools(), currentMode()) }]
  for (const m of chat.messages) {
    if (m.compacted) continue
    if (m.error && m.role === 'assistant' && !m.content && !m.tool_calls?.length) continue
    const msg: any = { role: m.role }
    if (m.kind === 'summary') msg.content = SUMMARY_PREFIX + (m.content as string)
    else if (m.content !== undefined) msg.content = m.content
    else if (m.role !== 'assistant') msg.content = ''
    if (m.tool_calls?.length) msg.tool_calls = m.tool_calls
    if (m.tool_call_id) msg.tool_call_id = m.tool_call_id
    if (m.name) msg.name = m.name
    out.push(JSON.parse(JSON.stringify(msg)))
  }
  return out
}

const confirm: Confirm = (req) => {
  // "Apply without asking" covers file changes; commands of the Plan mode always ask.
  if (req.kind === 'edit' && prefs.autoApply) return Promise.resolve(true)
  return new Promise((resolve) => {
    setApproval({
      ...req,
      resolve: (ok) => {
        setApproval(null)
        resolve(ok)
      },
    })
  })
}

/** The user accepts the plan of a tool message: Build mode, then its execution. */
export async function executePlan(index: number) {
  if (live.busy) return
  setChat('messages', index, 'planState', 'accepted')
  setMode('build')
  await send(t('The plan is accepted: carry it out now, step by step.'), [], [], t('Execute the plan'))
}

export function dismissPlan(index: number) {
  setChat('messages', index, 'planState', 'dismissed')
  saveChat()
}

export function isBusy() {
  return live.busy
}

/** display: what the bubble shows when it differs from the text sent (commands). */
export async function send(text: string, parts: Part[], attachments: ChatMessage['attachments'], display?: string) {
  if (live.busy) {
    enqueue(text, parts, attachments, display)
    return
  }
  if (!config.server || !config.model) throw new Error(t('Choose a server and a model'))
  const content: string | Part[] = parts.length ? [...(text ? [{ type: 'text' as const, text }] : []), ...parts] : text
  setChat(
    produce((c) => {
      c.server = config.server
      c.model = config.model
    }),
  )
  skipQuestions()
  pushMessage({ role: 'user', content, display: display ?? (parts.length ? text : undefined), attachments: attachments?.length ? attachments : undefined })
  saveChat()
  await run()
}

// ---------- questions asked with ask_user ----------

function normalizeQuestions(raw: unknown): Question[] {
  if (!Array.isArray(raw)) return []
  const out: Question[] = []
  for (const q of raw.slice(0, MAX_QUESTIONS)) {
    if (!q || typeof q !== 'object' || !String((q as any).question ?? '').trim()) continue
    const options = (Array.isArray((q as any).options) ? (q as any).options : [])
      .map((o: any) => (typeof o === 'string' ? { label: o } : { label: String(o?.label ?? ''), description: o?.description ? String(o.description) : undefined }))
      .filter((o: { label: string }) => o.label.trim())
      .slice(0, 6)
    out.push({ question: String((q as any).question), header: (q as any).header ? String((q as any).header).slice(0, 24) : undefined, options, multiple: !!(q as any).multiple })
  }
  return out
}

/** Text given back to the model for the answers of the user. */
function answersText(qs: Question[], answers: string[][]): string {
  return (
    'Answers of the user:\n' +
    qs.map((q, i) => `${i + 1}. ${q.question}\n   → ${(answers[i] ?? []).filter((a) => a.trim()).join(' ; ') || '(no answer)'}`).join('\n')
  )
}

/** The user answers the questions of a tool message: the agent goes on with them. */
export async function answerQuestions(index: number, answers: string[][]) {
  const m = chat.messages[index]
  if (live.busy || !m?.questions || m.askState !== 'pending') return
  setChat(
    produce((c) => {
      const msg = c.messages[index]
      msg.answers = answers
      msg.askState = 'answered'
      msg.content = answersText(m.questions!, answers)
      msg.summary = t('answers received')
    }),
  )
  saveChat()
  await run()
}

/** A message sent instead of answering: the pending questions are left aside. */
function skipQuestions() {
  if (!chat.messages.some((m) => m.askState === 'pending')) return
  setChat(
    produce((c) => {
      for (const m of c.messages)
        if (m.askState === 'pending') {
          m.askState = 'skipped'
          m.content = 'The user did not answer these questions; their message follows.'
          m.summary = t('questions not answered')
        }
    }),
  )
}

/** Keeps a message written during an answer: it is sent at the next step of the agent. */
export function enqueue(text: string, parts: Part[], attachments: ChatMessage['attachments'], display?: string) {
  setChat(produce((c) => (c.queue ??= []).push({ id: newId(), text, parts, attachments: attachments?.length ? attachments : undefined, display })))
  saveChat()
}

export function unqueue(id: string) {
  setChat(produce((c) => (c.queue = (c.queue ?? []).filter((q) => q.id !== id))))
  saveChat()
}

/** Moves the queued messages into the conversation; true when there were some. */
function drainQueue(): boolean {
  const q = chat.queue ?? []
  if (!q.length) return false
  setChat(
    produce((c) => {
      for (const m of q) {
        const content: string | Part[] = m.parts.length ? [...(m.text ? [{ type: 'text' as const, text: m.text }] : []), ...m.parts] : m.text
        c.messages.push({ role: 'user', content, display: m.display ?? (m.parts.length ? m.text : undefined), attachments: m.attachments })
      }
      c.queue = []
    }),
  )
  return true
}

/**
 * After a reload during an answer: the tools that were running are marked interrupted,
 * then the agent attaches to the completion still running in the pod (or goes on).
 */
export async function resumeIfNeeded() {
  if (!chat.running || (live.busy && !live.watching)) return
  // Another window may run it: then this one only follows.
  if (!(await request<boolean>('llm.claim', { id: chat.id }).catch(() => true))) {
    syncWatch()
    return
  }
  stopWatch()
  setChat(produce((c) => closeToolCalls(c.messages, 'Interrupted by a reload of the page.')))
  await run(true)
}

/** Tool calls left without a result (interrupted run) get one: the API expects it. */
function closeToolCalls(messages: ChatMessage[], reason: string) {
  const done = new Set(messages.filter((m) => m.role === 'tool').map((m) => m.tool_call_id))
  for (const m of messages) {
    if (m.role === 'tool' && !m.status) {
      m.status = 'error'
      m.content = reason
      m.summary = t('interrupted')
    }
  }
  const last = [...messages].reverse().find((m) => m.role === 'assistant')
  for (const call of last?.tool_calls ?? []) {
    if (!done.has(call.id)) messages.push({ role: 'tool', tool_call_id: call.id, name: call.function.name, content: reason, status: 'error', summary: t('interrupted') })
  }
}

/** Index of the last message written by the user (not a summary), -1 when none. */
function lastUserIndex(): number {
  let i = chat.messages.length - 1
  while (i >= 0 && (chat.messages[i].role !== 'user' || chat.messages[i].kind === 'summary')) i--
  return i
}

/** Steps (answers with tool calls) done since the last user message: a resume keeps them. */
export function stepsSinceUser(): number {
  return chat.messages.slice(lastUserIndex() + 1).filter((m) => m.role === 'assistant' && m.tool_calls?.length).length
}

/**
 * Goes on from the last completed step (after an error or a stop): only the failed
 * answer is written again, the steps before it and their tool results are kept.
 */
export async function resume() {
  if (live.busy) return
  setChat(
    produce((c) => {
      const last = c.messages[c.messages.length - 1]
      if (last?.role === 'assistant' && last.error && !last.tool_calls?.length) c.messages.pop()
      closeToolCalls(c.messages, 'Interrupted: the run stopped before this tool ended.')
    }),
  )
  await run()
}

/** Asks again from the last user message (after an error or a stop). */
export async function retry() {
  if (live.busy) return
  const last = lastUserIndex()
  if (last < 0) return
  setChat(produce((c) => c.messages.splice(last + 1)))
  await run()
}

export function stop() {
  // Following another window: ask it to stop.
  if (live.watching) {
    request('llm.stop', { id: chat.id }).catch(() => {})
    return
  }
  ctrl?.abort()
  approval()?.resolve(false)
}

// ---------- following an answer run by another window ----------

let watchCtrl: AbortController | null = null
let watchedStream = ''

/** Shows the answer another window is running: its stream, read only. */
function syncWatch() {
  if (ctrl) return // this window runs the conversation
  if (!chat.running) {
    stopWatch()
    return
  }
  setLive({ busy: true, watching: true })
  const stream = chat.running.stream
  if (!stream || stream === watchedStream) {
    if (!stream) setLive({ ...resetLive })
    return
  }
  watchCtrl?.abort()
  watchCtrl = new AbortController()
  watchedStream = stream
  setLive({ ...resetLive, stream, startedAt: Date.now() })
  // The answer joins the conversation when the other window saves it (llm.saved).
  request('llm.attach', { stream, watch: true }, watchCtrl.signal).catch(() => {})
}

/** Stops following (the window shows another conversation). */
export function stopWatch() {
  watchCtrl?.abort()
  watchCtrl = null
  watchedStream = ''
  if (live.watching) setLive({ busy: false, watching: false, stream: '', ...resetLive })
}

/** Another window saved this conversation: show its state. */
on('llm.saved', async (e: { id: string }) => {
  if (e.id !== chat.id || ctrl) return
  await openChat(e.id).catch(() => {})
  syncWatch()
})

/** The window running this conversation is gone (or done): take over if it still runs. */
on('llm.released', async (e: { id: string }) => {
  if (e.id !== chat.id || ctrl) return
  await openChat(e.id).catch(() => {})
  stopWatch()
  await resumeIfNeeded()
})

/** The window following this conversation asks to stop. */
on('llm.stop', (e: { id: string }) => {
  if (e.id === chat.id && ctrl) stop()
})

function needsCompaction(): boolean {
  const ctx = contextSize()
  if (!prefs.autoCompact || !ctx) return false
  return contextUsed() > (ctx * prefs.compactAt) / 100
}

const contextError = /context|exceed|too long|too many tokens|n_ctx|num_ctx|longueur/i

/** resume: continue a run interrupted by a reload (attach to its completion if any). */
async function run(resume = false) {
  const chatId = chat.id
  // Busy at once: the page must not look idle while the claim is asked.
  setLive({ busy: true, stream: '', ...resetLive })
  // One window runs a conversation at a time (another window may already resume it).
  if (!(await request<boolean>('llm.claim', { id: chatId }).catch(() => true))) {
    setLive('busy', false)
    if (!resume) pushMessage({ role: 'assistant', content: '', error: t('This conversation is already running in another window.') })
    return
  }
  const c = new AbortController()
  ctrl = c
  let attach = resume ? chat.running?.stream : undefined
  setChat('running', { stream: attach })
  saveChat()
  try {
    // Instructions and skills may have changed since the last message.
    await loadPromptContext()
    let compactedForError = false
    // No step limit: the agent goes on until it answers without tools or is stopped.
    for (;;) {
      // Messages written meanwhile join the conversation before the next request.
      if (!attach) drainQueue()
      if (!attach && needsCompaction()) await compact(false, c.signal).catch((e) => console.warn('compaction', e))
      if (c.signal.aborted) return
      const stream = attach ?? newId()
      setLive({ ...resetLive, stream, startedAt: Date.now() })
      if (!attach) {
        setChat('running', { stream })
        saveChat()
      }
      // The linked ticket as it is now goes in the system prompt.
      if (!attach) await loadTicketPrompt(chat.ticket)
      const model = currentModel()
      const tgt = target()
      const mode = currentMode()
      let res: any
      try {
        res = attach
          ? await request('llm.attach', { stream }, c.signal)
          : await request(
              'llm.chat',
              {
                server: tgt.server,
                model: tgt.model,
                messages: apiMessages(),
                tools: useTools() ? toolsFor(mode, chat.ticket) : undefined,
                think: model?.caps.thinking ? prefs.think : undefined,
                stream,
              },
              c.signal,
            )
        attach = undefined
      } catch (e) {
        attach = undefined
        const canceled = e instanceof RpcError && e.code === 'canceled'
        // Context exceeded: compact once, then try again.
        if (!canceled && !compactedForError && !live.content && contextError.test((e as Error).message)) {
          compactedForError = true
          const done = await compact(false, c.signal).then(
            () => true,
            () => false,
          )
          if (done) continue
        }
        // Keep what was already written.
        if (live.content || live.reasoning || !canceled) {
          pushMessage({
            role: 'assistant',
            content: live.content,
            reasoning_content: live.reasoning || undefined,
            model: tgt.model,
            mode,
            ...timing(),
            error: canceled ? t('Stopped.') : (e as Error).message,
          })
        }
        return
      }
      const msg = res.message as ChatMessage
      pushMessage({
        role: 'assistant',
        content: typeof msg.content === 'string' ? msg.content : '',
        reasoning_content: msg.reasoning_content || undefined,
        tool_calls: msg.tool_calls?.length ? msg.tool_calls : undefined,
        usage: res.usage,
        model: tgt.model,
        mode,
        ...timing(),
        error: res.finish === 'length' ? t('Answer cut: length limit reached.') : undefined,
      })
      // The answer is in the conversation: a reload from now on does not attach to it.
      setChat('running', { stream: undefined }) // a store merges objects: clear the field itself
      saveChat()
      setLive({ ...resetLive })
      // A model loaded on demand tells its context size only once loaded.
      if (!contextSize()) loadModels().catch(() => {})
      if (!msg.tool_calls?.length) {
        // Messages queued during the answer: the agent goes on with them.
        if (chat.queue?.length && !c.signal.aborted) continue
        return
      }
      let stopAfter = false
      for (const call of msg.tool_calls) {
        if (c.signal.aborted) {
          pushMessage({ role: 'tool', tool_call_id: call.id, name: call.function.name, content: 'Canceled by the user.', status: 'denied', summary: t('canceled') })
          continue
        }
        const name = call.function.name
        let args: any = {}
        try {
          args = JSON.parse(call.function.arguments || '{}')
        } catch {
          /* checked by the tools */
        }
        if (name === 'exit_plan_mode') {
          // The plan goes to the user; the turn ends until they decide.
          const plan = String(args.plan ?? '').trim()
          pushMessage({
            role: 'tool',
            tool_call_id: call.id,
            name,
            content: plan ? 'Plan presented to the user, who will accept it or ask for changes. Wait for their answer.' : 'Error: empty plan.',
            status: plan ? 'ok' : 'error',
            summary: plan ? t('plan proposed') : t('empty plan'),
            plan: plan || undefined,
            planState: plan ? 'pending' : undefined,
          })
          if (plan) stopAfter = true
          continue
        }
        if (name === 'ask_user') {
          // The questions go to the user; the turn ends until they answer (answerQuestions).
          const questions = normalizeQuestions(args.questions)
          pushMessage({
            role: 'tool',
            tool_call_id: call.id,
            name,
            content: questions.length ? 'Questions asked to the user: waiting for their answers.' : `Error: 1 to ${MAX_QUESTIONS} questions are needed, each with at least one choice.`,
            status: questions.length ? 'ok' : 'error',
            summary: questions.length ? tn(questions.length, '{n} question', '{n} questions') : t('invalid questions'),
            questions: questions.length ? questions : undefined,
            askState: questions.length ? 'pending' : undefined,
          })
          if (questions.length) stopAfter = true
          continue
        }
        pushMessage({ role: 'tool', tool_call_id: call.id, name, content: '', summary: writeTools.has(name) ? t('waiting…') : t('running…') })
        if (name === 'compact_conversation') {
          // Asked by the model: everything but the last exchange is summarized.
          const r = await compact(true, c.signal, String(args.instructions ?? '')).then(
            () => ({ content: 'Conversation compacted: the older messages are replaced by a summary.', summary: t('conversation compacted'), status: 'ok' as const }),
            (e) => ({ content: `Error: ${(e as Error).message}`, summary: (e as Error).message, status: 'error' as const }),
          )
          updateLast((m) => Object.assign(m, r))
          continue
        }
        const r = await runTool(call, confirm, c.signal, mode, chat.ticket?.id)
        updateLast((m) => {
          m.content = r.content
          m.summary = r.summary
          m.status = r.status
          m.diff = r.diff
        })
      }
      if (stopAfter) return
      saveChat()
      if (c.signal.aborted) return
    }
  } finally {
    if (ctrl === c) ctrl = null
    setLive({ busy: false, stream: '', compacting: false, ...resetLive })
    setApproval(null)
    if (chat.id === chatId) setChat('running', undefined)
    await saveChat()
    request('llm.release', { id: chatId }).catch(() => {})
  }
}

// ---------- compaction ----------

const SUMMARY_SYSTEM = `You summarize a conversation between a user and a programming assistant acting on a project with tools, so that another assistant can carry on without having read it.
Write a structured summary in Markdown, in the language of the conversation, with:
- the request of the user and their instructions (including the current request if it is not finished);
- the decisions taken and the important information found (files, functions, commands, errors);
- the files read or changed and what changed;
- the current state and the next planned steps.
Be precise (paths, names, values) and concise. Do not answer the conversation: summarize it.`

function transcriptOf(m: ChatMessage, budget: number): string {
  const cut = (s: string, n: number) => (s.length > n ? `${s.slice(0, n / 2)}\n… (${s.length - n} characters cut) …\n${s.slice(-n / 2)}` : s)
  const text = typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => (p.type === 'text' ? p.text : `[${p.type}]`)).join('\n')
  switch (m.role) {
    case 'user':
      return m.kind === 'summary' ? `## Earlier summary\n${text}` : `## User\n${cut(text, budget)}${m.attachments?.length ? `\n(attachments: ${m.attachments.map((a) => a.name).join(', ')})` : ''}`
    case 'assistant': {
      const calls = (m.tool_calls ?? []).map((c) => `→ ${c.function.name}(${cut(c.function.arguments, 400)})`).join('\n')
      return `## Assistant\n${cut(text, budget)}${calls ? `\n${calls}` : ''}`
    }
    case 'tool':
      return `### Result of ${m.name ?? 'tool'}${m.status && m.status !== 'ok' ? ` (${m.status})` : ''}\n${cut(text, Math.min(budget, 1500))}`
  }
}

/**
 * Replaces the oldest messages by a summary, keeping the recent ones (about a quarter of
 * the context). manual: compact even a short conversation (all but the last exchange).
 */
export async function compact(manual: boolean, signal?: AbortSignal, instructions = ''): Promise<void> {
  const msgs = chat.messages
  let start = 0
  while (start < msgs.length && msgs[start].compacted) start++
  const ctx = contextSize() || 16384
  const keepBudget = manual ? 0 : ctx * 0.25
  // The kept tail starts at a user or assistant message (never at a tool result).
  let keepFrom = msgs.length
  let tokens = 0
  for (let i = msgs.length - 1; i > start; i--) {
    tokens += estimateTokens(msgs[i])
    if (tokens > keepBudget && keepFrom < msgs.length) break
    if (msgs[i].role !== 'tool') keepFrom = i
  }
  // Manual, or the model reports a full context although the recent messages look small:
  // keep only the last message group.
  if (manual || keepFrom - start < 2) {
    keepFrom = msgs.length
    for (let i = msgs.length - 1; i > start; i--) {
      if (msgs[i].role === 'user' && msgs[i].kind !== 'summary') {
        keepFrom = i
        break
      }
    }
    if (keepFrom === msgs.length) keepFrom = msgs.length - 1
  }
  if (keepFrom - start < 2) {
    if (manual) throw new Error(t('Nothing to compact'))
    return
  }
  const head = msgs.slice(start, keepFrom)
  // Fit the transcript in about 60 % of the context of the summarizing model.
  const maxChars = Math.max(8000, ctx * 0.6 * 3.5)
  let per = 6000
  let transcript = ''
  for (;;) {
    transcript = head.map((m) => transcriptOf(m, per)).join('\n\n')
    if (transcript.length <= maxChars || per <= 300) break
    per = Math.floor(per / 2)
  }
  if (transcript.length > maxChars) transcript = transcript.slice(-maxChars)
  const server = prefs.compactServer || config.server
  const model = prefs.compactServer ? prefs.compactModel : config.model
  if (!server || !model) throw new Error(t('No model for the compaction'))
  setLive('compacting', true)
  try {
    const res = await request(
      'llm.chat',
      {
        server,
        model,
        messages: [
          { role: 'system', content: SUMMARY_SYSTEM + (instructions ? `\n\nInstructions of the user for this summary: ${instructions}` : '') },
          { role: 'user', content: `Conversation to summarize:\n\n${transcript}` },
        ],
        think: false,
        // Its own stream id: the summary must not show in the live answer.
        stream: 'compact-' + newId(),
      },
      signal,
    )
    let summary = String(res.message?.content ?? '').replace(/<think>[\s\S]*?<\/think>/g, '').trim()
    if (!summary) throw new Error(t('the model returned an empty summary'))
    if (res.finish === 'length') summary += '\n\n(summary cut)'
    setChat(
      produce((c) => {
        for (let i = start; i < keepFrom; i++) c.messages[i].compacted = true
        c.messages.splice(keepFrom, 0, { role: 'user', kind: 'summary', content: summary, summarized: keepFrom - start, model })
        c.resetAt = c.messages.length
      }),
    )
    await saveChat()
  } finally {
    setLive('compacting', false)
  }
}

/** Manual compaction from the panel or /compact (with optional instructions). */
export async function compactNow(instructions = '') {
  if (live.busy) return
  setLive('busy', true)
  try {
    await compact(true, undefined, instructions)
  } finally {
    setLive('busy', false)
  }
}
