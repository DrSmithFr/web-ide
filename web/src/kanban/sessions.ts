// Conversations of the assistant linked to a ticket: briefing and plan in the window of
// the project, development and corrections in the worktree of the ticket (docs/kanban.md).
import { mutate, project } from '../state/project'
import { request, RpcError } from '../pod/rpc'
import { toast, errorToast } from '../ui/toast'
import { chat, config, emptyChat, loadConfig, resetChat, setChat, type ChatRole } from '../llm/state'
import { agentOptions, openChat, send } from '../llm/agent'
import { feedbackOp, inWorktreeOf, moveTicket, openWorktreeWindow, roleLabels, startWork, worktreeProject, type Feedback, type Ticket } from './state'
import { t } from '../i18n'

const firstMessage: Record<ChatRole, (tk: Ticket, f?: Feedback) => string> = {
  briefing: (k) => t("Let's do the briefing of ticket #{id} “{title}”: help me clarify it.", { id: k.id, title: k.title }),
  plan: (k) => t('Write the implementation plan of ticket #{id} “{title}” and its goals.', { id: k.id, title: k.title }),
  dev: (k) => t('Develop ticket #{id} “{title}” following its plan.', { id: k.id, title: k.title }),
  correction: (k, f) =>
    f
      ? t('Handle this test feedback of ticket #{id} “{title}”:\n\n{text}', { id: k.id, title: k.title, text: f.text })
      : t('Handle the open test feedback of ticket #{id} “{title}”.', { id: k.id, title: k.title }),
  resolve: (k) => t('Resolve the git conflicts of the branch of ticket #{id}.', { id: k.id }),
}

/** The conversation handling a feedback is recorded on it. */
const linkFeedback = (tk: Ticket, f: Feedback | undefined, chatId: string) => (f ? feedbackOp(tk.id, { op: 'chat', id: f.id, chatId }).catch(() => {}) : undefined)

function showAssistant() {
  mutate((s) => (s.right.panel = 'assistant'))
}

/** Starts a conversation linked to a ticket in this window, with its first message. */
export async function startTicketChat(tk: Ticket, role: ChatRole, text?: string, feedback?: Feedback) {
  resetChat()
  void linkFeedback(tk, feedback, chat.id)
  setChat({ ticket: { id: tk.id, role, ...(feedback ? { feedback: feedback.id } : {}) }, mode: role === 'briefing' ? 'briefing' : role === 'plan' ? 'plan' : 'build', title: `#${tk.id} ${roleLabels[role]} · ${tk.title}`.slice(0, 80) })
  showAssistant()
  try {
    await loadConfig()
    if (!config.server || !config.model) {
      toast(t('Choose a server and a model in the assistant, then try again.'), 'warn')
      return
    }
    await send(text ?? firstMessage[role](tk, feedback), [], [])
  } catch (e) {
    errorToast(e)
  }
}

/** Opens a conversation of a ticket in the assistant of this window. */
export async function openTicketChat(chatId: string) {
  showAssistant()
  if (chat.id === chatId) return
  try {
    await openChat(chatId)
  } catch (e) {
    errorToast(e)
  }
}

/**
 * Development, correction or conflict resolution: the conversation runs in the window of
 * the worktree of the ticket (created on the way). From that window it starts at once;
 * from another one, the pod starts it in the worktree and the worktree window opens on it.
 */
export async function startWorkSession(tk: Ticket, role: ChatRole, feedback?: Feedback, force = false) {
  if (project()?.ticket === tk.id) return startTicketChat(tk, role, undefined, feedback)
  let target: string
  try {
    target = (await startWork(tk.id, '', force)).project
    // A step of a lineage started from the window of its worktree goes on there.
    if (inWorktreeOf(tk)) return startTicketChat(tk, role, undefined, feedback)
  } catch (e) {
    const msg = (e as Error).message
    if (e instanceof RpcError && e.code === 'not_git' && confirm(t('{error}.\n\nDevelop in the project folder, without branch or worktree?', { error: msg }))) {
      if (tk.status === 'todo') await moveTicket(tk.id, 'in_progress', 'user', '', force).catch(() => {})
      return startTicketChat(tk, role, undefined, feedback)
    }
    errorToast(e)
    return
  }
  try {
    await loadConfig()
    if (!config.server || !config.model) {
      toast(t('Choose a server and a model in the assistant, then try again.'), 'warn')
      openWorktreeWindow(target)
      return
    }
    // The conversation runs in the pod, in the worktree; its window shows it.
    const id = emptyChat().id
    const ticket = { id: tk.id, role, ...(feedback ? { feedback: feedback.id } : {}) }
    const title = `#${tk.id} ${roleLabels[role]} · ${tk.title}`.slice(0, 80)
    await request('agent.send', { id, text: firstMessage[role](tk, feedback), server: config.server, model: config.model, mode: 'build', options: agentOptions(), ticket, title, project: target })
    const c = { id, title }
    await request('kanban.chat.link', { id: tk.id, chatId: c.id, role, title: c.title }).catch(() => {})
    await linkFeedback(tk, feedback, c.id)
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
export async function openWorktree(tk: Ticket) {
  if (inWorktreeOf(tk)) return
  try {
    openWorktreeWindow((await worktreeProject(tk.id)).project)
  } catch (e) {
    errorToast(e)
  }
}
