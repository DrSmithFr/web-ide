// The teams of the account (#88), under its GitHub link in Members: a sponsorship of $50 a month
// or more gives seats, $5 each, and the admin of the team adds its members by GitHub login or
// account id; the members within the seats are VIPs of the relay. The admin of an
// organization's team proves it once on GitHub (read:org, asked for this alone).
import { createSignal, For, Show } from 'solid-js'
import { t } from '../i18n'

export interface TeamMember {
  kind: 'github' | 'account'
  id: string
  login?: string
}

export interface TeamInfo {
  owner: string
  name: string
  org?: boolean
  seats: number
  members: TeamMember[]
}

const short = (m: TeamMember) => (m.login ? '@' + m.login : m.id.slice(0, 12) + '…')

export function TeamSection(props: {
  teams: TeamInfo[]
  mayOwn: boolean
  send: (m: any) => void
  manage: () => void
}) {
  const [adding, setAdding] = createSignal<Record<string, string>>({})
  const add = (team: TeamInfo) => {
    const who = (adding()[team.owner] ?? '').trim()
    if (!who) return
    props.send({ type: 'teamAdd', owner: team.owner, member: who })
    setAdding({ ...adding(), [team.owner]: '' })
  }
  return (
    <div data-testid="teams">
      <For each={props.teams}>
        {(team) => (
          <div class="team" data-testid="team">
            <h4>
              {t('Team {name}', { name: team.name })}{' · '}
              <span data-testid="team-seats">{t('{used}/{seats} seats', { used: Math.min(team.members.length, team.seats), seats: team.seats })}</span>
            </h4>
            <Show when={team.members.length > team.seats}>
              <p class="muted small">{t('The sponsorship gives fewer seats than members: the members added last are not VIPs until some are removed.')}</p>
            </Show>
            <table class="rules">
              <tbody>
                <For each={team.members}>
                  {(m, i) => (
                    <tr data-testid="team-member">
                      <td class="mono" title={m.id}>{short(m)}</td>
                      <td class="muted small">{i() >= team.seats ? t('beyond the seats') : ''}</td>
                      <td>
                        <button class="btn small" onClick={() => props.send({ type: 'teamRemove', owner: team.owner, kind: m.kind, member: m.id })}>
                          {t('Remove')}
                        </button>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
            <div class="field-row">
              <input class="mono grow" placeholder={t('GitHub login or account id')} data-testid="team-add"
                value={adding()[team.owner] ?? ''}
                onInput={(e) => setAdding({ ...adding(), [team.owner]: e.currentTarget.value })}
                onKeyDown={(e) => { if (e.key === 'Enter') add(team) }} />
              <button class="btn small" onClick={() => add(team)}>{t('Add a member')}</button>
            </div>
          </div>
        )}
      </For>
      <Show when={props.mayOwn}>
        <div class="field-row">
          <button class="btn small" onClick={() => props.manage()}>{t('Manage the team of an organization')}</button>
        </div>
        <p class="muted small">
          {t('GitHub asks to read your organizations (read:org): the relay checks once that you are an admin of the organization that sponsors the project, then forgets the token.')}
        </p>
      </Show>
    </div>
  )
}
