// Settings > Remote access > Devices: the devices of the account, the pairing requests to
// approve, the invite QR for a phone, and the revocation of a device.
//
// The panel runs on the relay origin and on http://localhost. WebAuthn refuses an IP address
// as a relying-party id, so a passkey cannot be made on http://127.0.0.1: there the panel
// gives a one-click link to localhost with the same port. Passkeys and IndexedDB are per
// origin, so the tailnet origin, localhost and the relay are separate admin devices, each
// enrolled once with the phrase.
//
// The identity module is the same file the bootstrap page imports (served under
// /_relay/identity.js by the relay and by the pod), so the browser and the pod agree on every
// byte a signature covers. The pod only carries the frames of the pairing link: it holds no
// account key and cannot approve a pairing or sign a revocation list.
import { createSignal, For, Show, onCleanup, onMount } from 'solid-js'
import { request } from '../pod/rpc'
import { errorToast, toast } from '../ui/toast'
import { copyText } from '../ui/clipboard'
import { fmtDate, lang, t } from '../i18n'
import qrcode from 'qrcode-generator'

type Cert = { v: number; account: string; key: string; name: string; kind: string; created: number; scope: string; sig: string }
type Device = { account: string; key: string; cert: Cert }
type Pairing = { code: string; account: string; key: string; name: string; kind: string; cert?: Cert; created: number; expires: number; used: boolean }
type Revocations = { account: string; seq: number; epoch: number; revoked: string[]; created: number; sig: string }

// The parts of the module the panel uses. It is imported at runtime, so the app and the
// bootstrap page load the same file.
interface Identity {
  openStore(): Promise<IDBDatabase>
  list(db: IDBDatabase): Promise<any[]>
  unlock(db: IDBDatabase): Promise<{ rec: any; key: CryptoKey; prf: Uint8Array; seed: Uint8Array } | null>
  enroll(db: IDBDatabase, phrase: string, wordlist: string[], name: string, kind: string): Promise<{ rec: any; accountKey: CryptoKey }>
  accountKey(rec: any, prf: Uint8Array): Promise<CryptoKey>
  syncSeed(rec: any, prf: Uint8Array): Promise<Uint8Array | null>
  x25519FromSeed(seed: Uint8Array): Promise<{ jwk: any; key: string }>
  x25519Shared(jwk: any, peerKey: string): Promise<Uint8Array>
  wrapKey(shared: Uint8Array): Promise<Uint8Array>
  epochKey(syncSeed: Uint8Array, epoch: number): Promise<Uint8Array>
  wrapAAD(account: string, device: string, epoch: number): string
  sealWith(key: Uint8Array, aad: string, bytes: Uint8Array): Promise<{ iv: string; data: string }>
  sign(key: CryptoKey, msg: string): Promise<string>
  canonicalWrap(w: Wrap): string
  wrapFor(accountKey: CryptoKey, accountId: string, device: string, peerKey: string, seed: Uint8Array, syncSeed: Uint8Array, epoch: number): Promise<Wrap>
  signCert(accountKey: CryptoKey, accountId: string, deviceId: string, name: string, kind: string): Promise<Cert>
  signRevocations(accountKey: CryptoKey, accountId: string, revoked: string[], seq: number, epoch: number): Promise<Revocations>
  fingerprint(id: string, wordlist: string[]): Promise<string>
  support(): Promise<string[]>
  link(onPush: (m: any) => void): Promise<{ send: (m: any) => void; recv: (type?: string) => Promise<any>; close: () => void }>
  wordlist(): Promise<string[]>
  KindBrowser: string
}

type SyncKey = { account: string; device: string; key: string; sig: string }
type Wrap = { account: string; device: string; epoch: number; key: string; nonce: string; data: string; sig: string }

const IDENTITY_PATH = '/_relay/identity.js'

const deviceName = () => (navigator as any).userAgentData?.platform || navigator.platform || 'browser'

/** The QR of an invite link, as an inline SVG (the phone opens it in its camera app). */
function qrSvg(text: string) {
  const q = qrcode(0, 'M')
  q.addData(text)
  q.make()
  return q.createSvgTag({ cellSize: 4, margin: 2, scalable: true })
}

