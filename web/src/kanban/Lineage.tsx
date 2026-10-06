// Lineage of a ticket (docs/kanban.md): its parent, its children in order (developed one
// after the other in the worktree of the parent), the tickets of other lineages it waits
// for, and what keeps it from starting.
import { For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { fuzzy, pick } from '../ui/overlay'
import { board, blockerNames, getTicket, moveChild, openTicket, statusLabels, updateTicket, type Blocker, type Summary, type Ticket } from './state'
import { Section, type Apply } from './TicketView'
import { t } from '../i18n'

const startable = (s: Summary) => s.status === 'new' || s.status === 'todo'
const titleOf = (id: number) => board.tickets.find((x) => x.id === id)?.title ?? ''

/** State of a dependency for a ticket that may still start: waiting, abandoned or resolved. */
function depState(tk: Ticket, id: number): 'wait' | 'abandoned' | 'ok' {
  const b = tk.blockers?.find((x) => x.id === id && (x.kind === 'depends' || x.kind === 'abandoned'))
  return b ? (b.kind === 'abandoned' ? 'abandoned' : 'wait') : 'ok'
}

/** Picks a ticket of the board (except the given ones). */
async function pickTicket(placeholder: string, keep: (s: Summary) => boolean): Promise<number | null> {
  return pick<number>({
    placeholder,
    provider: (q) =>
      board.tickets
        .filter(keep)
        .map((s) => ({ s, score: q ? fuzzy(q, `#${s.id} ${s.title}`) : 1 }))
        .filter((x) => x.score > 0)
        .sort((a, b) => b.score - a.score || b.s.id - a.s.id)
        .slice(0, 100)
        .map(({ s }) => ({ label: `#${s.id} ${s.title}`, detail: statusLabels[s.status], value: s.id })),
  })
}

export function blockerText(b: Blocker) {
  return t('#{id}: {reason}', { id: b.id, reason: t(blockerNames[b.kind]) })
}

export function LineageSection(props: { tk: Ticket; apply: Apply }) {
  const tk = () => props.tk
  const editable = () => startable(tk())
  // A change of another ticket (a child): this one is read again.
  const reload = () => getTicket(tk().id)
  const setParent = async () => {
    const id = await pickTicket(t('Parent ticket: this one becomes its next step'), (s) => s.id !== tk().id && !s.parent && s.status !== 'done' && s.status !== 'abandoned')
    if (id) await props.apply(updateTicket(tk().id, { parent: id }))
  }
  const addChild = async () => {
    const id = await pickTicket(t('Ticket to add as the next step'), (s) => s.id !== tk().id && !s.parent && startable(s) && !board.tickets.some((c) => c.parent === s.id))
    if (id) await props.apply(updateTicket(id, { parent: tk().id }).then(reload))
  }
  const addDep = async () => {
    const id = await pickTicket(t('Ticket to wait for (merged or done)'), (s) => s.id !== tk().id && !tk().dependsOn.includes(s.id) && s.status !== 'abandoned')
    if (id) await props.apply(updateTicket(tk().id, { dependsOn: [...tk().dependsOn, id] }))
  }
  return (
    <Section title={t('Lineage')}>
      <div class="tk-lineage" data-testid="ticket-lineage">
        <Show when={tk().blockers?.length}>
          <div class="tk-blockers warn small" data-testid="ticket-blockers">
            <Icon name="lock" size={12} />
            <span>
              {t('Cannot start yet:')}{' '}
              <For each={tk().blockers}>
                {(b, i) => (
                  <>
                    {i() ? ', ' : ''}
                    <button class="link" onClick={() => openTicket(b.id)}>
                      {blockerText(b)}
                    </button>
                  </>
                )}
              </For>
            </span>
          </div>
        </Show>

        <Show
          when={tk().parent}
          fallback={
            <Show when={!tk().children.length && editable()}>
              <button class="btn small" onClick={() => void setParent()} data-testid="ticket-set-parent">
                <Icon name="branch" size={12} /> {t('Make it a step of…')}
              </button>
            </Show>
          }
        >
          <div class="tk-row small" data-testid="ticket-parent">
            <Icon name="branch" size={12} />
            <span class="muted">{t('Step of')}</span>
            <button class="link ellipsis" onClick={() => openTicket(tk().parent!)}>
              #{tk().parent} {titleOf(tk().parent!)}
            </button>
            <span class="grow" />
            <Show when={editable()}>
              <button class="icon-btn small" title={t('Take it out of the lineage')} onClick={() => props.apply(updateTicket(tk().id, { parent: 0 }))} data-testid="ticket-unparent">
                <Icon name="close" size={11} />
              </button>
            </Show>
          </div>
        </Show>

        <Show when={!tk().parent}>
          <Show when={tk().children.length}>
            <p class="muted small">{t('Next steps, developed in this worktree in this order; the ticket is merged once they are finished.')}</p>
          </Show>
          <ol class="tk-children" data-testid="ticket-children">
            <For each={tk().children}>
              {(c, i) => (
                <li class="tk-row small" data-testid="ticket-child">
                  <span class={`kb-dot st-${c.status}`} title={statusLabels[c.status]} />
                  <button class="link ellipsis" onClick={() => openTicket(c.id)}>
                    #{c.id} {c.title}
                  </button>
                  <span class="grow" />
                  <Show when={startable(c)}>
                    <button class="icon-btn small" title={t('Move up')} disabled={i() === 0} onClick={() => props.apply(moveChild(c.id, -1).then(reload))}>
                      <Icon name="up" size={11} />
                    </button>
                    <button class="icon-btn small" title={t('Move down')} disabled={i() === tk().children.length - 1} onClick={() => props.apply(moveChild(c.id, 1).then(reload))}>
                      <Icon name="down" size={11} />
                    </button>
                    <button class="icon-btn small" title={t('Take it out of the lineage')} onClick={() => props.apply(updateTicket(c.id, { parent: 0 }).then(reload))}>
                      <Icon name="close" size={11} />
                    </button>
                  </Show>
                </li>
              )}
            </For>
          </ol>
          <Show when={tk().status !== 'done' && tk().status !== 'abandoned'}>
            <button class="btn small" onClick={() => void addChild()} data-testid="ticket-add-child">
              <Icon name="plus" size={12} /> {t('Add a step')}
            </button>
          </Show>
        </Show>

        <div class="tk-deps">
          <span class="muted small">{t('Waits for')}</span>
          <For each={tk().dependsOn} fallback={<span class="muted small">{t('nothing')}</span>}>
            {(id) => {
              const st = () => depState(tk(), id)
              return (
                <span class={`tk-dep ${st()}`} data-testid="ticket-dep" title={st() === 'ok' ? t('Merged or done') : st() === 'abandoned' ? t('Abandoned: it never resolves') : t('Not merged yet')}>
                  <Icon name={st() === 'ok' ? 'check' : st() === 'abandoned' ? 'warning' : 'clock'} size={11} />
                  <button class="link" onClick={() => openTicket(id)}>
                    #{id}
                  </button>
                  <Show when={editable()}>
                    <button class="icon-btn small" title={t('Remove')} onClick={() => props.apply(updateTicket(tk().id, { dependsOn: tk().dependsOn.filter((d) => d !== id) }))}>
                      <Icon name="close" size={10} />
                    </button>
                  </Show>
                </span>
              )
            }}
          </For>
          <Show when={editable()}>
            <button class="icon-btn small" title={t('Wait for another ticket')} onClick={() => void addDep()} data-testid="ticket-add-dep">
              <Icon name="plus" size={12} />
            </button>
          </Show>
        </div>
      </div>
    </Section>
  )
}
