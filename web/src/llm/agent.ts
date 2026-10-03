// Agent loop: send the conversation, stream the answer, run the tool calls it asks for and
// send their results back, until the model answers without tools.
import { produce } from 'solid-js/store'
import { on, request, RpcError } from '../pod/rpc'
import { activeTab, project, relPath, root } from '../state/project'
import { runTool, toolDefs, writeTools, type Confirm } from './tools'
import {
  approval,
  chat,
  config,
  currentModel,
  live,
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

on('llm.delta', (d: { stream: string; content: string; reasoning: string; tool: string }) => {
  if (d.stream !== live.stream) return
  setLive(
    produce((l) => {
      l.content += d.content
      l.reasoning += d.reasoning
      if (d.tool) l.tool = d.tool
    }),
  )
})

function systemPrompt(): string {
  const p = project()
  const active = activeTab()?.kind === 'file' ? activeTab()!.path! : ''
  return [
    `Tu es l'assistant de programmation intégré à un IDE web. Projet ouvert : « ${p?.name ?? ''} », racine ${root()}${p?.ssh ? ` sur l'hôte SSH ${p.ssh.host}` : ''}.`,
    active ? `Fichier actif dans l'éditeur : ${relPath(active)}.` : '',
    `Réponds dans la langue de l'utilisateur, en Markdown. Les blocs de code indiquent leur langage (\`\`\`go, \`\`\`ts…). Pour un schéma, utilise un bloc \`\`\`mermaid.`,
    prefs.tools && currentModel()?.caps.tools !== false
      ? [
          `Tu as des outils pour explorer et modifier le projet : list_dir, find_files, read_file, search_text, edit_file, write_file, et les serveurs de langage (lsp_symbols, lsp_workspace_symbols, lsp_definition, lsp_references, lsp_hover, lsp_diagnostics).`,
          `Lis un fichier avant de le modifier. Préfère edit_file (remplacement exact et unique) à write_file pour changer un fichier existant. Les chemins sont relatifs à la racine du projet.`,
          `N'invente pas le contenu des fichiers : vérifie avec les outils. Après une modification, résume ce qui a changé.`,
        ].join('\n')
      : '',
  ]
    .filter(Boolean)
    .join('\n')
}

/** Messages as the API expects them (fields of the page removed). */
function apiMessages(): any[] {
  const out: any[] = [{ role: 'system', content: systemPrompt() }]
  for (const m of chat.messages) {
    if (m.error && m.role === 'assistant' && !m.content && !m.tool_calls?.length) continue
    const msg: any = { role: m.role }
    if (m.content !== undefined) msg.content = m.content
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
  setChat(produce((c) => {
    c.server = config.server
    c.model = config.model
  }))
  pushMessage({ role: 'user', content, attachments: attachments?.length ? attachments : undefined })
  saveChat()
  await run()
}

/** Asks again from the last user message (after an error or a stop). */
export async function retry() {
  if (live.busy) return
  let last = chat.messages.length - 1
  while (last >= 0 && chat.messages[last].role !== 'user') last--
  if (last < 0) return
  setChat(produce((c) => c.messages.splice(last + 1)))
  await run()
}

export function stop() {
  ctrl?.abort()
  approval()?.resolve(false)
}

async function run() {
  const c = new AbortController()
  ctrl = c
  setLive({ busy: true, content: '', reasoning: '', tool: '', stream: '' })
  try {
    for (let step = 0; step < MAX_STEPS; step++) {
      const stream = newId()
      setLive({ content: '', reasoning: '', tool: '', stream })
      const model = currentModel()
      const useTools = prefs.tools && model?.caps.tools !== false
      let res: any
      try {
        res = await request(
          'llm.chat',
          {
            server: config.server,
            model: config.model,
            messages: apiMessages(),
            tools: useTools ? toolDefs : undefined,
            think: model?.caps.thinking ? prefs.think : undefined,
            stream,
          },
          c.signal,
        )
      } catch (e) {
        const canceled = e instanceof RpcError && e.code === 'canceled'
        // Keep what was already written.
        if (live.content || live.reasoning || !canceled) {
          pushMessage({
            role: 'assistant',
            content: live.content,
            reasoning_content: live.reasoning || undefined,
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
        error: res.finish === 'length' ? 'Réponse coupée : limite de longueur atteinte.' : undefined,
      })
      setLive({ content: '', reasoning: '', tool: '' })
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
    setLive({ busy: false, content: '', reasoning: '', tool: '', stream: '' })
    setApproval(null)
    saveChat()
  }
}
