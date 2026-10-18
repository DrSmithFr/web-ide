// The level of the account on the relay (#87): what its GitHub sponsorship and the VIP list
// of the relay give. One sober badge, the same next to the GitHub login (Members) and next to the
// TURN quota (Remote access). The words of the badge stay in English in every language.
import { t } from '../i18n'

/** The level as the relay tells it ("" is free); sponsor is what the sponsorship alone gives. */
export interface AccountLevel {
  level?: string
  until?: number
  sponsor?: string
  vip?: boolean
}

const words: Record<string, string> = { backer: 'Backer', supporter: 'Supporter', vip: 'VIP' }

/** The badge: `Backer`, `Supporter`, `VIP`, `Supporter · VIP` for a sponsor on the VIP list too,
 * with `· 12 d` when the level ends. Empty for a free account. */
export function levelBadge(l: AccountLevel | null | undefined, now = Date.now()): string {
  if (!l?.level || !words[l.level]) return ''
  let badge = words[l.level]
  if (l.vip && l.sponsor && l.sponsor !== 'vip' && words[l.sponsor]) badge = `${words[l.sponsor]} · VIP`
  if (l.until) {
    const days = Math.max(1, Math.ceil((l.until * 1000 - now) / 86400000))
    badge += ' · ' + t('{n} d', { n: days })
  }
  return badge
}
