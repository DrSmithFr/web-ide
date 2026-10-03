// Kanban tools of the assistant: every conversation can read the tickets, create one and
// ask the user questions; a conversation linked to a ticket can also change that ticket
// (plan, goals, notes, status). See docs/kanban.md.
import { request } from '../pod/rpc'
import {
  addNote, createTicket, getTicket, goalOp, linkCommit, moveTicket, priorityLabels, refreshBoard, roleLabels, setPlan, statusLabels, typeLabels, updateTicket,
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
  `Pose une ou plusieurs questions à l'utilisateur (1 à ${MAX_QUESTIONS}) quand une information manque ou qu'un choix lui revient. Chaque question propose 2 à 4 choix ; l'utilisateur peut aussi répondre librement. Après cet appel, le tour s'arrête jusqu'à ses réponses (renvoyées comme résultat de l'outil).`,
  {
    questions: {
      type: 'array',
      description: 'Les questions, posées une à une',
      items: {
        type: 'object',
        properties: {
          question: str('La question complète, terminée par un point d’interrogation'),
          header: str('Étiquette très courte (12 caractères max), ex. « Format »'),
          options: {
            type: 'array',
            description: '2 à 4 choix ; mettre l’option recommandée en premier avec « (recommandé) »',
            items: { type: 'object', properties: { label: str('Choix (1 à 5 mots)'), description: str('Ce que ce choix implique') }, required: ['label'] },
          },
          multiple: { type: 'boolean', description: 'Plusieurs choix possibles' },
        },
        required: ['question', 'options'],
      },
    },
  },
  ['questions'],
)

const statusEnum = { type: 'string', enum: Object.keys(statusLabels), description: 'État' }

/** Tools of every conversation. */
export const kanbanReadDefs = [
  fn('kanban_list', 'Liste les tickets du kanban du projet (numéro, état, type, priorité, titre, goals).', { status: statusEnum, query: str('Filtre sur le titre (facultatif)') }),
  fn('kanban_get', 'Lit un ticket complet : description, plan, goals (avec leurs id), notes et retours de test, fichiers liés, conversations, branche.', { id: { type: 'integer', description: 'Numéro du ticket' } }, ['id']),
  fn(
    'kanban_create',
    'Crée un ticket dans le backlog (état Nouveau). À utiliser quand l’utilisateur le demande ou accepte de noter une tâche pour plus tard.',
    {
      title: str('Titre court'),
      description: str('Description en Markdown : contexte, besoin, critères'),
      type: { type: 'string', enum: Object.keys(typeLabels) },
      priority: { type: 'string', enum: Object.keys(priorityLabels) },
      files: strList('Chemins des fichiers concernés (relatifs à la racine)'),
    },
    ['title'],
  ),
]

/** Tools of a conversation linked to a ticket: they act on that ticket only. */
export const kanbanWriteDefs = [
  fn(
    'kanban_update',
    'Modifie le ticket lié à cette conversation (champs donnés seulement).',
    {
      title: str('Nouveau titre'),
      description: str('Nouvelle description complète (Markdown)'),
      type: { type: 'string', enum: Object.keys(typeLabels) },
      priority: { type: 'string', enum: Object.keys(priorityLabels) },
      add_files: strList('Fichiers à lier'),
      remove_files: strList('Fichiers à délier'),
    },
  ),
  fn('kanban_add_note', 'Ajoute une note au ticket lié (contexte découvert, décision, réponse de l’utilisateur à garder).', { text: str('Note en Markdown') }, ['text']),
  fn(
    'kanban_set_plan',
    'Écrit le plan d’implémentation du ticket lié et ses goals (objectifs vérifiables, cochés pendant le développement). Remplace le plan et les goals issus d’un plan précédent (les retours de test restent).',
    { plan: str('Plan en Markdown : approche, fichiers, étapes, risques, tests'), goals: strList('Goals : chacun vérifiable, une phrase') },
    ['plan', 'goals'],
  ),
  fn(
    'kanban_goal',
    'Coche, décoche ou ajoute un goal du ticket lié. Coche chaque goal dès qu’il est atteint et vérifié.',
    { action: { type: 'string', enum: ['check', 'uncheck', 'add'] }, id: { type: 'integer', description: 'Id du goal (check / uncheck), voir kanban_get' }, text: str('Texte du goal (add)') },
    ['action'],
  ),
  fn(
    'kanban_move',
    'Change l’état du ticket lié. Permis : Nouveau → ready (À développer, après kanban_set_plan) ; En cours ou Correction → review (À tester, avec test_summary). Les autres changements reviennent à l’utilisateur.',
    {
      status: { type: 'string', enum: ['ready', 'review'] },
      test_summary: str('Pour review : ce qu’il faut tester et comment (étapes, commandes, résultats attendus), en Markdown'),
      comment: str('Commentaire pour l’historique (facultatif)'),
    },
    ['status'],
  ),
  fn('kanban_link_commit', 'Lie un commit au ticket lié (après un git commit).', { hash: str('Hash du commit (court ou complet)') }, ['hash']),
]

