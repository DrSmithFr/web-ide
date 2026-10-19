// The services the core still provides, until AI is a plugin (#123): the conversation of the
// assistant and the Agents tool, which the Kanban plugin starts and shows.
import { provide } from './capabilities'
import { chat, config, loadConfig, resetChat, setChat } from '../llm/state'
import { openChat, send } from '../llm/assistant'
import { catalog, setActive } from '../agents/state'
import { TicketAgentStats } from '../agents/stats'

provide('core', 'assistant', {
  chat: () => chat,
  config: () => config,
  loadConfig,
  newChat: (o) => {
    resetChat()
    setChat(o as any)
  },
  send: (text) => send(text, [], []),
  openChat: (id) => openChat(id),
})

provide('core', 'agents', { catalog, show: setActive, TicketStats: TicketAgentStats })
