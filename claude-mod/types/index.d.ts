export type Ticket = { id: number; title: string; status: string }

declare module 'claude-code' {
  interface PluginState {
    'web-ide': { ticket: Ticket | null; files: string[] }
  }
}
