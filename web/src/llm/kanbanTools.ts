// Kanban tools of the assistant: every conversation can read the tickets, create one and
// ask the user questions; a conversation linked to a ticket can also change that ticket
// (plan, goals, notes, status). See docs/kanban.md. What the model reads is in English;
// the summaries shown in the conversation are translated.
import { request } from '../pod/rpc'
import { t, tn } from '../i18n'
import {
  addNote, createTicket, getTicket, goalOp, linkCommit, moveTicket, priorityNames, refreshBoard, roleNames, setPlan, statusLabels, statusNames, typeNames, updateTicket,
  board, type Priority, type Status, type Ticket, type TicketType,
} from '../kanban/state'
import type { ToolResult } from './tools'

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

/** Longest note the model may add: notes are for decisions, not reports. */
export const MAX_NOTE = 500

const statusEnum = { type: 'string', enum: Object.keys(statusNames), description: 'Status' }

/** Tools of every conversation. */
export const kanbanReadDefs = [
  fn('kanban_list', 'Lists the tickets of the kanban of the project (number, status, type, priority, title, goals).', { status: statusEnum, query: str('Filter on the title (optional)') }),
  fn('kanban_get', 'Reads a whole ticket: description, plan, goals (with their ids), notes and test feedback, linked files, conversations, branch.', { id: { type: 'integer', description: 'Ticket number' } }, ['id']),
  fn(
    'kanban_create',
    'Creates a ticket in the backlog (status New). Use it when the user asks for it or agrees to note a task for later.',
    {
      title: str('Short title'),
      description: str('Description in Markdown: context, need, criteria'),
      type: { type: 'string', enum: Object.keys(typeNames) },
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
      description: str('New full description (Markdown)'),
      type: { type: 'string', enum: Object.keys(typeNames) },
      priority: { type: 'string', enum: Object.keys(priorityNames) },
      add_files: strList('Files to link'),
      remove_files: strList('Files to unlink'),
    },
  ),
  fn(
    'kanban_add_note',
    `Adds a short note to the linked ticket (${MAX_NOTE} characters max): a decision, a fact found, an answer of the user worth keeping. Not for progress logs, restatements of the ticket or corrections of earlier notes.`,
    { text: str(`Note in Markdown, ${MAX_NOTE} characters max`) },
    ['text'],
  ),
  fn(
    'kanban_set_plan',
    'Writes the implementation plan of the linked ticket and its goals (verifiable objectives, checked during development). Replaces the plan and the goals of a previous plan (test feedback goals stay).',
    { plan: str('Plan in Markdown: approach, files, steps, risks, tests'), goals: strList('Goals: each one verifiable, one sentence') },
    ['plan', 'goals'],
  ),
  fn(
    'kanban_goal',
    'Checks, unchecks or adds a goal of the linked ticket. Check each goal as soon as it is reached and verified.',
    { action: { type: 'string', enum: ['check', 'uncheck', 'add'] }, id: { type: 'integer', description: 'Goal id (check / uncheck), see kanban_get' }, text: str('Text of the goal (add)') },
    ['action'],
  ),
  fn(
    'kanban_move',
    'Changes the status of the linked ticket. Allowed: New → ready (after kanban_set_plan); In progress or Fix → review (To test, with test_summary). Other changes belong to the user.',
    {
      status: { type: 'string', enum: ['ready', 'review'] },
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

const isoDate = (ms: number) => new Date(ms).toISOString().slice(0, 16).replace('T', ' ')

/** A ticket as the model reads it. */
export function ticketMarkdown(tk: Ticket): string {
  const out: string[] = [`# Ticket #${tk.id} · ${tk.title}`]
  out.push(`Status: ${statusNames[tk.status]} · type: ${typeNames[tk.type]} · priority: ${priorityNames[tk.priority]}${tk.branch ? ` · branch: ${tk.branch}` : ''}${tk.base ? ` · base: ${tk.base}` : ''}`)
  out.push(`\n## Description\n${tk.description.trim() || '(empty)'}`)
  if (tk.files.length) out.push(`\n## Linked files\n${tk.files.map((f) => `- ${f}`).join('\n')}`)
  if (tk.attachments.length) out.push(`\n## Attachments\n${tk.attachments.map((a) => `- ${a.name} (${a.mime || 'file'})`).join('\n')}`)
  out.push(`\n## Plan\n${tk.plan.trim() || '(no plan yet)'}`)
  if (tk.goalList.length) out.push(`\n## Goals\n${tk.goalList.map((g) => `- [${g.done ? 'x' : ' '}] (id ${g.id}${g.source === 'feedback' ? ', test feedback' : ''}) ${g.text}`).join('\n')}`)
  if (tk.testSummary.trim()) out.push(`\n## To test\n${tk.testSummary.trim()}`)
  const notes = tk.notes.filter((n) => n.kind !== 'event')
  if (notes.length)
    out.push(`\n## Notes and feedback\n${notes.map((n) => `- ${n.kind === 'feedback' ? '**Test feedback**' : 'Note'} (${n.author === 'model' ? 'assistant' : 'user'}, ${isoDate(n.created)}): ${n.text.trim()}`).join('\n')}`)
  if (tk.chatList.length) out.push(`\n## Linked conversations\n${tk.chatList.map((c) => `- ${roleNames[c.role]}: ${c.title || c.chatId}`).join('\n')}`)
  if (tk.commits.length) out.push(`\n## Linked commits\n${tk.commits.map((c) => `- ${c.hash.slice(0, 10)} ${c.subject}`).join('\n')}`)
  return out.join('\n')
}

export async function runKanbanTool(name: string, a: Record<string, any>, ticket: number | undefined): Promise<ToolResult> {
  switch (name) {
    case 'kanban_list': {
      await refreshBoard()
      const q = String(a.query ?? '').toLowerCase()
      const list = board.tickets.filter((tk) => (!a.status || tk.status === a.status) && (!q || tk.title.toLowerCase().includes(q)))
      if (!list.length) return ok('No ticket.', tn(0, '{n} ticket', '{n} tickets'))
      const lines = list.map((tk) => `#${tk.id} [${statusNames[tk.status]}] (${typeNames[tk.type]}, ${priorityNames[tk.priority]}) ${tk.title}${tk.goals ? ` · goals ${tk.goalsDone}/${tk.goals}` : ''}`)
      return ok(lines.join('\n'), tn(list.length, '{n} ticket', '{n} tickets'))
    }
    case 'kanban_get': {
      const tk = await getTicket(Number(a.id))
      return ok(ticketMarkdown(tk), `#${tk.id} ${tk.title}`)
    }
    case 'kanban_create': {
      if (!String(a.title ?? '').trim()) throw new Error('title is missing')
      const tk = await createTicket(
        { title: String(a.title), description: a.description ? String(a.description) : '', type: a.type as TicketType, priority: a.priority as Priority, addFiles: Array.isArray(a.files) ? a.files.map(String) : undefined },
        'model',
      )
      return ok(`Ticket #${tk.id} created in the backlog (status New).`, t('#{id} created', { id: tk.id }))
    }
  }
  if (!ticket)
    return { content: 'Error: this conversation is not linked to a ticket; only kanban_list, kanban_get and kanban_create are available.', summary: t('no linked ticket'), status: 'error' }
  switch (name) {
    case 'kanban_update': {
      const tk = await updateTicket(
        ticket,
        {
          title: a.title,
          description: a.description,
          type: a.type,
          priority: a.priority,
          addFiles: Array.isArray(a.add_files) ? a.add_files.map(String) : undefined,
          removeFiles: Array.isArray(a.remove_files) ? a.remove_files.map(String) : undefined,
        },
        'model',
      )
      return ok(`Ticket #${tk.id} updated.`, t('#{id} updated', { id: tk.id }))
    }
    case 'kanban_add_note': {
      const text = String(a.text ?? '').trim()
      if (text.length > MAX_NOTE)
        return {
          content: `Error: note too long (${text.length} characters, ${MAX_NOTE} max). Keep only what is worth remembering, in a few lines; the details belong in the description (kanban_update) or the plan.`,
          summary: t('note too long'),
          status: 'error',
        }
      await addNote(ticket, 'note', text, 'model')
      return ok('Note added.', t('note added'))
    }
    case 'kanban_set_plan': {
      const goals = Array.isArray(a.goals) ? a.goals.map(String).filter((g: string) => g.trim()) : []
      if (!String(a.plan ?? '').trim()) throw new Error('empty plan')
      const tk = await setPlan(ticket, String(a.plan), goals, 'model')
      return ok(`Plan saved with ${goals.length} goal(s):\n${tk.goalList.map((g) => `- (id ${g.id}) ${g.text}`).join('\n')}`, tn(goals.length, 'plan · {n} goal', 'plan · {n} goals'))
    }
    case 'kanban_goal': {
      const action = String(a.action ?? '')
      if (action === 'add') {
        const tk = await goalOp(ticket, { op: 'add', text: String(a.text ?? ''), source: 'plan' }, 'model')
        const g = tk.goalList[tk.goalList.length - 1]
        return ok(`Goal added (id ${g?.id}).`, t('goal added'))
      }
      if (action !== 'check' && action !== 'uncheck') throw new Error('unknown action: ' + action)
      const tk = await goalOp(ticket, { op: 'check', id: Number(a.id), done: action === 'check' }, 'model')
      const g = tk.goalList.find((x) => x.id === Number(a.id))
      const left = tk.goalList.filter((x) => !x.done).length
      return ok(`Goal ${action === 'check' ? 'checked' : 'unchecked'}: ${g?.text}. ${left} goal(s) left.`, `${action === 'check' ? '☑' : '☐'} ${g?.text ?? a.id}`)
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
