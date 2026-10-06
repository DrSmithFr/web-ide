// ask_user, page side: the answer of a question as the card keeps it, and as a string (the
// English words the pod gives back to the model, which validates the questions and writes
// the text of the answers: pod/internal/agent/ask.go).
import { t } from '../i18n'

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