export const kanbanToolNames = new Set([...kanbanReadDefs, ...kanbanWriteDefs].map((t) => t.function.name))

function ok(content: string, summary: string): ToolResult {
  return { content, summary, status: 'ok' }
}

const fmtDate = (t: number) => new Date(t).toLocaleString('fr-FR')

/** A ticket as the model reads it. */
export function ticketMarkdown(t: Ticket): string {
  const out: string[] = [`# Ticket #${t.id} · ${t.title}`]
  out.push(`État : ${statusLabels[t.status]} · type : ${typeLabels[t.type]} · priorité : ${priorityLabels[t.priority]}${t.branch ? ` · branche : ${t.branch}` : ''}${t.base ? ` · base : ${t.base}` : ''}`)
  out.push(`\n## Description\n${t.description.trim() || '(vide)'}`)
  if (t.files.length) out.push(`\n## Fichiers liés\n${t.files.map((f) => `- ${f}`).join('\n')}`)
  if (t.attachments.length) out.push(`\n## Pièces jointes\n${t.attachments.map((a) => `- ${a.name} (${a.mime || 'fichier'})`).join('\n')}`)
  out.push(`\n## Plan\n${t.plan.trim() || '(pas encore de plan)'}`)
  if (t.goalList.length) out.push(`\n## Goals\n${t.goalList.map((g) => `- [${g.done ? 'x' : ' '}] (id ${g.id}${g.source === 'feedback' ? ', retour de test' : ''}) ${g.text}`).join('\n')}`)
  if (t.testSummary.trim()) out.push(`\n## À tester\n${t.testSummary.trim()}`)
  const notes = t.notes.filter((n) => n.kind !== 'event')
  if (notes.length)
    out.push(`\n## Notes et retours\n${notes.map((n) => `- ${n.kind === 'feedback' ? '**Retour de test**' : 'Note'} (${n.author === 'model' ? 'assistant' : 'utilisateur'}, ${fmtDate(n.created)}) : ${n.text.trim()}`).join('\n')}`)
  if (t.chatList.length) out.push(`\n## Conversations liées\n${t.chatList.map((c) => `- ${roleLabels[c.role]} : ${c.title || c.chatId}`).join('\n')}`)
  if (t.commits.length) out.push(`\n## Commits liés\n${t.commits.map((c) => `- ${c.hash.slice(0, 10)} ${c.subject}`).join('\n')}`)
  return out.join('\n')
}

