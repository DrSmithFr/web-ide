// One home page for the whole account: the one of the pod the window entered through. A page of
// another pod (its projects open with its own front end, under a prefix) goes back there, never
// to a home page of its own.
import { base } from './base'
import { navigate } from './router'
import { entryBase, inTunnel } from '../pod/transport'

/** The base of the pod whose home page this window shows. On a pod (127.0.0.1 or its LAN port)
 * the pod itself, which serves every other pod under /_lan/pod/; on the relay origin the entry
 * pod of the bootstrap page, which moves on a failover. */
export async function homeBase(): Promise<string> {
  if (base.startsWith('/_lan/pod/')) return ''
  if (!inTunnel() || !base) return base
  // A bootstrap page that does not tell its entry (older than the unified home page) keeps the
  // home page of the pod shown.
  return (await entryBase()) ?? base
}

/** Opens the home page: within the page when this pod is the entry, else a load of the entry's. */
export async function goHome() {
  const to = await homeBase()
  if (to === base) navigate('/')
  else location.assign(to + '/')
}
