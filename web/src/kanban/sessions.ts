// Conversations of the assistant linked to a ticket: briefing and plan in the window of
// the project, development and corrections in the worktree of the ticket (docs/kanban.md).
import { mutate } from '../state/project'
import { toast, errorToast } from '../ui/toast'
import { chat, config, live, loadConfig, openChat, resetChat, setChat, type ChatRole } from '../llm/state'
import { resumeIfNeeded, send, stopWatch } from '../llm/agent'
import { roleLabels, type Ticket } from './state'

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
