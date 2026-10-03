// System prompt of the assistant: an editable template (global, or per project in
// .ide/system-prompt.md), then the memory files (CLAUDE.md, AGENTS.md…) and the list of
// skills, loaded the same way as Claude Code does.
import { createSignal } from 'solid-js'
import { request } from '../pod/rpc'
import { activeTab, project, relPath, root } from '../state/project'
import { getTicket } from '../kanban/state'
import { ticketMarkdown } from './kanbanTools'
import type { ChatRole } from './state'

export interface InstructionFile {
  scope: 'global' | 'project'
  path: string
  content: string
}

export interface Skill {
  scope: 'global' | 'project'
  name: string
  description: string
  dir: string
}

export interface PromptContext {
  globalPrompt: string | null
  projectPrompt: string | null
  globalPlanPrompt: string | null
  projectPlanPrompt: string | null
  files: InstructionFile[]
  skills: Skill[]
}

export const DEFAULT_TEMPLATE = `You are the programming assistant built into a web IDE. Open project: "{{project}}", root {{root}}{{host}}.
{{activeFile}}
Answer in the language of the user, in Markdown. Code blocks state their language (\`\`\`go, \`\`\`ts…). For a diagram, use a \`\`\`mermaid block.

{{tools}}`

export const DEFAULT_PLAN_TEMPLATE = `You are the programming assistant built into a web IDE, in **Plan mode**. Open project: "{{project}}", root {{root}}{{host}}.
{{activeFile}}
In Plan mode you change nothing: you explore the project, ask questions when the request is ambiguous, then propose a plan.
The plan is precise and actionable: goal, files concerned (paths), numbered steps with what changes, risks and points to check, how to test. When it is ready, present it with the exit_plan_mode tool: the user can accept it to switch to Build mode and carry it out.
Answer in the language of the user, in Markdown. For a diagram, use a \`\`\`mermaid block.

{{tools}}`

export const PLAN_TOOLS_TEXT = `Reading tools: list_dir, find_files, read_file, search_text, the language servers (lsp_symbols, lsp_workspace_symbols, lsp_definition, lsp_references, lsp_hover, lsp_diagnostics), open_file and focus to show something to the user, bash for reading commands (ls, grep, git log, git diff…) and the build, test and lint commands of the project (make test, go test, npm run check…), which run freely; any other command asks the user first. edit_file and write_file are not available in Plan mode.
In the messages of the user, @path designates a file or folder of the project (path relative to the root).
When a task is done or the conversation gets long, you can summarize it with compact_conversation.
Kanban of the project: kanban_list and kanban_get read the tickets, kanban_create creates one. ask_user asks the user multiple-choice questions (up to 10) when information is missing.`

export const TOOLS_TEXT = `You have tools to explore and change the project: list_dir, find_files, read_file, search_text, edit_file, write_file; the language servers (lsp_symbols, lsp_workspace_symbols, lsp_definition, lsp_references, lsp_hover, lsp_diagnostics); bash to run your commands (tests, builds, git…); the IDE (open_file to show a file to the user, focus to show a panel or a console); the consoles visible to the user (run_command for a development server or a command they should follow, list_consoles, read_console, console_input).
In the messages of the user, @path designates a file or folder of the project (path relative to the root): read it with the tools when needed.
Read a file before changing it. Prefer edit_file (exact, unique replacement) to write_file to change an existing file. Paths are relative to the project root.
Do not make up the content of files: check with the tools. After a change, summarize what changed.
When a task is done or the conversation gets long, you can summarize it with compact_conversation to free context.
Kanban of the project: kanban_list and kanban_get read the tickets, kanban_create creates one. ask_user asks the user multiple-choice questions (up to 10) when information is missing or a choice is theirs.`

export const [promptContext, setPromptContext] = createSignal<PromptContext | null>(null)

/** What a conversation linked to a ticket must do, by role (docs/kanban.md). */
export const ROLE_INSTRUCTIONS: Record<ChatRole, string> = {
  briefing: `You do the **briefing** of this ticket with the user: understand and clarify the need before any implementation.
- Read the ticket, its linked files and the code concerned.
- Ask your questions with ask_user, grouped (up to 10), rather than one by one in the text.
- Record what you learn in the ticket: kanban_update (more precise description, linked files), kanban_add_note (decisions, answers worth keeping: a few lines each, no notes correcting earlier ones).
- Do not write the implementation plan and do not change any file: the plan comes next.`,
  plan: `You write the **implementation plan** of this ticket.
- Explore the code concerned; if essential information is missing, ask with ask_user.
- Save the plan with kanban_set_plan: text in Markdown (approach, files to change, steps, risks, tests) and a list of goals, each one a verifiable objective (visible feature, passing test…).
- Then move the ticket to "Ready" with kanban_move (status ready) and sum up the plan in a few lines.
- Do not change any file.`,
  dev: `You **develop** this ticket{{branch}}.
- Follow the plan. Check each goal with kanban_goal as soon as it is reached and verified (tests, build).
- Commit regularly on the ticket branch with bash (git add, git commit); each commit message starts with "#{{id}} ". Link each commit with kanban_link_commit.
- Do not merge or push the branch: the user does it.
- When all the goals are checked, the tests pass and everything is committed, move the ticket to "To test" with kanban_move (status review) and a test_summary: what the user must test and how (steps, commands, expected result).`,
  correction: `You **fix** this ticket after the test feedback of the user{{branch}}.
- The feedback is in the "Test feedback" notes and in the goals marked "test feedback": handle all of it, check each fixed goal with kanban_goal.
- Commit on the ticket branch (messages starting with "#{{id}} ") and link the commits with kanban_link_commit.
- Do not merge or push the branch.
- When everything is fixed and committed, move the ticket back to "To test" with kanban_move (status review) and an updated test_summary.`,
  resolve: `You **resolve the git conflicts** of the branch of this ticket{{branch}}: a rebase stopped in the worktree, or a merge stopped in the main folder of the project.
- git status lists the conflicted files: fix each file keeping both intentions, then git add.
- For a rebase: GIT_EDITOR=true git -c core.commentChar=auto rebase --continue, again while conflicts remain. For a merge: git -c core.commentChar=auto commit --no-edit.
- Check that the project builds and the tests pass, then sum up what you did in a short note (kanban_add_note).`,
}

