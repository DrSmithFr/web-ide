// The agent runs in the pod (docs/architecture.md): this module shows the conversation it
// runs and sends what the user does. The pod announces every change of a conversation
// (agent.update) and streams the answer being written (llm.delta, followed with llm.attach);
// a conversation goes on when the window closes, and any window can follow or stop it.
import { produce, reconcile } from 'solid-js/store'
import { createSignal } from 'solid-js'
import { on, request } from '../pod/rpc'
import { activeTab, attachWorktree, home, relPath, session } from '../state/project'
import { approval, chat, config, emptyChat, live, newId, prefs, refreshChats, setApproval, setChat, setIncomingDraft, setLive, type Chat, type ChatMessage, type Mode, type Part } from './state'
import { runUiTool } from './uiTools'
import { entryToString, type AnswerEntry } from './ask'
import { toast } from '../ui/toast'
import { t } from '../i18n'

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

/** State of a conversation in the pod. */
export type RunState = 'idle' | 'running' | 'queued' | 'waiting_user' | 'compacting'

/** States of the conversations running in the pod (the side bar shows them). */
export const [runStates, setRunStates] = createSignal<Record<string, RunState>>({})

interface Update {
  id: string
  state: RunState
  ahead?: number
  stream?: string
  title: string
  mode?: Mode
  ticket?: Chat['ticket']
  queue: Chat['queue']
  approval?: Chat['approval']
  resetAt?: number
  server: string
  model: string
  parent?: string
  agent?: Chat['agent']
  children?: string[]
  dismissed?: boolean
  from: number
  count: number
  messages?: ChatMessage[]
}

let attached: AbortController | null = null

/** Follows the answer being written (its stream in the pod). */
function attach(stream: string | undefined) {
  if ((stream ?? '') === live.stream) return
  attached?.abort()
  attached = null
  setLive({ ...resetLive, stream: stream ?? '', startedAt: stream ? Date.now() : 0 })
  if (!stream) return
  attached = new AbortController()
  request('llm.attach', { stream, watch: true }, attached.signal).catch(() => {})
}

function applyState(state: RunState, ahead = 0) {
  setLive({ busy: state !== 'idle', state, ahead, compacting: state === 'compacting' })
}

on('agent.update', (u: Update) => {
  // A conversation that starts or ends moves in the list (active ones first).
  if ((runStates()[u.id] ?? 'idle') !== u.state) listSoon()
  setRunStates((s) => {
    const next = { ...s }
    if (u.state === 'idle') delete next[u.id]
    else next[u.id] = u.state
    return next
  })
  // The cards of the sub-agents of the conversation shown follow their state.
  if (u.parent && u.parent === chat.id) listSoon()
  if (u.id !== chat.id) return
  setChat(
    produce((c) => {
      if (u.from >= 0) c.messages.splice(u.from, c.messages.length - u.from, ...(u.messages ?? []))
      if (c.messages.length > u.count) c.messages.length = u.count
      c.title = u.title
      c.mode = u.mode
      c.ticket = u.ticket
      c.queue = u.queue ?? []
      c.resetAt = u.resetAt
      c.server = u.server
      c.model = u.model
      c.approval = u.approval
      c.parent = u.parent
      c.agent = u.agent
      c.children = u.children
      c.dismissed = u.dismissed
    }),
  )
  setApproval(u.approval ?? null)
  applyState(u.state, u.ahead)
  attach(u.state === 'idle' ? undefined : u.stream)
  rememberActive()
  listSoon()
})

// The list of the conversations follows their titles and dates (at most once a second).
let listTimer: ReturnType<typeof setTimeout> | undefined
function listSoon() {
  if (listTimer) return
  listTimer = setTimeout(() => {
    listTimer = undefined
    refreshChats()
  }, 1000)
}

/**
 * A conversation waits for the user (a change or a command to confirm): a toast with a way
 * to open it when this window shows another one, and a notification of the system when the
 * page is hidden (if allowed).
 */
