// Conversations of the assistant linked to a ticket: briefing and plan in the window of
// the project, development and corrections in the worktree of the ticket (docs/kanban.md).
import { mutate, project } from '../state/project'
import { request, RpcError } from '../pod/rpc'
import { toast, errorToast } from '../ui/toast'
import { chat, config, emptyChat, live, loadConfig, openChat, resetChat, setChat, type Chat, type ChatRole } from '../llm/state'
import { resumeIfNeeded, send, stopWatch } from '../llm/agent'
import { feedbackOp, moveTicket, openWorktreeWindow, roleLabels, startWork, worktreeProject, type Feedback, type Ticket } from './state'
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

function assistantFree(): boolean {
  if (live.busy && !live.watching) {
    toast(t('An answer is running in the assistant: wait for it to end or stop it.'), 'warn')
    showAssistant()
    return false
  }
  return true
}

/** Starts a conversation linked to a ticket in this window, with its first message. */
export async function startTicketChat(tk: Ticket, role: ChatRole, text?: string, feedback?: Feedback) {
  if (!assistantFree()) return
  stopWatch()
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
export async function startWorkSession(tk: Ticket, role: ChatRole, feedback?: Feedback) {
  if (project()?.ticket === tk.id) return startTicketChat(tk, role, undefined, feedback)
  let target: string
  try {
    target = (await startWork(tk.id)).project
  } catch (e) {
    const msg = (e as Error).message
    if (e instanceof RpcError && e.code === 'not_git' && confirm(t('{error}.\n\nDevelop in the project folder, without branch or worktree?', { error: msg }))) {
      if (tk.status === 'todo') await moveTicket(tk.id, 'in_progress').catch(() => {})
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
    const now = Date.now()
    const c: Chat = {
      ...emptyChat(),
      title: `#${tk.id} ${roleLabels[role]} · ${tk.title}`.slice(0, 80),
      server: config.server,
      model: config.model,
      mode: 'build',
      ticket: { id: tk.id, role, ...(feedback ? { feedback: feedback.id } : {}) },
      messages: [{ role: 'user', content: firstMessage[role](tk, feedback) }],
      // Picked up by the worktree window as an answer to resume (resumeIfNeeded).
      running: {},
      created: now,
      updated: now,
    }
    await request('llm.chats.save', { chat: c })
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
  if (project()?.ticket === tk.id) return
  try {
    openWorktreeWindow((await worktreeProject(tk.id)).project)
  } catch (e) {
    errorToast(e)
  }
}
