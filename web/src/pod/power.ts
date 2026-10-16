// Wake, shut down, reboot, restart (relay.md, *Power*): signed by the device key of this browser
// like *Update this pod*. The pod it is for checks it and acts; a wake is sent by an awake pod of
// the network of the machine that sleeps.
import { request } from './rpc'
import { signed, type UpdateAnswer } from './update'

export type PowerAction = 'wake' | 'shutdown' | 'reboot' | 'restart'

/** What the pod answers: running (in a second), sent (the magic packet left), not_service,
 * refused (with the reason, and the command that would allow it). */
export type PowerAnswer = UpdateAnswer

export async function askPower(podKey: string, action: PowerAction): Promise<PowerAnswer> {
  const req = await signed({ v: 1, pod: podKey, action, created: Date.now() }, (id, r) => id.canonicalPower(r))
  return request<PowerAnswer>('pods.power', req)
}
