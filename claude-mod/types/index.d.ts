export type Ticket = { id: number; title: string; status: string; url: string }

/** The IDE project Claude works in: its page, and the ticket of its worktree. */
export type Ide = { project: string; ticket: Ticket | null }

declare module 'claude-code' {
  interface PluginState {
    'web-ide': { ide: Ide | null; files: string[] }
  }
}
