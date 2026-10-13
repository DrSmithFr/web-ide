// The path the page runs under: empty on its own pod, /_lan/pod/<key> for the page of a pod of
// the LAN served through its gateway, /pod/<key> on the relay origin (one origin for the
// installed app, whatever the pod that serves it).
export const base = location.pathname.match(/^\/(_lan\/pod|pod)\/[^/]+/)?.[0] ?? ''

/** An absolute path of the pod that serves the page. */
export const podPath = (path: string) => base + path
