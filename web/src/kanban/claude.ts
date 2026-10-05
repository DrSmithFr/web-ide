// Claude Code on a ticket: a terminal of the IDE runs `claude` with one of the prompts of
// the MCP endpoint of the pod (docs/kanban.md, Claude Code). Briefing and planning ask for
// Opus; development and fixes keep the default model of Claude Code.
import { request } from '../pod/rpc'
import { root } from '../state/project'
import { newConsole } from '../console/consoles'
import type { MenuItem } from '../ui/overlay'
import type { Ticket } from './state'
import { t } from '../i18n'

type Role = 'brief' | 'plan' | 'dev' | 'fix'

const opus: Record<Role, boolean> = { brief: true, plan: true, dev: false, fix: false }

/** Runs Claude Code on a ticket in a terminal: in its worktree for development. */
export function runClaude(tk: Ticket, role: Role) {
  const cwd = (role === 'dev' || role === 'fix') && tk.worktree ? tk.worktree : root()
  const args = [...(opus[role] ? ['--model', 'opus'] : []), `/mcp__web-ide__${role} ${tk.id}`]
  // WEBIDE_CLAUDE (environment of the pod) names another command: the fake one of the tests.
  const script = 'c="${WEBIDE_CLAUDE:-claude}"; command -v "$c" >/dev/null || { echo "$c: not found in the PATH of the pod"; exit 127; }; exec "$c" "$@"'
  void newConsole({ kind: 'terminal', cwd, command: ['sh', '-c', script, 'sh', ...args], title: `Claude #${tk.id} ${role}` })
}

/** Menu items for the status of a ticket. */
export function claudeItems(tk: Ticket): MenuItem[] {
  const item = (role: Role, label: string): MenuItem => ({ label, action: () => runClaude(tk, role) })
  switch (tk.status) {
    case 'new':
      return [item('brief', t('Check the briefing (Opus)')), item('plan', t('Write the plan (Opus)'))]
    case 'todo':
      return [item('plan', t('Redo the plan (Opus)')), item('dev', t('Develop the ticket'))]
    case 'in_progress':
      return [item('dev', t('Develop the ticket'))]
    case 'review':
      return [item('fix', t('Handle the test feedback')), item('dev', t('Develop the ticket'))]
    default:
      return []
  }
}

/** The command adding the MCP endpoint of this pod to Claude Code (the token stays on disk). */
export async function mcpAddCommand(): Promise<string> {
  const ws = await request<{ dataDir: string }>('workspace.get')
  return `claude mcp add --transport http --scope user web-ide ${location.origin}/mcp --header "Authorization: Bearer $(cat ${ws.dataDir}/token)"`
}
