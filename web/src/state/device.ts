// Per-device layout: the fonts and the panel sizes a device chose for itself, kept in the settings
// so a phone keeps its layout on any pod of the account. A device that has no entry of its own uses
// the fallback layout the user saved for new devices.
import type { Settings } from './settings'

export interface DeviceLayout {
  font?: { size?: number; uiSize?: number; lineHeight?: number }
  panels?: { left?: number; right?: number; bottom?: number; split?: number }
}

/** The entry holding the fallback layout, used by the devices that have none of their own. */
export const FALLBACK = 'fallback'

// The device id of this browser: the key of its certificate over the relay. The bootstrap page
// exposes it, and the app runs in an iframe of the same origin. On 127.0.0.1 and localhost a
// passkey cannot be made, so there is no certificate there: the id is a random one kept in
// localStorage, which makes this browser a device of its own.
function readDeviceId(): string {
  const fromParent = (window.parent as any)?.identity?.device
  if (fromParent) return fromParent
  const host = location.hostname
  if (host === 'localhost' || host === '127.0.0.1' || host === '[::1]') {
    let id = localStorage.getItem('webide.device')
    if (!id) {
      id = Math.random().toString(36).slice(2, 10)
      localStorage.setItem('webide.device', id)
    }
    return id
  }
  return ''
}

export const deviceId = readDeviceId()

/** The layout in effect for a device: its own entry, else the one saved for new devices. */
export function layoutFor(s: Settings, device = deviceId): DeviceLayout {
  return { ...(s.devices?.[FALLBACK] ?? {}), ...(s.devices?.[device] ?? {}) }
}
