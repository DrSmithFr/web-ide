// Routing of the sessions of a ticket by its complexity (docs/kanban.md): each level has an
// effort of the integrated assistant and a model of Claude Code. The click of a session
// button runs the level recommended by the complexity; its arrow offers all of them.
import type { Effort } from '../llm/state'
import type { Complexity } from './state'
import { t } from '../i18n'

export type ClaudeModel = 'opus' | 'sonnet' | 'haiku'

export interface Tier {
  complexity: Complexity
  /** Effort of the integrated assistant: auto is the dynamic one. */
  effort: 'auto' | Effort
  claude: ClaudeModel
}

/** From the hardest to the simplest, as the menus list them. */
export const tiers: Tier[] = [
  { complexity: 'high', effort: 'xhigh', claude: 'opus' },
  { complexity: 'medium', effort: 'auto', claude: 'sonnet' },
  { complexity: 'low', effort: 'low', claude: 'haiku' },
]

/** The level of a complexity; not estimated: medium (dynamic effort). */
export const tierOf = (c?: Complexity | '') => tiers.find((x) => x.complexity === c) ?? tiers[1]

/** The hardest of some complexities (those not estimated left aside). */
export function hardest(list: (Complexity | '' | undefined)[]): Complexity | undefined {
  return tiers.find((x) => list.includes(x.complexity))?.complexity
}

export const aiLabel = (x: Tier) => (x.effort === 'xhigh' ? t('With the integrated AI (Max)') : x.effort === 'low' ? t('With the integrated AI (Low)') : t('With the integrated AI'))
export const claudeLabel = (x: Tier) => ({ opus: t('With Claude Opus'), sonnet: t('With Claude Sonnet'), haiku: t('With Claude Haiku') })[x.claude]
/** What the button adds to its label for the level it runs: Max, Low, or the Claude model. */
export const aiSuffix = (x: Tier) => (x.effort === 'xhigh' ? t('Max') : x.effort === 'low' ? t('Low') : '')
export const claudeSuffix = (x: Tier) => ({ opus: 'Opus', sonnet: 'Sonnet', haiku: 'Haiku' })[x.claude]
