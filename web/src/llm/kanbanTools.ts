// Kanban tools of the assistant: every conversation can read the tickets, create one (in
// Briefing mode, the conversation is linked to it) and ask the user questions; a conversation linked to a ticket can also change that ticket
// (plan, goals, notes, test feedback, status). See docs/kanban.md. What the model reads is in English;
// the summaries shown in the conversation are translated.
import { request } from '../pod/rpc'
import { t, tn } from '../i18n'
import {
  addNote, createTicket, feedbackNames, feedbackOp, getTicket, goalOp, linkChat, linkCommit, moveTicket, priorityNames, refreshBoard, roleNames, setPlan, statusLabels, statusNames, updateTicket,
  board, MAX_DESCRIPTION, MAX_NOTE, type GoalInput, type Priority, type Status, type Ticket,
} from '../kanban/state'
import { chat, setChat, type Mode } from './state'
import type { ToolResult } from './tools'
import { attachDoodles, doodlesNote } from './doodle/tickets'

const str = (description: string) => ({ type: 'string', description })
const fn = (name: string, description: string, properties: Record<string, any>, required: string[] = []) => ({
  type: 'function',
  function: { name, description, parameters: { type: 'object', properties, required } },
})
const strList = (description: string) => ({ type: 'array', items: { type: 'string' }, description })

export const MAX_QUESTIONS = 10

export const askUserDef = fn(
  'ask_user',
  `Asks the user one or more questions (1 to ${MAX_QUESTIONS}) when information is missing or a choice is theirs. Each question offers 2 to 4 choices; the user can also answer freely. After this call the turn stops until the answers come back (as the tool result). Write the questions in the user's language.`,
  {
    questions: {
      type: 'array',
      description: 'The questions, asked one at a time',
      items: {
        type: 'object',
        properties: {
          question: str('The full question, ending with a question mark'),
          header: str('Very short label (12 characters max), e.g. "Format"'),
          options: {
            type: 'array',
            description: '2 to 4 choices; put the recommended option first with "(recommended)"',
            items: { type: 'object', properties: { label: str('Choice (1 to 5 words)'), description: str('What this choice implies') }, required: ['label'] },
          },
          multiple: { type: 'boolean', description: 'Several choices allowed' },
        },
        required: ['question', 'options'],
      },
    },
  },
  ['questions'],
)

const statusEnum = { type: 'string', enum: Object.keys(statusNames), description: 'Status' }

/** Tools of every conversation. */
export const kanbanReadDefs = [
  fn('kanban_list', 'Lists the tickets of the kanban of the project (number, status, priority, title, goals, open feedback).', { status: statusEnum, query: str('Filter on the title (optional)') }),
  fn('kanban_get', 'Reads a whole ticket: description, notes, plan, goals and test feedback (with their ids), linked files, conversations, branch.', { id: { type: 'integer', description: 'Ticket number' } }, ['id']),
  fn(
    'kanban_create',
    'Creates a ticket in the backlog (status New). Use it when the user asks for it or agrees to note a task for later.',
    {
      title: str('Short title'),
      description: str(`Description in Markdown, ${MAX_DESCRIPTION} characters max: context, need, acceptance criteria`),
      priority: { type: 'string', enum: Object.keys(priorityNames) },
      files: strList('Paths of the files concerned (relative to the root)'),
    },
    ['title'],
  ),
]

