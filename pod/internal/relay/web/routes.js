// The routing decisions of the bootstrap page, kept pure so they can be run outside a browser
// (scripts/routes-test.mjs, node --test): which pod opens the page of another one, and under
// which label its channels go (relay.md, *Multi-pod*).

/** The gateway of a site, as the relay elected it: the preferred one, else the elected one,
 * else any pod of the site. Null when the site has no pod online. */
export function pickGateway(pods, site) {
  const group = pods.filter((p) => p.site === site)
  return group.find((p) => p.gateway && p.preferred) ?? group.find((p) => p.gateway) ?? group[0] ?? null
}

/** The site a window enters, and its gateway: the site used last, else the one with a preferred
 * gateway, else the first. "Preferred gateway" is the pod its user marked as the entry point. */
export function pickSite(pods, last) {
  const sites = new Map()
  for (const p of pods) {
    if (!sites.has(p.site)) sites.set(p.site, [])
    sites.get(p.site).push(p)
  }
  const order = [...sites.keys()].sort()
  const preferred = order.find((s) => (sites.get(s) ?? []).some((p) => p.gateway && p.preferred))
  const site = order.includes(last) ? last : (preferred ?? order[0])
  return { site, pod: pickGateway(pods, site) }
}

/** The pod a message of the app or the Service Worker is about: the base of its page names it.
 * /pod/<key>/ on the relay origin, /_lan/pod/<key>/ on the origin of a gateway. */
export function keyOf(base) {
  const m = /^\/(?:_lan\/)?pod\/([^/]+)/.exec(base ?? '')
  return m ? m[1] : ''
}

/** The gateway to reconnect through when the one this window entered through left: a pod of the
 * site as this window knew it (a site is named after the pods of the local network, so its id
 * changes when the gateway leaves — the pods seen before are what to follow), elected for the site
 * it is on now; else the gateway of the site as it is named now; else any gateway the relay
 * reports, then any pod that is not the one that left. */
export function nextGateway(pods, podsPrev, entrySite, entryKey) {
  const same = new Set(podsPrev.filter((p) => p.site === entrySite).map((p) => p.key))
  const group = pods.filter((p) => same.has(p.key) && p.key !== entryKey)
  return pickGateway(group, group[0]?.site) ?? pickGateway(pods, entrySite) ??
    pods.find((p) => p.gateway && same.has(p.key)) ?? pods.find((p) => p.gateway) ??
    pods.find((p) => p.key !== entryKey) ?? null
}

/** The tunnel that carries the channels of a pod, and the prefix of its labels: the pod's own
 * when the relay reports it online, else the gateway this window entered through, with every
 * label prefixed by @<key>/ (the gateway opens them on its own link to the peer). A pod the relay
 * flags as speaking another multi-pod protocol is never routed to: the two would not understand
 * each other, and the home page offers to update it. */
export function routeOf(pods, key, entryKey) {
  if (!key || key === entryKey) return { via: entryKey, prefix: '' }
  const target = pods.find((p) => p.key === key)
  if (target && !target.mismatch) return { via: key, prefix: '' }
  return { via: entryKey, prefix: '@' + key + '/' }
}
