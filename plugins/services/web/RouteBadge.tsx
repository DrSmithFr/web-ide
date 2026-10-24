// The route a service takes between two pods, shown on both sides: LAN, Relay · direct,
// Relay · TURN (copied by the relay, counted in the quota), idle; the addresses in the tooltip.
import { Show } from 'solid-js'
import { t } from './plugin'

export type Route = { name: '' | 'lan' | 'relay-direct' | 'relay-turn' | 'idle'; local?: string; peer?: string }

export function RouteBadge(props: { route?: Route }) {
  const label = () =>
    ({
      lan: t('LAN'),
      'relay-direct': t('Relay · direct'),
      'relay-turn': t('Relay · TURN'),
      idle: t('idle'),
      '': '',
    })[props.route?.name ?? '']
  const tip = () => [props.route?.local, props.route?.peer].filter(Boolean).join(' ↔ ')
  return (
    <Show when={label()}>
      <span class="badge svc-route" classList={{ [props.route!.name]: true }} title={tip()} data-testid="route" data-route={props.route!.name}>
        {label()}
      </span>
    </Show>
  )
}