/** Tools of a conversation linked to a ticket: they act on that ticket only. */
export const kanbanWriteDefs = [
  fn(
    'kanban_update',
    'Changes the ticket linked to this conversation (only the given fields).',
    {
      title: str('New title'),
      description: str(`New full description (Markdown, ${MAX_DESCRIPTION} characters max)`),
      priority: { type: 'string', enum: Object.keys(priorityNames) },
      test_summary: str('How to test the ticket (Markdown): steps, commands, expected results'),
      add_files: strList('Files to link'),
      remove_files: strList('Files to unlink'),
    },
  ),
  fn(
    'kanban_add_note',
    `Adds a short note to the linked ticket (${MAX_NOTE} characters max), linked to this conversation: a decision, a fact found, an answer of the user worth keeping. Not for progress logs, restatements of the ticket or corrections of earlier notes.`,
    { text: str(`Note in Markdown, ${MAX_NOTE} characters max`) },
    ['text'],
  ),
  fn(
    'kanban_set_plan',
    'Writes the implementation plan of the linked ticket and its goals (verifiable objectives, checked during development). Replaces the plan and the goals of a previous plan (the goals of the user stay). A New ticket moves to "To do".',
    {
      plan: str('Plan in Markdown: approach, files, steps, risks, tests'),
      goals: {
        type: 'array',
        description: 'Goals, each one verifiable',
        items: { type: 'object', properties: { title: str('Short title, one sentence'), description: str('How to check it (optional, a few lines)') }, required: ['title'] },
      },
    },
    ['plan', 'goals'],
  ),
  fn(
    'kanban_goal',
    'Checks, unchecks or adds a goal of the linked ticket. Check each goal as soon as it is reached and verified.',
    {
      action: { type: 'string', enum: ['check', 'uncheck', 'add'] },
      id: { type: 'integer', description: 'Goal id (check / uncheck), see kanban_get' },
      title: str('Title of the goal (add)'),
      description: str('How to check it (add, optional)'),
    },
    ['action'],
  ),
  fn(
    'kanban_feedback',
    'Marks a test feedback of the linked ticket as handled (done) once fixed and verified, or as open again (reopen).',
    { action: { type: 'string', enum: ['done', 'reopen'] }, id: { type: 'integer', description: 'Feedback id, see kanban_get' } },
    ['action', 'id'],
  ),
  fn(
    'kanban_move',
    'Moves the linked ticket from In progress to To test (status review), with test_summary: how to test it. Other changes belong to the user.',
    {
      status: { type: 'string', enum: ['review'] },
      test_summary: str('For review: what to test and how (steps, commands, expected results), in Markdown'),
      comment: str('Comment for the history (optional)'),
    },
    ['status'],
  ),
  fn('kanban_link_commit', 'Links a commit to the linked ticket (after a git commit).', { hash: str('Commit hash (short or full)') }, ['hash']),
]

export const kanbanToolNames = new Set([...kanbanReadDefs, ...kanbanWriteDefs].map((d) => d.function.name))

function ok(content: string, summary: string): ToolResult {
  return { content, summary, status: 'ok' }
}

/** The model tends to be verbose: a description over the limit is refused with advice. */
function tooLong(description: unknown): ToolResult | null {
  const n = typeof description === 'string' ? [...description.trim()].length : 0
  if (n <= MAX_DESCRIPTION) return null
  return {
    content: `Error: description too long (${n} characters, ${MAX_DESCRIPTION} max). Keep the context, the need and the acceptance criteria, in short sentences; decisions go in notes (kanban_add_note), the approach in the plan.`,
    summary: t('description too long'),
    status: 'error',
  }
}

const isoDate = (ms: number) => new Date(ms).toISOString().slice(0, 16).replace('T', ' ')

/** A ticket as the model reads it. */
export function ticketMarkdown(tk: Ticket): string {
  const out: string[] = [`# Ticket #${tk.id} · ${tk.title}`]
  out.push(`Status: ${statusNames[tk.status]} · priority: ${priorityNames[tk.priority]}${tk.branch ? ` · branch: ${tk.branch}` : ''}${tk.base ? ` · base: ${tk.base}` : ''}`)
  out.push(`\n## Description\n${tk.description.trim() || '(empty)'}`)
  if (tk.files.length) out.push(`\n## Linked files\n${tk.files.map((f) => `- ${f}`).join('\n')}`)
  if (tk.attachments.length) out.push(`\n## Attachments\n${tk.attachments.map((a) => `- ${a.name} (${a.mime || 'file'})`).join('\n')}`)
  const notes = tk.notes.filter((n) => n.kind !== 'event')
  if (notes.length) out.push(`\n## Notes\n${notes.map((n) => `- (${n.author === 'model' ? 'assistant' : 'user'}, ${isoDate(n.created)}) ${n.text.trim()}`).join('\n')}`)
  out.push(`\n## Plan\n${tk.plan.trim() || '(no plan yet)'}`)
  if (tk.goalList.length)
    out.push(`\n## Goals\n${tk.goalList.map((g) => `- [${g.done ? 'x' : ' '}] (id ${g.id}) ${g.text}${g.description.trim() ? `\n  ${g.description.trim().replace(/\n/g, '\n  ')}` : ''}`).join('\n')}`)
  if (tk.testSummary.trim()) out.push(`\n## How to test\n${tk.testSummary.trim()}`)
  if (tk.feedbackList.length)
    out.push(
      `\n## Test feedback\n${tk.feedbackList.map((f) => `- [${f.done ? 'x' : ' '}] (id ${f.id}, ${feedbackNames[f.kind]}, ${isoDate(f.created)}) ${f.text.trim().replace(/\n/g, '\n  ')}`).join('\n')}`,
    )
  if (tk.chatList.length) out.push(`\n## Linked conversations\n${tk.chatList.map((c) => `- ${roleNames[c.role]}: ${c.title || c.chatId}`).join('\n')}`)
  if (tk.commits.length) out.push(`\n## Linked commits\n${tk.commits.map((c) => `- ${c.hash.slice(0, 10)} ${c.subject}`).join('\n')}`)
  return out.join('\n')
}