const [ticketPrompt, setTicketPrompt] = createSignal('')
export { ticketPrompt }

/** Loads the ticket linked to the conversation for the system prompt ('' without one). */
export async function loadTicketPrompt(link: { id: number; role: ChatRole } | undefined) {
  if (!link) {
    setTicketPrompt('')
    return
  }
  try {
    const tk = await getTicket(link.id)
    const role = ROLE_INSTRUCTIONS[link.role] ?? ''
    const branch = tk.branch ? ` on the branch ${tk.branch}, in its worktree (the root of the open project)` : ''
    setTicketPrompt(
      `# Ticket linked to this conversation\nThis conversation works on ticket #${tk.id} of the kanban of the project. The tools kanban_update, kanban_add_note, kanban_set_plan, kanban_goal, kanban_move and kanban_link_commit act on this ticket.\n\n${role.replace(/\{\{branch\}\}/g, branch).replace(/\{\{id\}\}/g, String(tk.id))}\n\n${ticketMarkdown(tk)}`,
    )
  } catch (e) {
    setTicketPrompt(`# Linked ticket\nTicket #${link.id} cannot be found (${(e as Error).message}).`)
  }
}

export async function loadPromptContext(): Promise<PromptContext> {
  try {
    const c = await request<PromptContext>('llm.context')
    setPromptContext(c)
    return c
  } catch {
    const empty: PromptContext = { globalPrompt: null, projectPrompt: null, globalPlanPrompt: null, projectPlanPrompt: null, files: [], skills: [] }
    setPromptContext(empty)
    return empty
  }
}

export function templateOf(c: PromptContext | null, mode: 'plan' | 'build' = 'build'): { text: string; source: 'project' | 'global' | 'default' } {
  const project = mode === 'plan' ? c?.projectPlanPrompt : c?.projectPrompt
  const global = mode === 'plan' ? c?.globalPlanPrompt : c?.globalPrompt
  if (project?.trim()) return { text: project, source: 'project' }
  if (global?.trim()) return { text: global, source: 'global' }
  return { text: mode === 'plan' ? DEFAULT_PLAN_TEMPLATE : DEFAULT_TEMPLATE, source: 'default' }
}

function displayPath(f: InstructionFile) {
  return f.scope === 'project' ? relPath(f.path) : f.path.replace(/^\/home\/[^/]+/, '~')
}

/** Final system prompt. tools: whether the model receives the tools. */
export function buildSystemPrompt(c: PromptContext | null, tools: boolean, mode: 'plan' | 'build' = 'build'): string {
  const p = project()
  const active = activeTab()?.kind === 'file' ? activeTab()!.path! : ''
  const vars: Record<string, string> = {
    project: p?.name ?? '',
    root: root(),
    host: p?.ssh ? ` on the SSH host ${p.ssh.host}` : '',
    activeFile: active ? `Active file in the editor: ${relPath(active)}.` : '',
    date: new Date().toLocaleDateString('en-US', { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' }),
    tools: tools ? (mode === 'plan' ? PLAN_TOOLS_TEXT : TOOLS_TEXT) : '',
  }
  let text = templateOf(c, mode).text.replace(/\{\{(\w+)\}\}/g, (m, k) => (k in vars ? vars[k] : m))
  text = text.replace(/\n{3,}/g, '\n\n').trim()
  const files = c?.files ?? []
  if (files.length) {
    text += '\n\n# Instructions'
    text += '\nInstructions of the user (global) and of the project. They come before your habits; the project ones come before the global ones.'
    for (const f of files) text += `\n\n## ${f.scope === 'global' ? 'Global' : 'Project'} · ${displayPath(f)}\n${f.content.trim()}`
  }
  if (ticketPrompt()) text += '\n\n' + ticketPrompt()
  const skills = c?.skills ?? []
  if (skills.length && tools) {
    text += '\n\n# Skills'
    text += '\nAvailable skills. When a request matches one of them, load its instructions with load_skill(name) before acting; read_skill_file reads its other files.'
    for (const s of skills) text += `\n- ${s.name}: ${s.description || '(no description)'}`
  }
  return text
}