export function Devices() {
  const [id, setId] = createSignal<Identity | null>(null)
  const [error, setError] = createSignal('')
  const [devices, setDevices] = createSignal<Device[]>([])
  const [pairings, setPairings] = createSignal<Pairing[]>([])
  const [revoked, setRevoked] = createSignal<Revocations | null>(null)
  const [fp, setFp] = createSignal<Record<string, string>>({})
  const [account, setAccount] = createSignal('')
  const [device, setDevice] = createSignal('')
  const [phrase, setPhrase] = createSignal('')
  const [invite, setInvite] = createSignal<Pairing | null>(null)
  const [podId, setPodId] = createSignal('')
  const [keys, setKeys] = createSignal<SyncKey[]>([])
  const [wraps, setWraps] = createSignal<Wrap[]>([])
  let link: { send: (m: any) => void; recv: (type?: string) => Promise<any>; close: () => void } | null = null
  let accountKey: CryptoKey | null = null
  let own: any = null
  let wordlist: string[] = []
  // The seeds of this device, kept for the session: the X25519 key it signs its wraps with, and the
  // sync key of the epoch it derives. A device that does not hold the account key cannot wrap.
  let seed: Uint8Array | null = null
  let syncSeed: Uint8Array | null = null

  // The epoch of the sync key is the epoch of the revocation list the admin signs.
  const epoch = () => revoked()?.epoch ?? 0
  const wrapOf = (key: string) => wraps().find((w) => w.device === key)

  const isLoopback = () => location.hostname === '127.0.0.1' || location.hostname === '[::1]'
  const localhostLink = () => `${location.protocol}//localhost:${location.port || (location.protocol === 'https:' ? 443 : 80)}`

  async function showFp(key: string) {
    if (!wordlist.length || fp()[key]) return
    const read = await id()!.fingerprint(key, wordlist)
    setFp((f) => ({ ...f, [key]: read }))
  }

  async function refresh() {
    if (!link) return
    link.send({ type: 'devices' })
    link.send({ type: 'pairings' })
    link.send({ type: 'revocations' })
    link.send({ type: 'syncKeys' })
    link.send({ type: 'wraps' })
  }

  // The wrap of the key of the epoch for a device: the only way a pod paired without the phrase gets
  // the key. The relay stores the wrap but cannot open it, so it cannot read a blob either.
  async function wrapDevice(k: SyncKey) {
    if (!accountKey || !syncSeed || !seed) return
    const w = await id()!.wrapFor(accountKey, account(), k.device, k.key, seed, syncSeed, epoch())
    link?.send({ type: 'wrap', wrap: w })
    setWraps((ws) => [...ws.filter((x) => x.device !== k.device), w])
    toast(t('Sync key wrapped for {device}.', { device: fp()[k.device] || k.device }), 'ok')
  }

  // Wraps for every device that published a key and has none for the current epoch: a device paired
  // before this panel was open, and every device left after a rotation.
  async function wrapMissing() {
    if (!accountKey || !syncSeed || !seed) return
    for (const k of keys()) {
      if (revoked()?.revoked.includes(k.device)) continue
      const w = wrapOf(k.device)
      if (w && w.epoch === epoch()) continue
      await wrapDevice(k)
    }
  }

  onMount(async () => {
    const m = await import(IDENTITY_PATH) as unknown as Identity
    setId(m)
    if (isLoopback()) return
    const missing = await m.support()
    if (missing.length) {
      setError(t('This browser is missing: {missing}', { missing: missing.join(', ') }))
      return
    }
    wordlist = await m.wordlist()
    const db = await m.openStore()
    const d = await m.unlock(db)
    if (!d) {
      setError(t('This device is not paired on this origin, or its passkey was not unlocked.'))
      return
    }
    accountKey = await m.accountKey(d.rec, d.prf)
    syncSeed = await m.syncSeed(d.rec, d.prf)
    seed = d.seed
    own = d.rec
    setDevice(d.rec.key)
    setAccount(d.rec.account ?? '')
    void showFp(d.rec.key)

    // The pod id of the relay: the invite link must carry it, else the joined device pairs
    // without knowing which pod to open.
    try {
      const st = await request('relay.status')
      setPodId(st?.podId ?? '')
    } catch {
      setPodId(new URL(location.href).hash.match(/[#&]pod=([^&]+)/)?.[1] ?? '')
    }

    if (!(await startLink())) return
    void refresh()
    onCleanup(() => link?.close())
  })

  // The pairing and admin link. The own certificate is posted with the account: the relay
  // lists the devices of an account from the certificates it can verify, so this device
  // appears in the list.
  async function startLink() {
    link = await id()!.link((msg: any) => {
      if (msg.type === 'error') return errorToast(t(msg.message))
      if (msg.type === 'devices') setDevices(msg.devices ?? [])
      else if (msg.type === 'pairings') setPairings(msg.pairings ?? [])
      else if (msg.type === 'revocations') {
        setRevoked(msg.list)
        // The epoch moved: the wraps of the old epoch no longer open, so the remaining devices
        // must be wrapped again for the new one.
        void wrapMissing()
      } else if (msg.type === 'syncKeys') {
        setKeys(msg.syncKeys ?? [])
        void wrapMissing()
      } else if (msg.type === 'syncKey' && msg.syncKey) {
        // A device published its X25519 key: wrap the key of the epoch for it, else it has no key
        // and cannot read or write a blob.
        setKeys((k) => [...k.filter((x) => x.device !== msg.syncKey.device), msg.syncKey])
        void wrapDevice(msg.syncKey)
      } else if (msg.type === 'wraps') setWraps(msg.wraps ?? [])
      else if (msg.type === 'wrap' && msg.wrap) setWraps((w) => [...w.filter((x) => x.device !== msg.wrap.device), msg.wrap])
      else if (msg.type === 'pair' && msg.pairing) setPairings((p) => [...p.filter((x) => x.code !== msg.pairing.code), msg.pairing])
      for (const key of [...devices().map((d) => d.key), ...pairings().map((p) => p.key)]) void showFp(key)
    })
    link.send({ type: 'register', account: account(), name: deviceName(), cert: own?.cert ?? null, lang: lang() })
    const ready = await link.recv()
    if (ready.type === 'error') {
      setError(t(ready.message))
      return false
    }
    return true
  }

  async function enroll() {
    const m = id()!
    const db = await m.openStore()
    const d = await m.enroll(db, phrase(), wordlist, deviceName(), m.KindBrowser)
    accountKey = d.accountKey
    own = d.rec
    setDevice(d.rec.key)
    setAccount(d.rec.account)
    setPhrase('')
    toast(t('This device is paired as an admin.'), 'ok')
    if (!(await startLink())) return
    void refresh()
  }

  async function approve(p: Pairing) {
    const cert = await id()!.signCert(accountKey!, account(), p.key, p.name || deviceName(), p.kind)
    link?.send({ type: 'pairApprove', code: p.code, cert })
    toast(t('Device approved.'), 'ok')
    void refresh()
    // The wrap is made at approval when the device already published its X25519 key; a device that
    // pairs first gets it when it publishes, on its first connection.
    const k = keys().find((x) => x.device === p.key)
    if (k) void wrapDevice(k)
  }

  // The invite link carries the pod the admin is on: the joining device pairs first, then
  // opens that pod. The code is created empty, the device that opens the link posts its key
  // to it, and the fingerprint shown here must match the key read back.
  const inviteLink = (code: string) => `${location.origin}/#pod=${podId()}&join=${code}`

  async function inviteDevice() {
    link?.send({ type: 'pair', key: '', name: deviceName(), kind: id()!.KindBrowser })
    const m = await link?.recv('pair')
    if (m?.type === 'error') return errorToast(t(m.message))
    setInvite(m?.pairing ?? null)
  }

  async function revoke(device: Device) {
    const list = await id()!.signRevocations(
      accountKey!, account(), [...(revoked()?.revoked ?? []), device.key],
      (revoked()?.seq ?? 0) + 1, (revoked()?.epoch ?? 0) + 1,
    )
    link?.send({ type: 'revoke', list })
    toast(t('Device revoked. The sync key epoch moved to {n}.', { n: list.epoch }), 'ok')
    void refresh()
  }

  return (
    <section class="form" data-testid="devices">
      <Show when={error()}>
        <p class="err">{t(error())}</p>
      </Show>
      <Show when={isLoopback()}>
        <p class="muted small">
          {t('A passkey cannot be made on an IP address: open the pod on {link} to unlock the devices of this account.', { link: localhostLink() })}
        </p>
        <div class="field-row">
          <a class="btn" href={localhostLink()}>{t('Open on localhost')}</a>
        </div>
      </Show>
      <Show when={!isLoopback() && !account()}>
        <label class="field">
          <span>{t('Recovery phrase (24 words), never stored')}</span>
          <input class="grow" value={phrase()} onInput={(e) => setPhrase(e.currentTarget.value)} spellcheck="false" autocomplete="off" />
        </label>
        <div class="field-row">
          <button class="btn" onClick={() => void enroll()}>{t('Pair this device as an admin')}</button>
        </div>
        <p class="muted small">{t('Passkeys and stored keys are per origin: the tailnet address, localhost and the relay are separate devices, each enrolled once with the phrase.')}</p>
      </Show>
      <Show when={account()}>
        <dl class="props">
          <dt>{t('This device')}</dt>
          <dd class="mono small">{fp()[device()] || device()}</dd>
        </dl>

        <Show when={pairings().length}>
          <h3>{t('Pairing requests')}</h3>
          <table class="rules">
            <thead>
              <tr>
                <th>{t('Code')}</th>
                <th>{t('Device')}</th>
                <th>{t('Fingerprint')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <For each={pairings()}>
                {(p) => (
                  <tr>
                    <td class="mono">{p.code}</td>
                    <td class="small">{p.name || p.kind}</td>
                    <td class="mono small">{fp()[p.key] || p.key}</td>
                    <td>
                      <Show when={!p.used}>
                        <button class="btn small" onClick={() => void approve(p)}>{t('Approve')}</button>
                      </Show>
                      <Show when={p.used}>
                        <span class="muted small">{t('approved')}</span>
                      </Show>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </Show>

        <h3>{t('Devices of the account')}</h3>
        <table class="rules" data-testid="device-list">
          <thead>
            <tr>
              <th>{t('Name')}</th>
              <th>{t('Kind')}</th>
              <th>{t('Fingerprint')}</th>
              <th>{t('Created')}</th>
              <th>{t('Sync key')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <For each={devices()}>
              {(d) => (
                <tr>
                  <td class="small">{d.cert?.name || '—'}</td>
                  <td class="small">{d.cert?.kind}</td>
                  <td class="mono small">{fp()[d.key] || d.key}</td>
                  <td class="small">{fmtDate(d.cert?.created * 1000)}</td>
                  <td>
                    <Show when={revoked()?.revoked.includes(d.key)}>
                      <span class="muted small">—</span>
                    </Show>
                    <Show when={!revoked()?.revoked.includes(d.key)}>
                      <Show when={wrapOf(d.key)?.epoch === epoch()}>
                        <span class="muted small">{t('wrapped')}</span>
                      </Show>
                      <Show when={!wrapOf(d.key) || wrapOf(d.key)?.epoch !== epoch()}>
                        <Show when={keys().some((k) => k.device === d.key)}>
                          <button class="btn small" onClick={() => void wrapDevice(keys().find((k) => k.device === d.key)!)}>
                            {t('Wrap')}
                          </button>
                        </Show>
                        <Show when={!keys().some((k) => k.device === d.key)}>
                          <span class="muted small">{t('no key published')}</span>
                        </Show>
                      </Show>
                    </Show>
                  </td>
                  <td>
                    <Show when={revoked()?.revoked.includes(d.key)}>
                      <span class="muted small">{t('revoked')}</span>
                    </Show>
                    <Show when={!revoked()?.revoked.includes(d.key) && d.key !== device()}>
                      <button class="btn small" onClick={() => void revoke(d)}>{t('Revoke')}</button>
                    </Show>
                  </td>
                </tr>
              )}
            </For>
          </tbody>
        </table>

        <div class="field-row">
          <button class="btn" onClick={() => void inviteDevice()}>{t('Invite a device')}</button>
        </div>
        <Show when={invite()}>
          <div class="qr">
            <div class="mono">{invite()!.code}</div>
            <p class="mono small">{inviteLink(invite()!.code)}</p>
            <div innerHTML={qrSvg(inviteLink(invite()!.code))} />
            <p class="muted small">
              {t('Fingerprint of this device: {fp}. Compare it with the one the joining device shows.', { fp: fp()[device()] || '' })}
            </p>
            <div class="field-row">
              <button class="btn small" onClick={() => void copyText(inviteLink(invite()!.code))}>{t('Copy the invite link')}</button>
            </div>
          </div>
        </Show>
        <p class="muted small">{t('The relay only carries the pairing link: it cannot approve a device or sign a revocation list, and it never holds the phrase.')}</p>
      </Show>
    </section>
  )
}