on('agent.attention', (e: { id: string; title: string; kind: string; sub?: boolean }) => {
  const text = e.sub
    ? e.kind === 'command'
      ? t('Sub-agent “{title}” asks to run a command', { title: e.title })
      : t('Sub-agent “{title}” asks to change a file', { title: e.title })
    : e.kind === 'command'
      ? t('“{title}” asks to run a command', { title: e.title })
      : t('“{title}” asks to change a file', { title: e.title })
  if (e.id !== chat.id) toast(text, 'warn', { label: t('Open'), run: () => void openChat(e.id) }, 10000)
  if (document.hidden && 'Notification' in window && Notification.permission === 'granted') new Notification('Web IDE', { body: text, tag: e.id })
})

/** The Orchestrator moved the user into another conversation: the windows showing it follow. */
on('agent.open', (e: { chat: string; from: string }) => {
  if (e.from === chat.id) openChat(e.chat).catch(() => {})
})

on('agent.error', (e: { id: string; error: string }) => {
  if (e.id === chat.id) console.warn('agent', e.error)
})

/** A tool of the agent that acts on the interface: this window runs it. */
on('agent.ui', async (req: { id: string; chat: string; tool: string; args: Record<string, any>; project?: string }) => {
  // The run may work in another worktree than the one shown: its files open from there.
  const base = req.project ? await attachWorktree(req.project).then((w) => w.root, () => undefined) : undefined
  const res = await runUiTool(req.tool, req.args, base).catch((e) => ({ content: `Error: ${(e as Error).message}`, summary: (e as Error).message, status: 'error' as const }))
  if (!req.args.quiet) request('agent.ui.result', { id: req.id, ...res }).catch(() => {})
})

on('pod.reconnected', () => {
  if (chat.messages.length) openChat(chat.id).catch(() => {})
  loadStates()
})

/** The conversations running in the pod for this project. */
export async function loadStates() {
  try {
    setRunStates(await request<Record<string, RunState>>('agent.states'))
  } catch {
    /* no project */
  }
}

// ---------- the conversation shown ----------

const activeKey = () => `webide.llm.active.${home()?.id ?? ''}`

function rememberActive() {
  try {
    if (chat.messages.length) localStorage.setItem(activeKey(), chat.id)
  } catch {
    /* private mode */
  }
}

/** Shows a conversation: its messages and its state, then follows it. */
export async function openChat(id: string) {
  const r = await request<{ chat: Chat; state: RunState; ahead: number }>('agent.open', { id })
  setChat(reconcile({ ...r.chat, queue: r.chat.queue ?? [] }))
  setApproval(r.chat.approval ?? null)
  // A first message prepared for the user goes to the message box, once.
  if (r.chat.draft) {
    setIncomingDraft(r.chat.draft)
    request('agent.draft', { id }).catch(() => {})
  }
  applyState(r.state, r.ahead)
  attach(r.state === 'idle' ? undefined : r.chat.running?.stream)
  rememberActive()
}

let chatProject = ''

/**
 * Shows the conversation of the open project: the one last active in this browser, or a
 * new one. Called when the panel opens; nothing changes when the project is the same.
 */
export async function restoreActive() {
  const pid = home()?.id ?? ''
  if (!pid || pid === chatProject) return
  const changed = chatProject !== ''
  chatProject = pid
  if (changed) setChat(reconcile(emptyChat()))
  loadStates()
  let id: string | null = null
  try {
    id = localStorage.getItem(activeKey())
  } catch {
    /* private mode */
  }
  if (id && !chat.messages.length) await openChat(id).catch(() => {})
}

// ---------- what the user does ----------

/** Mode of the conversation shown: a new one starts in the default mode of the settings. */
export function currentMode(): Mode {
  return chat.mode ?? (chat.messages.length ? 'build' : prefs.defaultMode)
}

const modeCycle: Mode[] = ['orchestrator', 'build', 'plan', 'briefing']

/** Mode after the current one (Shift+Tab). */
export const nextMode = (): Mode => modeCycle[(modeCycle.indexOf(currentMode()) + 1) % modeCycle.length]

export function setMode(mode: Mode) {
  if (currentMode() === mode) return
  setChat('mode', mode)
  if (chat.messages.length) request('agent.set', { id: chat.id, mode }).catch(() => {})
}

