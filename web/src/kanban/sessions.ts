// Conversations of the assistant linked to a ticket: briefing and plan in the window of
// the project, development and corrections in the worktree of the ticket (docs/kanban.md).
import { mutate, project } from '../state/project'
import { request } from '../pod/rpc'
import { toast, errorToast } from '../ui/toast'
import { chat, config, emptyChat, live, loadConfig, openChat, resetChat, setChat, type Chat, type ChatRole } from '../llm/state'
import { resumeIfNeeded, send, stopWatch } from '../llm/agent'
import { moveTicket, openWorktreeWindow, roleLabels, startWork, worktreeProject, type Ticket } from './state'

const firstMessage: Record<ChatRole, (t: Ticket) => string> = {
  briefing: (t) => `Faisons le briefing du ticket #${t.id} « ${t.title} » : aide-moi à le préciser.`,
  plan: (t) => `Rédige le plan d'implémentation du ticket #${t.id} « ${t.title} » et ses goals, puis passe-le à « À développer ».`,
  dev: (t) => `Développe le ticket #${t.id} « ${t.title} » en suivant son plan.`,
  correction: (t) => `Corrige le ticket #${t.id} « ${t.title} » d'après les retours de test.`,
  resolve: (t) => `Résous les conflits du rebase de la branche du ticket #${t.id}.`,
}

function showAssistant() {
  mutate((s) => (s.right.panel = 'assistant'))
}

function assistantFree(): boolean {
  if (live.busy && !live.watching) {
    toast('Une réponse est en cours dans l’assistant : attendre sa fin ou l’arrêter.', 'warn')
    showAssistant()
    return false
  }
  return true
}

/** Starts a conversation linked to a ticket in this window, with its first message. */
export async function startTicketChat(t: Ticket, role: ChatRole, text?: string) {
  if (!assistantFree()) return
  stopWatch()
  resetChat()
  setChat({ ticket: { id: t.id, role }, mode: role === 'briefing' || role === 'plan' ? 'plan' : 'build', title: `#${t.id} ${roleLabels[role]} · ${t.title}`.slice(0, 80) })
  showAssistant()
  try {
    await loadConfig()
    if (!config.server || !config.model) {
      toast('Choisir un serveur et un modèle dans l’assistant, puis relancer.', 'warn')
      return
    }
    await send(text ?? firstMessage[role](t), [], [])
  } catch (e) {
    errorToast(e)
  }
}

/** Opens a conversation of a ticket in the assistant of this window. */
export async function openTicketChat(chatId: string) {
  if (chat.id !== chatId && !assistantFree()) return
  showAssistant()
  if (chat.id === chatId) return
  try {
    stopWatch()
    await openChat(chatId)
    await resumeIfNeeded()
  } catch (e) {
    errorToast(e)
  }
}

/**
 * Development, correction or conflict resolution: the conversation runs in the window of
 * the worktree of the ticket (created on the way). From that window it starts at once;
 * from another one, the conversation is saved with its first message and the worktree
 * window opens on it and runs it.
 */
export async function startWorkSession(t: Ticket, role: ChatRole) {
  if (project()?.ticket === t.id) return startTicketChat(t, role)
  let target: string
  try {
    target = (await startWork(t.id)).project
  } catch (e) {
    const msg = (e as Error).message
    if (/pas un dépôt git/.test(msg) && confirm(`${msg}.\n\nDévelopper dans le dossier du projet, sans branche ni worktree ?`)) {
      if (t.status === 'ready') await moveTicket(t.id, 'in_progress').catch(() => {})
      return startTicketChat(t, role)
    }
    errorToast(e)
    return
  }
  try {
    await loadConfig()
    if (!config.server || !config.model) {
      toast('Choisir un serveur et un modèle dans l’assistant, puis relancer.', 'warn')
      openWorktreeWindow(target)
      return
    }
    const now = Date.now()
    const c: Chat = {
      ...emptyChat(),
      title: `#${t.id} ${roleLabels[role]} · ${t.title}`.slice(0, 80),
      server: config.server,
      model: config.model,
      mode: 'build',
      ticket: { id: t.id, role },
      messages: [{ role: 'user', content: firstMessage[role](t) }],
      // Picked up by the worktree window as an answer to resume (resumeIfNeeded).
      running: {},
      created: now,
      updated: now,
    }
    await request('llm.chats.save', { chat: c })
    await request('kanban.chat.link', { id: t.id, chatId: c.id, role, title: c.title }).catch(() => {})
    try {
      localStorage.setItem(`webide.llm.active.${target}`, c.id)
    } catch {
      /* private mode: the window opens on a new conversation */
    }
    openWorktreeWindow(target, true)
  } catch (e) {
    errorToast(e)
  }
}

/** Opens the window of the worktree of a ticket. */
export async function openWorktree(t: Ticket) {
  if (project()?.ticket === t.id) return
  try {
    openWorktreeWindow((await worktreeProject(t.id)).project)
  } catch (e) {
    errorToast(e)
  }
}
