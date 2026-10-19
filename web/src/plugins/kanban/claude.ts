// Claude Code on a ticket (or a milestone): a terminal of the IDE runs `claude` with one of the prompts of
// the MCP endpoint of the pod (docs/design/kanban.md, Claude Code). Briefing, planning and review ask
// for Opus; development and fixes keep the default model of Claude Code.
import type { Ticket } from './state'
import type { ClaudeModel } from './route'
import { capability, request, root } from './plugin'

type Role = 'brief' | 'plan' | 'dev' | 'fix' | 'review'

const opus: Record<Role, boolean> = { brief: true, plan: true, dev: false, fix: false, review: true }

/** Runs Claude Code on a ticket in a terminal: in its worktree for development; a fix may
 * name one feedback; model: the one of the complexity (else Opus for the roles that ask for it). */
export function runClaude(tk: Ticket, role: Role, feedback?: number, model?: ClaudeModel) {
  const cwd = (role === 'dev' || role === 'fix' || role === 'review') && tk.worktree ? tk.worktree : root()
  const m = model ?? (opus[role] ? 'opus' : undefined)
  const args = [...(m ? ['--model', m] : []), `/mcp__web-ide__${role} ${tk.id}${feedback ? ` ${feedback}` : ''}`]
  // WEBIDE_CLAUDE (environment of the pod) names another command: the fake one of the tests.
  const script = 'c="${WEBIDE_CLAUDE:-claude}"; command -v "$c" >/dev/null || { echo "$c: not found in the PATH of the pod"; exit 127; }; exec "$c" "$@"'
  void capability('terminal')?.open({ kind: 'terminal', cwd, command: ['sh', '-c', script, 'sh', ...args], title: `Claude #${tk.id} ${role}` })
}

/** The command adding the MCP endpoint of this pod to Claude Code (the token stays on disk). */
export async function mcpAddCommand(): Promise<string> {
  const ws = await request<{ dataDir: string }>('workspace.get')
  return `claude mcp add --transport http --scope user web-ide ${location.origin}/mcp --header "Authorization: Bearer $(cat ${ws.dataDir}/token)"`
}

/** Runs Claude Code on a milestone in a terminal of the project: the review of the e2e
 *  coverage of its user stories (Opus unless a model is chosen). */
export function runClaudeMilestone(id: number, model?: ClaudeModel) {
  const args = ['--model', model ?? 'opus', `/mcp__web-ide__coverage ${id}`]
  const script = 'c="${WEBIDE_CLAUDE:-claude}"; command -v "$c" >/dev/null || { echo "$c: not found in the PATH of the pod"; exit 127; }; exec "$c" "$@"'
  void capability('terminal')?.open({ kind: 'terminal', cwd: root(), command: ['sh', '-c', script, 'sh', ...args], title: `Claude milestone ${id} coverage` })
}
