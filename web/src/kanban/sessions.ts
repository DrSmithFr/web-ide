// Conversations of the assistant linked to a ticket: briefing and plan in the window of
// the project, development and corrections in the worktree of the ticket (docs/kanban.md).
import { mutate, project } from '../state/project'
import { request, RpcError } from '../pod/rpc'
import { toast, errorToast } from '../ui/toast'
import { chat, config, emptyChat, live, loadConfig, openChat, resetChat, setChat, type Chat, type ChatRole } from '../llm/state'
import { resumeIfNeeded, send, stopWatch } from '../llm/agent'
import { moveTicket, openWorktreeWindow, roleLabels, startWork, worktreeProject, type Ticket } from './state'
import { t } from '../i18n'

const firstMessage: Record<ChatRole, (tk: Ticket) => string> = {
  briefing: (k) => t("Let's do the briefing of ticket #{id} “{title}”: help me clarify it.", { id: k.id, title: k.title }),
  plan: (k) => t('Write the implementation plan of ticket #{id} “{title}” and its goals, then move it to “Ready”.', { id: k.id, title: k.title }),
  dev: (k) => t('Develop ticket #{id} “{title}” following its plan.', { id: k.id, title: k.title }),
  correction: (k) => t('Fix ticket #{id} “{title}” according to the test feedback.', { id: k.id, title: k.title }),
  resolve: (k) => t('Resolve the git conflicts of the branch of ticket #{id}.', { id: k.id }),
}

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
export async function startTicketChat(tk: Ticket, role: ChatRole, text?: string) {
  if (!assistantFree()) return
  stopWatch()
  resetChat()
  setChat({ ticket: { id: tk.id, role }, mode: role === 'briefing' || role === 'plan' ? 'plan' : 'build', title: `#${tk.id} ${roleLabels[role]} · ${tk.title}`.slice(0, 80) })
  showAssistant()
  try {
    await loadConfig()
    if (!config.server || !config.model) {
      toast(t('Choose a server and a model in the assistant, then try again.'), 'warn')
      return
    }
    await send(text ?? firstMessage[role](tk), [], [])
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
export async function startWorkSession(tk: Ticket, role: ChatRole) {
  if (project()?.ticket === tk.id) return startTicketChat(tk, role)
  let target: string
  try {
    target = (await startWork(tk.id)).project
  } catch (e) {
    const msg = (e as Error).message
    if (e instanceof RpcError && e.code === 'not_git' && confirm(t('{error}.\n\nDevelop in the project folder, without branch or worktree?', { error: msg }))) {
      if (tk.status === 'ready') await moveTicket(tk.id, 'in_progress').catch(() => {})
      return startTicketChat(tk, role)
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
      ticket: { id: tk.id, role },
      messages: [{ role: 'user', content: firstMessage[role](tk) }],
      // Picked up by the worktree window as an answer to resume (resumeIfNeeded).
      running: {},
      created: now,
      updated: now,
    }
    await request('llm.chats.save', { chat: c })
    await request('kanban.chat.link', { id: tk.id, chatId: c.id, role, title: c.title }).catch(() => {})
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
