// Wake, shut down, reboot, restart (relay.md, *Power*). Through the relay the browser signs the
// action with its device key, like *Update this pod*. On the origin of the pod (the LAN,
// 127.0.0.1) the window already holds the terminals of the machine: the pod signs it, no passkey.
// The pod it is for checks it and acts; a wake is sent by an awake pod of the network of the
// machine that sleeps.
import { request } from './rpc'
import { signed, type UpdateAnswer } from './update'

export type PowerAction = 'wake' | 'shutdown' | 'reboot' | 'restart'

/** What the pod answers: running (in a second), sent (the magic packet left), not_service,
 * refused (with the reason, and the command that would allow it). */
export type PowerAnswer = UpdateAnswer

export async function askPower(podKey: string, action: PowerAction): Promise<PowerAnswer> {
  const req = { v: 1, pod: podKey, action, created: Date.now() }
  const self = await request<{ local?: boolean }>('pods.self').catch(() => null)
  if (self?.local) return request<PowerAnswer>('pods.power', req)
  return request<PowerAnswer>('pods.power', await signed(req, (id, r) => id.canonicalPower(r)))
}