/** Options of the user sent with each message: they change how the agent runs. */
export function agentOptions() {
  const tab = activeTab()
  return {
    autoApply: prefs.autoApply,
    think: prefs.think,
    effort: prefs.effort,
    effortTool: prefs.effortTool,
    tools: prefs.tools,
    autoCompact: prefs.autoCompact,
    compactAt: prefs.compactAt,
    compactServer: prefs.compactServer,
    compactModel: prefs.compactModel,
    planServer: prefs.planServer,
    planModel: prefs.planModel,
    activeFile: tab?.kind === 'file' && tab.path ? relPath(tab.path) : undefined,
    dockerProfiles: session.docker?.profiles ?? [],
  }
}

/** The options changed in the settings: a running conversation uses them from its next step. */
export function syncOptions() {
  if (live.busy) request('agent.set', { id: chat.id, options: agentOptions() }).catch(() => {})
}

export function isBusy() {
  return live.busy
}

/**
 * Sends a message of the user (queued by the pod while the conversation runs). display: what
 * the bubble shows when it differs from the text sent (commands); from: the conversation
 * restarts from this message (an edited message).
 */
export async function send(text: string, parts: Part[], attachments: ChatMessage['attachments'], display?: string, from?: number) {
  if (!config.server || !config.model) throw new Error(t('Choose a server and a model'))
  await request('agent.send', {
    id: chat.id,
    text,
    parts,
    attachments: attachments?.length ? attachments : undefined,
    display,
    server: config.server,
    model: config.model,
    mode: currentMode(),
    options: agentOptions(),
    ticket: chat.ticket,
    adoptedBy: chat.adoptedBy,
    title: chat.title,
    from,
  })
}

/** Same as send: the pod queues the message when the conversation runs. */
export const enqueue = send

export function unqueue(id: string) {
  request('agent.unqueue', { id: chat.id, queued: id }).catch(() => {})
}

/** The user answers the questions of a tool message: the agent goes on with them. */
export async function answerQuestions(index: number, answers: (AnswerEntry | null)[][], notes: string[] = [], path?: number[], offPath?: number) {
  // One string per chosen option: the pod follows the branches by label.
  const strings = answers.map((a) => a.flatMap((x) => (x?.kind === 'choices' ? x.value : [entryToString(x)])).filter((s) => s.trim()))
  await request('agent.answer', { id: chat.id, index, answers: strings, notes, path, offPath })
}

/** The user accepts the plan of a tool message: Build mode, then its execution. */
export async function executePlan(index: number) {
  await request('agent.plan', { id: chat.id, index, state: 'accepted' })
  await send(t('The plan is accepted: carry it out now, step by step.'), [], [], t('Execute the plan'))
}

export function dismissPlan(index: number) {
  request('agent.plan', { id: chat.id, index, state: 'dismissed' }).catch(() => {})
}

/** The answer of the user to a change or a command waiting for them. */
export function answerApproval(allow: boolean, always = false) {
  const a = approval()
  if (!a) return
  request('agent.approve', { id: chat.id, approval: a.id, allow, always }).catch(() => {})
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

/** The model chosen differs from the one of the conversation: offered to go on with it. */
export function otherModel(): boolean {
  return !!config.server && !!config.model && !!chat.model && (config.server !== chat.server || config.model !== chat.model)
}

/** The model chosen, sent when the conversation goes on with it. */
const chosen = (switchModel: boolean) => (switchModel ? { server: config.server, model: config.model } : {})

/** Goes on from the last completed step (after an error or a stop), with the model chosen when switchModel. */
export async function resume(switchModel = false) {
  if (live.busy) return
  await request('agent.resume', { id: chat.id, ...chosen(switchModel) })
}

/** Asks again from the last user message (after an error or a stop), with the model chosen when switchModel. */
export async function retry(switchModel = false) {
  if (live.busy) return
  await request('agent.retry', { id: chat.id, ...chosen(switchModel) })
}

/** Abandons a conversation that failed: it is no longer shown as failed (a sub-agent is stopped). */
export async function dismiss(id = chat.id) {
  await request('agent.dismiss', { id })
  listSoon()
}

export function stop() {
  request('agent.stop', { id: chat.id }).catch(() => {})
}

/** Manual compaction from the panel or /compact (with optional instructions). */
export async function compactNow(instructions = '') {
  if (live.busy) return
  await request('agent.compact', { id: chat.id, instructions, options: agentOptions() })
}

export { newId }