export async function runKanbanTool(name: string, a: Record<string, any>, ticket: number | undefined, mode: Mode = 'build'): Promise<ToolResult> {
  switch (name) {
    case 'kanban_list': {
      await refreshBoard()
      const q = String(a.query ?? '').toLowerCase()
      const list = board.tickets.filter((tk) => (!a.status || tk.status === a.status) && (!q || tk.title.toLowerCase().includes(q)))
      if (!list.length) return ok('No ticket.', tn(0, '{n} ticket', '{n} tickets'))
      const lines = list.map(
        (tk) =>
          `#${tk.id} [${statusNames[tk.status]}] (${priorityNames[tk.priority]}) ${tk.title}${tk.goals ? ` · goals ${tk.goalsDone}/${tk.goals}` : ''}${tk.feedbackOpen ? ` · open feedback ${tk.feedbackOpen}` : ''}`,
      )
      return ok(lines.join('\n'), tn(list.length, '{n} ticket', '{n} tickets'))
    }
    case 'kanban_get': {
      const tk = await getTicket(Number(a.id))
      return ok(ticketMarkdown(tk), `#${tk.id} ${tk.title}`)
    }
    case 'kanban_create': {
      if (!String(a.title ?? '').trim()) throw new Error('title is missing')
      const long = tooLong(a.description)
      if (long) return long
      const tk = await createTicket(
        { title: String(a.title), description: a.description ? String(a.description) : '', priority: a.priority as Priority, addFiles: Array.isArray(a.files) ? a.files.map(String) : undefined },
        'model',
      )
      const drawn = await attachDoodles(tk.id, tk.attachments.map((x) => x.name)).catch(() => 0)
      const done = `Ticket #${tk.id} created in the backlog (status New).${doodlesNote(drawn)}`
      if (mode !== 'briefing') return ok(done, t('#{id} created', { id: tk.id }))
      // Briefing: the ticket lists this conversation; the first one created is linked to it.
      if (!chat.ticket) {
        setChat('ticket', { id: tk.id, role: 'briefing' })
        return ok(`${done} This conversation is now linked to it: kanban_update and kanban_add_note refine it.`, t('#{id} created', { id: tk.id }))
      }
      await linkChat(tk.id, chat.id, 'briefing', chat.title).catch(() => {})
      return ok(`${done} It lists this conversation as its briefing; this conversation stays linked to ticket #${chat.ticket.id}.`, t('#{id} created', { id: tk.id }))
    }
  }
  if (!ticket)
    return { content: 'Error: this conversation is not linked to a ticket; only kanban_list, kanban_get and kanban_create are available.', summary: t('no linked ticket'), status: 'error' }
  switch (name) {
    case 'kanban_update': {
      const long = tooLong(a.description)
      if (long) return long
      const tk = await updateTicket(
        ticket,
        {
          title: a.title,
          description: a.description,
          priority: a.priority,
          testSummary: a.test_summary,
          addFiles: Array.isArray(a.add_files) ? a.add_files.map(String) : undefined,
          removeFiles: Array.isArray(a.remove_files) ? a.remove_files.map(String) : undefined,
        },
        'model',
      )
      const drawn = await attachDoodles(tk.id, tk.attachments.map((x) => x.name)).catch(() => 0)
      return ok(`Ticket #${tk.id} updated.${doodlesNote(drawn)}`, t('#{id} updated', { id: tk.id }))
    }
    case 'kanban_add_note': {
      const text = String(a.text ?? '').trim()
      if ([...text].length > MAX_NOTE)
        return {
          content: `Error: note too long (${text.length} characters, ${MAX_NOTE} max). Keep only what is worth remembering, in a few lines; the details belong in the description (kanban_update) or the plan.`,
          summary: t('note too long'),
          status: 'error',
        }
      await addNote(ticket, text, 'model', chat.id)
      return ok('Note added.', t('note added'))
    }
    case 'kanban_set_plan': {
      const goals: GoalInput[] = (Array.isArray(a.goals) ? a.goals : [])
        .map((g: any) => (typeof g === 'string' ? { title: g } : { title: String(g?.title ?? ''), description: g?.description ? String(g.description) : '' }))
        .filter((g: GoalInput) => g.title.trim())
      if (!String(a.plan ?? '').trim()) throw new Error('empty plan')
      const tk = await setPlan(ticket, String(a.plan), goals, 'model')
      const moved = tk.status === 'todo' ? ' The ticket is now "To do".' : ''
      return ok(`Plan saved with ${goals.length} goal(s):${moved}\n${tk.goalList.map((g) => `- (id ${g.id}) ${g.text}`).join('\n')}`, tn(goals.length, 'plan · {n} goal', 'plan · {n} goals'))
    }
    case 'kanban_goal': {
      const action = String(a.action ?? '')
      if (action === 'add') {
        const tk = await goalOp(ticket, { op: 'add', text: String(a.title ?? a.text ?? ''), description: String(a.description ?? ''), source: 'plan' }, 'model')
        const g = tk.goalList[tk.goalList.length - 1]
        return ok(`Goal added (id ${g?.id}).`, t('goal added'))
      }
      if (action !== 'check' && action !== 'uncheck') throw new Error('unknown action: ' + action)
      const tk = await goalOp(ticket, { op: 'check', id: Number(a.id), done: action === 'check' }, 'model')
      const g = tk.goalList.find((x) => x.id === Number(a.id))
      const left = tk.goalList.filter((x) => !x.done).length
      return ok(`Goal ${action === 'check' ? 'checked' : 'unchecked'}: ${g?.text}. ${left} goal(s) left.`, `${action === 'check' ? '☑' : '☐'} ${g?.text ?? a.id}`)
    }
    case 'kanban_feedback': {
      const action = String(a.action ?? '')
      if (action !== 'done' && action !== 'reopen') throw new Error('unknown action: ' + action)
      const tk = await feedbackOp(ticket, { op: 'check', id: Number(a.id), done: action === 'done' }, 'model')
      const f = tk.feedbackList.find((x) => x.id === Number(a.id))
      const left = tk.feedbackList.filter((x) => !x.done).length
      return ok(`Feedback ${action === 'done' ? 'marked done' : 'reopened'}: ${f?.text.slice(0, 80)}. ${left} open feedback left.`, `${action === 'done' ? '☑' : '☐'} ${f?.text.slice(0, 60) ?? a.id}`)
    }
    case 'kanban_move': {
      const status = String(a.status ?? '') as Status
      if (status === 'review') {
        const summary = String(a.test_summary ?? '').trim()
        if (!summary) throw new Error('test_summary is required to move to "To test"')
        await updateTicket(ticket, { testSummary: summary }, 'model')
      }
      const tk = await moveTicket(ticket, status, 'model', String(a.comment ?? ''))
      return ok(`Ticket #${tk.id} moved to "${statusNames[tk.status]}".`, `→ ${statusLabels[tk.status]}`)
    }
    case 'kanban_link_commit': {
      const hash = String(a.hash ?? '').trim()
      const r = await request<{ output: string; code: number }>('exec.run', { command: `git log -1 --format='%H%x1f%s' ${JSON.stringify(hash)}`, timeout: 20 })
      const [full, subject] = r.output.trim().split('\x1f')
      if (r.code !== 0 || !full) throw new Error(`commit not found: ${hash}`)
      await linkCommit(ticket, full, subject ?? '', 'model')
      return ok(`Commit ${full.slice(0, 10)} linked to the ticket.`, t('commit {hash}', { hash: full.slice(0, 8) }))
    }
  }
  throw new Error('unknown tool: ' + name)
}
