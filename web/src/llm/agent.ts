// Agent loop: send the conversation, stream the answer, run the tool calls it asks for and
// send their results back, until the model answers without tools. Long conversations are
// compacted: the oldest messages are replaced by a summary written by a model.
import { produce } from 'solid-js/store'
import { on, request, RpcError } from '../pod/rpc'
import { runTool, toolDefs, writeTools, type Confirm } from './tools'
import { buildSystemPrompt, loadPromptContext, promptContext } from './prompt'
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
  type Part,
} from './state'

const MAX_STEPS = 30

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
      }
    }),
  )
})

const resetLive = { content: '', reasoning: '', tool: '', startedAt: 0, firstAt: 0, thinkStart: 0, thinkEnd: 0, tokens: 0, speed: 0, promptDone: 0, promptTotal: 0 }

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

const SUMMARY_PREFIX = 'Résumé de la conversation précédente (compaction automatique, les messages résumés ne sont plus visibles) :\n\n'

/** Messages as the API expects them (fields of the page and compacted messages removed). */
function apiMessages(): any[] {
  const out: any[] = [{ role: 'system', content: buildSystemPrompt(promptContext(), useTools()) }]
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

const confirm: Confirm = (call, path, diff, created) => {
  if (prefs.autoApply) return Promise.resolve(true)
  return new Promise((resolve) => {
    setApproval({
      call,
      path,
      diff,
      created,
      resolve: (ok) => {
        setApproval(null)
        resolve(ok)
      },
    })
  })
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
  if (!config.server || !config.model) throw new Error('Choisir un serveur et un modèle')
  const content: string | Part[] = parts.length ? [...(text ? [{ type: 'text' as const, text }] : []), ...parts] : text
  setChat(
    produce((c) => {
      c.server = config.server
      c.model = config.model
    }),
  )
  pushMessage({ role: 'user', content, display: display ?? (parts.length ? text : undefined), attachments: attachments?.length ? attachments : undefined })
  saveChat()
  await run()
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
  setChat(
    produce((c) => {
      const done = new Set(c.messages.filter((m) => m.role === 'tool').map((m) => m.tool_call_id))
      for (const m of c.messages) {
        if (m.role === 'tool' && !m.status) {
          m.status = 'error'
          m.content = 'Interrompu par le rechargement de la page.'
          m.summary = 'interrompu'
        }
      }
      const last = [...c.messages].reverse().find((m) => m.role === 'assistant')
      for (const call of last?.tool_calls ?? []) {
        if (!done.has(call.id)) c.messages.push({ role: 'tool', tool_call_id: call.id, name: call.function.name, content: 'Interrompu par le rechargement de la page.', status: 'error', summary: 'interrompu' })
      }
    }),
  )
  await run(true)
}

/** Asks again from the last user message (after an error or a stop). */
export async function retry() {
  if (live.busy) return
  let last = chat.messages.length - 1
  while (last >= 0 && (chat.messages[last].role !== 'user' || chat.messages[last].kind === 'summary')) last--
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
    if (!resume) pushMessage({ role: 'assistant', content: '', error: 'Cette conversation est déjà en cours dans une autre fenêtre.' })
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
    for (let step = 0; step < MAX_STEPS; step++) {
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
      const model = currentModel()
      let res: any
      try {
        res = attach
          ? await request('llm.attach', { stream }, c.signal)
          : await request(
              'llm.chat',
              {
                server: config.server,
                model: config.model,
                messages: apiMessages(),
                tools: useTools() ? toolDefs : undefined,
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
          if (done) {
            step--
            continue
          }
        }
        // Keep what was already written.
        if (live.content || live.reasoning || !canceled) {
          pushMessage({
            role: 'assistant',
            content: live.content,
            reasoning_content: live.reasoning || undefined,
            model: config.model,
            ...timing(),
            error: canceled ? 'Arrêté.' : (e as Error).message,
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
        model: config.model,
        ...timing(),
        error: res.finish === 'length' ? 'Réponse coupée : limite de longueur atteinte.' : undefined,
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
      for (const call of msg.tool_calls) {
        if (c.signal.aborted) {
          pushMessage({ role: 'tool', tool_call_id: call.id, name: call.function.name, content: 'Annulé par l’utilisateur.', status: 'denied', summary: 'annulé' })
          continue
        }
        pushMessage({ role: 'tool', tool_call_id: call.id, name: call.function.name, content: '', summary: writeTools.has(call.function.name) ? 'en attente…' : 'en cours…' })
        const r = await runTool(call, confirm, c.signal)
        updateLast((m) => {
          m.content = r.content
          m.summary = r.summary
          m.status = r.status
          m.diff = r.diff
        })
      }
      saveChat()
      if (c.signal.aborted) return
    }
    pushMessage({ role: 'assistant', content: '', error: `Arrêt après ${MAX_STEPS} étapes d’outils.` })
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

const SUMMARY_SYSTEM = `Tu résumes une conversation entre un utilisateur et un assistant de programmation qui agit sur un projet avec des outils, pour qu'un autre assistant puisse la poursuivre sans l'avoir lue.
Écris un résumé structuré en Markdown, dans la langue de la conversation, avec :
- la demande de l'utilisateur et ses consignes (y compris la demande en cours si elle n'est pas terminée) ;
- les décisions prises et les informations importantes découvertes (fichiers, fonctions, commandes, erreurs) ;
- les fichiers lus ou modifiés et ce qui a changé ;
- l'état actuel et les prochaines étapes prévues.
Sois précis (chemins, noms, valeurs) et concis. Ne réponds pas à la conversation : résume-la.`

function transcriptOf(m: ChatMessage, budget: number): string {
  const cut = (t: string, n: number) => (t.length > n ? `${t.slice(0, n / 2)}\n… (${t.length - n} caractères coupés) …\n${t.slice(-n / 2)}` : t)
  const text = typeof m.content === 'string' ? m.content : (m.content ?? []).map((p) => (p.type === 'text' ? p.text : `[${p.type}]`)).join('\n')
  switch (m.role) {
    case 'user':
      return m.kind === 'summary' ? `## Résumé antérieur\n${text}` : `## Utilisateur\n${cut(text, budget)}${m.attachments?.length ? `\n(pièces jointes : ${m.attachments.map((a) => a.name).join(', ')})` : ''}`
    case 'assistant': {
      const calls = (m.tool_calls ?? []).map((c) => `→ ${c.function.name}(${cut(c.function.arguments, 400)})`).join('\n')
      return `## Assistant\n${cut(text, budget)}${calls ? `\n${calls}` : ''}`
    }
    case 'tool':
      return `### Résultat de ${m.name ?? 'outil'}${m.status && m.status !== 'ok' ? ` (${m.status})` : ''}\n${cut(text, Math.min(budget, 1500))}`
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
    if (manual) throw new Error('Rien à compacter')
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
  if (!server || !model) throw new Error('Aucun modèle pour la compaction')
  setLive('compacting', true)
  try {
    const res = await request(
      'llm.chat',
      {
        server,
        model,
        messages: [
          { role: 'system', content: SUMMARY_SYSTEM + (instructions ? `\n\nConsignes de l'utilisateur pour ce résumé : ${instructions}` : '') },
          { role: 'user', content: `Conversation à résumer :\n\n${transcript}` },
        ],
        think: false,
        // Its own stream id: the summary must not show in the live answer.
        stream: 'compact-' + newId(),
      },
      signal,
    )
    let summary = String(res.message?.content ?? '').replace(/<think>[\s\S]*?<\/think>/g, '').trim()
    if (!summary) throw new Error('le modèle a renvoyé un résumé vide')
    if (res.finish === 'length') summary += '\n\n(résumé coupé)'
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
