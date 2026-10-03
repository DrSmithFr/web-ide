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

export async function send(text: string, parts: Part[], attachments: ChatMessage['attachments']) {
  if (live.busy) return
  if (!config.server || !config.model) throw new Error('Choisir un serveur et un modèle')
  const content: string | Part[] = parts.length ? [...(text ? [{ type: 'text' as const, text }] : []), ...parts] : text
  setChat(
    produce((c) => {
      c.server = config.server
      c.model = config.model
    }),
  )
  pushMessage({ role: 'user', content, display: parts.length ? text : undefined, attachments: attachments?.length ? attachments : undefined })
  saveChat()
  await run()
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
  ctrl?.abort()
  approval()?.resolve(false)
}

function needsCompaction(): boolean {
  const ctx = contextSize()
  if (!prefs.autoCompact || !ctx) return false
  return contextUsed() > (ctx * prefs.compactAt) / 100
}

const contextError = /context|exceed|too long|too many tokens|n_ctx|num_ctx|longueur/i

async function run() {
  const c = new AbortController()
  ctrl = c
  setLive({ busy: true, stream: '', ...resetLive })
  try {
    // Instructions and skills may have changed since the last message.
    await loadPromptContext()
    let compactedForError = false
    for (let step = 0; step < MAX_STEPS; step++) {
      if (needsCompaction()) await compact(false, c.signal).catch((e) => console.warn('compaction', e))
      if (c.signal.aborted) return
      const stream = newId()
      setLive({ ...resetLive, stream, startedAt: Date.now() })
      const model = currentModel()
      let res: any
      try {
        res = await request(
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
      } catch (e) {
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
      setLive({ ...resetLive })
      // A model loaded on demand tells its context size only once loaded.
      if (!contextSize()) loadModels().catch(() => {})
      if (!msg.tool_calls?.length) return
      for (const call of msg.tool_calls) {
        if (c.signal.aborted) {
          pushMessage({ role: 'tool', tool_call_id: call.id, name: call.function.name, content: 'Annulé par l’utilisateur.', status: 'denied', summary: 'annulé' })
          continue
        }
        pushMessage({ role: 'tool', tool_call_id: call.id, name: call.function.name, content: '', summary: writeTools.has(call.function.name) ? 'en attente…' : 'en cours…' })
        const r = await runTool(call, confirm)
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
    saveChat()
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
export async function compact(manual: boolean, signal?: AbortSignal): Promise<void> {
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
          { role: 'system', content: SUMMARY_SYSTEM },
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

/** Manual compaction from the panel. */
export async function compactNow() {
  if (live.busy) return
  setLive('busy', true)
  try {
    await compact(true)
  } finally {
    setLive('busy', false)
  }
}
