// The path the page runs under: empty on its own pod, /_lan/pod/<key> for the page of a pod
// of the LAN served through this one (one origin for the installed app).
export const base = location.pathname.match(/^\/_lan\/pod\/[^/]+/)?.[0] ?? ''

/** An absolute path of the pod that serves the page. */
export const podPath = (path: string) => base + path