export async function runKanbanTool(name: string, a: Record<string, any>, ticket: number | undefined): Promise<ToolResult> {
  switch (name) {
    case 'kanban_list': {
      await refreshBoard()
      const q = String(a.query ?? '').toLowerCase()
      const list = board.tickets.filter((t) => (!a.status || t.status === a.status) && (!q || t.title.toLowerCase().includes(q)))
      if (!list.length) return ok('Aucun ticket.', '0 ticket')
      const lines = list.map((t) => `#${t.id} [${statusLabels[t.status]}] (${typeLabels[t.type]}, ${priorityLabels[t.priority]}) ${t.title}${t.goals ? ` · goals ${t.goalsDone}/${t.goals}` : ''}`)
      return ok(lines.join('\n'), `${list.length} ticket${list.length > 1 ? 's' : ''}`)
    }
    case 'kanban_get': {
      const t = await getTicket(Number(a.id))
      return ok(ticketMarkdown(t), `#${t.id} ${t.title}`)
    }
    case 'kanban_create': {
      if (!String(a.title ?? '').trim()) throw new Error('title manquant')
      const t = await createTicket(
        { title: String(a.title), description: a.description ? String(a.description) : '', type: a.type as TicketType, priority: a.priority as Priority, addFiles: Array.isArray(a.files) ? a.files.map(String) : undefined },
        'model',
      )
      return ok(`Ticket #${t.id} créé dans le backlog (état Nouveau).`, `#${t.id} créé`)
    }
  }
  if (!ticket) return { content: 'Erreur : cette conversation n’est liée à aucun ticket ; seuls kanban_list, kanban_get et kanban_create sont disponibles.', summary: 'aucun ticket lié', status: 'error' }
  switch (name) {
    case 'kanban_update': {
      const t = await updateTicket(
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
      return ok(`Ticket #${t.id} modifié.`, `#${t.id} modifié`)
    }
    case 'kanban_add_note':
      await addNote(ticket, 'note', String(a.text ?? ''), 'model')
      return ok('Note ajoutée.', 'note ajoutée')
    case 'kanban_set_plan': {
      const goals = Array.isArray(a.goals) ? a.goals.map(String).filter((g: string) => g.trim()) : []
      if (!String(a.plan ?? '').trim()) throw new Error('plan vide')
      const t = await setPlan(ticket, String(a.plan), goals, 'model')
      return ok(`Plan enregistré avec ${goals.length} goal(s) :\n${t.goalList.map((g) => `- (id ${g.id}) ${g.text}`).join('\n')}`, `plan · ${goals.length} goals`)
    }
    case 'kanban_goal': {
      const action = String(a.action ?? '')
      if (action === 'add') {
        const t = await goalOp(ticket, { op: 'add', text: String(a.text ?? ''), source: 'plan' }, 'model')
        const g = t.goalList[t.goalList.length - 1]
        return ok(`Goal ajouté (id ${g?.id}).`, 'goal ajouté')
      }
      if (action !== 'check' && action !== 'uncheck') throw new Error('action inconnue : ' + action)
      const t = await goalOp(ticket, { op: 'check', id: Number(a.id), done: action === 'check' }, 'model')
      const g = t.goalList.find((x) => x.id === Number(a.id))
      const left = t.goalList.filter((x) => !x.done).length
      return ok(`Goal ${action === 'check' ? 'coché' : 'décoché'} : ${g?.text}. Reste ${left} goal(s) à atteindre.`, `${action === 'check' ? '☑' : '☐'} ${g?.text ?? a.id}`)
    }
    case 'kanban_move': {
      const status = String(a.status ?? '') as Status
      if (status === 'review') {
        const summary = String(a.test_summary ?? '').trim()
        if (!summary) throw new Error('test_summary est obligatoire pour passer « À tester »')
        await updateTicket(ticket, { testSummary: summary }, 'model')
      }
      const t = await moveTicket(ticket, status, 'model', String(a.comment ?? ''))
      return ok(`Ticket #${t.id} passé à « ${statusLabels[t.status]} ».`, `→ ${statusLabels[t.status]}`)
    }
    case 'kanban_link_commit': {
      const hash = String(a.hash ?? '').trim()
      const r = await request<{ output: string; code: number }>('exec.run', { command: `git log -1 --format='%H%x1f%s' ${JSON.stringify(hash)}`, timeout: 20 })
      const [full, subject] = r.output.trim().split('\x1f')
      if (r.code !== 0 || !full) throw new Error(`commit introuvable : ${hash}`)
      await linkCommit(ticket, full, subject ?? '', 'model')
      return ok(`Commit ${full.slice(0, 10)} lié au ticket.`, `commit ${full.slice(0, 8)}`)
    }
  }
  throw new Error('outil inconnu : ' + name)
}
