// The routing decisions of the bootstrap page, kept pure so they can be run outside a browser
// (scripts/routes-test.mjs, node --test): which pod opens the page of another one, and under
// which label its channels go (relay.md, *Multi-pod*).

/** The gateway of a cluster, as the relay elected it: the preferred one, else the elected one,
 * else any pod of the cluster. Null when the cluster has no pod online. */
export function pickGateway(pods, cluster) {
  // p.site: "site" on the wire: the cluster (GLOSSARY.md)
  const group = pods.filter((p) => p.site === cluster)
  return group.find((p) => p.gateway && p.preferred) ?? group.find((p) => p.gateway) ?? group[0] ?? null
}

/** The cluster a window enters, and its gateway: the cluster used last, else the one with a preferred
 * gateway, else the first. "Preferred gateway" is the pod its user marked as the entry point. */
export function pickCluster(pods, last) {
  const clusters = new Map()
  for (const p of pods) {
    if (!clusters.has(p.site)) clusters.set(p.site, [])
    clusters.get(p.site).push(p)
  }
  const order = [...clusters.keys()].sort()
  const preferred = order.find((s) => (clusters.get(s) ?? []).some((p) => p.gateway && p.preferred))
  const cluster = order.includes(last) ? last : (preferred ?? order[0])
  return { cluster, pod: pickGateway(pods, cluster) }
}

/** The pod a message of the app or the Service Worker is about: the base of its page names it.
 * /pod/<key>/ on the relay origin, /_lan/pod/<key>/ on the origin of a gateway. */
export function keyOf(base) {
  const m = /^\/(?:_lan\/)?pod\/([^/]+)/.exec(base ?? '')
  return m ? m[1] : ''
}

/** The gateway to reconnect through when the one this window entered through left: a pod of the
 * cluster as this window knew it (a cluster is named after the pods of the local network, so its id
 * changes when the gateway leaves — the pods seen before are what to follow), elected for the cluster
 * it is on now; else the gateway of the cluster as it is named now; else any gateway the relay
 * reports, then any pod that is not the one that left. */
export function nextGateway(pods, podsPrev, entryCluster, entryKey) {
  const same = new Set(podsPrev.filter((p) => p.site === entryCluster).map((p) => p.key))
  // p.site: "site" on the wire: the cluster (GLOSSARY.md)
  const group = pods.filter((p) => same.has(p.key) && p.key !== entryKey)
  return pickGateway(group, group[0]?.site) ?? pickGateway(pods, entryCluster) ??
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
