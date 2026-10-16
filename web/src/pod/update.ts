// *Update this pod* (#60): the browser signs the request with its own device key — the pod it
// names checks it against the account it pinned and its revocation list, and runs the installer.
// The pod the page talks to only carries the request (relay.md, *Multi-pod*).
import { podPath } from '../app/base'
import { inTunnel } from './transport'
import { request } from './rpc'
import { t } from '../i18n'

// The identity module of this origin (the same file the bootstrap page imports), imported at
// runtime so the browser and the pod agree on every byte the signature covers.
const IDENTITY_PATH = inTunnel() ? '/_relay/identity.js' : podPath('/_relay/identity.js')

export interface Identity {
  openStore(): Promise<IDBDatabase>
  unlock(db: IDBDatabase): Promise<{ rec: any; key: CryptoKey } | null>
  resume(db: IDBDatabase): Promise<{ rec: any; key: CryptoKey } | null>
  sign(key: CryptoKey, msg: string): Promise<string>
  canonicalUpdate(r: { v: number; pod: string; version: string; created: number }): string
  canonicalPower(r: { v: number; pod: string; action: string; created: number }): string
}

/** What the pod that was asked answers, before it restarts. */
export interface UpdateAnswer {
  status: 'running' | 'not_service' | 'refused' | string
  message?: string
  command?: string
}

/** Whether this pod runs a release another pod may be asked to install, and which version. */
export async function updateOffer(): Promise<{ can: boolean; version: string }> {
  const [ws, self] = await Promise.all([request<{ version: string }>('workspace.get'), request<{ canUpdate: boolean }>('pods.self')])
  return { can: !!self?.canUpdate, version: ws?.version ?? '' }
}

/** Signs a request with the device key of this browser: the request, its certificate and the
 * signature of the canonical form canon gives (a function of the identity module). */
export async function signed<R extends object>(req: R, canon: (id: Identity, r: R) => string): Promise<R & { cert: unknown; sig: string }> {
  const id = (await import(/* @vite-ignore */ IDENTITY_PATH)) as unknown as Identity
  // The session of the device (an hour after its last use) signs without asking the passkey.
  const db = await id.openStore()
  const d = (await id.resume(db)) ?? (await id.unlock(db))
  if (!d?.rec?.cert) throw new Error(t('This browser is not paired on this origin, or its passkey was not unlocked.'))
  const sig = await id.sign(d.key, canon(id, req))
  return { ...req, cert: d.rec.cert, sig }
}

/** Signs the request with the device key of this browser and gives it to this pod to carry. */
export async function askUpdate(podKey: string, version: string): Promise<UpdateAnswer> {
  const req = await signed({ v: 1, pod: podKey, version, created: Date.now() }, (id, r) => id.canonicalUpdate(r))
  return request<UpdateAnswer>('pods.update', req)
}
