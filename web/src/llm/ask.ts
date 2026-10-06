// ask_user, page side: the answer of a question as the card keeps it, and as a string (the
// English words the pod gives back to the model, which validates the questions and writes
// the text of the answers: pod/internal/agent/ask.go).
import { t } from '../i18n'
import type { Question } from './state'

/** The words the user can answer with instead of an option (English: the model receives them). */
export const DONT_KNOW = "I don't know"
export const UP_TO_YOU = 'Up to you'

/** One answer of one question, as sent back to the model. */
export type AnswerEntry =
  | { kind: 'choices'; value: string[] }
  | { kind: 'free'; value: string }
  | { kind: 'idea'; value: 'No' | 'Yes' | 'Exactly' | `Yes, but: ${string}` }
  | { kind: 'rank'; value: string[]; top?: number }
  | { kind: 'dontknow' }
  | { kind: 'uptoyou' }

/**
 * The answer as one string, the English words the model receives (the recap and the
 * folded card translate it back through the UI).
 */
export const entryToString = (a: AnswerEntry | null): string => {
  if (!a) return ''
  if (a.kind === 'dontknow') return DONT_KNOW
  if (a.kind === 'uptoyou') return UP_TO_YOU
  if (a.kind === 'rank') return (a.top ? a.value.slice(0, a.top) : a.value).map((x, i) => `${i + 1}. ${x}`).join(', ')
  if (a.kind === 'choices') return a.value.join(' ; ')
  return a.value
}

/** The answer as the user reads it (recap and folded card), through t(). */
export function answerLabel(a: AnswerEntry | null): string {
  if (!a) return t('(no answer)')
  switch (a.kind) {
    case 'dontknow':
      return t("I don't know")
    case 'uptoyou':
      return t('Up to you')
    case 'rank':
      return (a.top ? a.value.slice(0, a.top) : a.value).map((x, i) => `${i + 1}. ${x}`).join(', ')
    case 'idea': {
      const v = a.value
      if (v.startsWith('Yes, but: ')) return v === 'Yes, but: ' ? t('Yes, but…') : `${t('Yes, but…')} ${v.slice('Yes, but: '.length)}`
      return v === 'No' ? t('No') : v === 'Exactly' ? t('Exactly') : t('Yes')
    }
    case 'free':
      return a.value
    case 'choices':
      return a.value.join(' ; ')
  }
}

/** The special answers, in the user's language (for the quick buttons). */
export const dontKnowLabel = () => t("I don't know")
export const upToYouLabel = () => t('Up to you')

// ---------- graphs of questions ----------
// An option (for an idea: nextYes / nextNo) names the id of the question asked when it is
// chosen; the questions nothing leads to are asked in order. Same rules as the pod
// (pod/internal/agent/graph.go), which checks the graph and writes the path to the model.

/** The ids a question leads to, with the answer leading there. */
function nexts(q: Question): [string, string][] {
  const out: [string, string][] = q.options.filter((o) => o.next).map((o) => [o.label, o.next!])
  if (q.nextYes) out.push(['Yes', q.nextYes], ['Exactly', q.nextYes])
  if (q.nextNo) out.push(['No', q.nextNo])
  return out
}

export const hasBranches = (q: Question) => nexts(q).length > 0
export const isGraph = (qs: Question[]) => qs.some(hasBranches)

/** A step of a breadcrumb: the question and the answer that led further. */
export interface Crumb {
  q: number
  label: string
}

/**
 * The questions to ask, in order, given what is answered: the roots in order, each followed
 * (depth first) by the questions its chosen answers lead to; with the breadcrumb of each.
 * `picked` gives the labels chosen for a question (options, or the idea answer).
 */
export function walk(qs: Question[], picked: (i: number) => string[]): { seq: number[]; crumbs: Crumb[][] } {
  const index = new Map<string, number>()
  qs.forEach((q, i) => q.id && index.set(q.id, i))
  const targets = new Set(qs.flatMap((q) => nexts(q).map((n) => n[1])))
  const seq: number[] = []
  const crumbs: Crumb[][] = qs.map(() => [])
  const visit = (i: number, crumb: Crumb[]) => {
    if (seq.includes(i)) return
    seq.push(i)
    crumbs[i] = crumb
    const p = picked(i)
    for (const [label, id] of nexts(qs[i])) {
      const j = index.get(id)
      if (j !== undefined && p.includes(label)) visit(j, [...crumb, { q: i, label }])
    }
  }
  qs.forEach((q, i) => !(q.id && targets.has(q.id)) && visit(i, []))
  return { seq, crumbs }
}

/** The labels an answer picks (what a branch follows). */
export function pickedOf(a: AnswerEntry | null): string[] {
  if (a?.kind === 'choices') return a.value
  if (a?.kind === 'idea' && !a.value.startsWith('Yes, but: ')) return [a.value]
  return []
}

/** On a question with branches: an answer that leaves the anticipated path. */
export const leavesPath = (a: AnswerEntry | null) => a?.kind === 'free' || a?.kind === 'dontknow' || (a?.kind === 'idea' && a.value.startsWith('Yes, but: '))
