// Settings > Remote access > Members: the members of the account (devices and pods), the pairing
// requests to approve, the invite QR for a phone, and the revocation of a member. The messages of
// the identity link keep their old names (`devices`, `device`: wire format, see GLOSSARY.md).
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
import { podPath } from '../app/base'
import { inTunnel } from '../pod/transport'
import { createSignal, For, Show, onCleanup, onMount } from 'solid-js'
import { levelBadge, type AccountLevel } from './level'
import { TeamSection, type TeamInfo } from './Team'
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
  enroll(db: IDBDatabase, phrase: string, wordlist: string[], name: string, kind: string): Promise<{ rec: any; key: CryptoKey; accountKey: CryptoKey }>
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
  prove(l: { send: (m: any) => void; recv: (type?: string) => Promise<any> }, key: CryptoKey, origin: string): Promise<any>
  wordlist(): Promise<string[]>
  lock(): Promise<void>
  KindBrowser: string
}

type SyncKey = { account: string; device: string; key: string; sig: string }
type Wrap = { account: string; device: string; epoch: number; key: string; nonce: string; data: string; sig: string }

// The identity module comes from whoever serves this origin: the relay on the relay origin (its
// own path, whatever pod the page belongs to), the pod on the origin of a pod.
const IDENTITY_PATH = inTunnel() ? '/_relay/identity.js' : podPath('/_relay/identity.js')

const deviceName = () => (navigator as any).userAgentData?.platform || navigator.platform || 'browser'

/** The QR of an invite link, as an inline SVG (the phone opens it in its camera app). */
function qrSvg(text: string) {
  const q = qrcode(0, 'M')
  q.addData(text)
  q.make()
  return q.createSvgTag({ cellSize: 4, margin: 2, scalable: true })
}

export function Members() {
  const [id, setId] = createSignal<Identity | null>(null)
  const [error, setError] = createSignal('')
  const [members, setMembers] = createSignal<Device[]>([])
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
  // The GitHub link of the account (milestone 5): the login it is linked to, and the level of the
  // account (#87: its sponsorship and the VIP list of the relay), which sets the TURN quota.
  const [gh, setGh] = createSignal<({ login: string } & AccountLevel) | null>(null)
  // The teams the account is the admin of (#88), and whether an organization's team may be its.
  const [teams, setTeams] = createSignal<{ teams: TeamInfo[]; mayOwn: boolean }>({ teams: [], mayOwn: false })
  let link: { send: (m: any) => void; recv: (type?: string) => Promise<any>; close: () => void } | null = null
  let accountKey: CryptoKey | null = null
  let own: any = null
  // The signing key of this device, and the host the relay sees the link on: the link proves
  // possession with them, which the relay asks before an account action (the GitHub link).
  let deviceKey: CryptoKey | null = null
  let relayHost = ''
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
    if (!key || !wordlist.length || fp()[key]) return
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
    link.send({ type: 'github' })
  }

  // The GitHub link of the account: the relay hands out a URL the browser opens on GitHub (no
  // scope, the relay only asks who the visitor is), GitHub sends it back to the relay which
  // stores the id of that person, and the window closes itself. The relay never sees a token.
  function linkGitHub() {
    link?.send({ type: 'githubStart' })
  }
  // The team of an organization: GitHub again, asking read:org this time (the window says what
  // the relay proved, the panel reads the teams when it closes).
  function manageTeam() {
    link?.send({ type: 'githubStart', purpose: 'team' })
  }
  function openGitHubLink(url: string) {
    const win = window.open(url, 'webide-github', 'width=520,height=620')
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== location.origin || (e.data && e.data.type !== 'webide.github')) return
      window.removeEventListener('message', onMessage)
      link?.send({ type: 'github' })
    }
    window.addEventListener('message', onMessage)
    // On a pod origin the window (on the relay origin) cannot post to this page: the panel reads
    // the link again when the window closes.
    const watch = win && setInterval(() => {
      if (!win.closed) return
      clearInterval(watch!)
      link?.send({ type: 'github' })
    }, 500)
    if (!win) {
      window.removeEventListener('message', onMessage)
      errorToast(t('The browser blocked the GitHub window: allow popups for this site.'))
    }
  }
  function unlinkGitHub() {
    link?.send({ type: 'githubUnlink' })
    setGh(null)
  }

  // The wrap of the key of the epoch for a device: the only way a pod paired without the phrase gets
  // the key. The relay stores the wrap but cannot open it, so it cannot read a blob either.
  async function wrapDevice(k: SyncKey) {
    if (!accountKey || !syncSeed || !seed) return
    const w = await id()!.wrapFor(accountKey, account(), k.device, k.key, seed, syncSeed, epoch())
    link?.send({ type: 'wrap', wrap: w })
    setWraps((ws) => [...ws.filter((x) => x.device !== k.device), w])
    toast(t('Sync key wrapped for {member}.', { member: fp()[k.device] || k.device }), 'ok')
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
    deviceKey = d.key
    setDevice(d.rec.key)
    setAccount(d.rec.account ?? '')
    void showFp(d.rec.key)

    // The pod id of the relay: the invite link must carry it, else the joined device pairs
    // without knowing which pod to open.
    try {
      const st = await request('relay.status')
      setPodId(st?.podId ?? '')
      // On the relay origin the link goes straight to the relay; on a pod origin the pod
      // carries it to its relay, which then sees its own host, not the page's.
      relayHost = inTunnel() ? location.host : st?.url ? new URL(st.url).host : ''
    } catch {
      setPodId(new URL(location.href).hash.match(/[#&]pod=([^&]+)/)?.[1] ?? '')
      if (inTunnel()) relayHost = location.host
    }

    if (!(await startLink())) return
    void refresh()
    onCleanup(() => link?.close())
  })

  // The pairing and admin link. The own certificate is posted with the account: the relay
  // lists the members of an account from the certificates it can verify, so this device
  // appears in the list.
  async function startLink() {
    link = await id()!.link((msg: any) => {
      if (msg.type === 'error') return errorToast(t(msg.message))
      if (msg.type === 'devices') setMembers(msg.devices ?? [])
      else if (msg.type === 'pairings') setPairings(msg.pairings ?? [])
      else if (msg.type === 'revocations') {
        setRevoked(msg.list)
        // The epoch moved: the wraps of the old epoch no longer open, so the remaining members
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
      else if (msg.type === 'github') {
        setGh({ login: msg.login ?? '', level: msg.level, until: msg.until, sponsor: msg.sponsor, vip: msg.vip, team: msg.team })
        if (msg.login) link?.send({ type: 'team' })
        else setTeams({ teams: [], mayOwn: false })
      } else if (msg.type === 'team') setTeams({ teams: msg.teams ?? [], mayOwn: !!msg.mayOwn })
      else if (msg.type === 'githubStart' && msg.loginUrl) openGitHubLink(msg.loginUrl)
      for (const key of [...members().map((d) => d.key), ...pairings().map((p) => p.key)]) void showFp(key)
    })
    const prove = !!(deviceKey && own?.cert && relayHost)
    link.send({ type: 'register', account: account(), name: deviceName(), cert: own?.cert ?? null, lang: lang(), prove })
    if (prove) {
      const err = await id()!.prove(link, deviceKey!, relayHost)
      if (err) {
        setError(t(err.message ?? err.type))
        return false
      }
    }
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
    deviceKey = d.key
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
    toast(t('Member approved.'), 'ok')
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

  // The servers whose API key the vault shares with the other pods. A revocation moves the epoch:
  // the key of a device that can still read the vault is no longer private, so the panel lists the
  // servers whose key must be renewed.
  const [keyServers, setKeyServers] = createSignal<{ id: string; name: string; hasKey: boolean }[]>([])

  async function loadKeys() {
    try {
      const c = await request('llm.config')
      setKeyServers(((c?.servers ?? []) as any[]).filter((s) => s.hasKey))
    } catch {
      setKeyServers([])
    }
  }

  async function revoke(device: Device) {
    const list = await id()!.signRevocations(
      accountKey!, account(), [...(revoked()?.revoked ?? []), device.key],
      (revoked()?.seq ?? 0) + 1, (revoked()?.epoch ?? 0) + 1,
    )
    link?.send({ type: 'revoke', list })
    await loadKeys()
    const names = keyServers().map((s) => s.name || s.id)
    toast(
      names.length
        ? t('Member revoked. The sync key epoch moved to {n}. Renew the API keys shared with the other pods: {servers}.', { n: list.epoch, servers: names.join(', ') })
        : t('Member revoked. The sync key epoch moved to {n}.', { n: list.epoch }),
      'ok',
    )
    void refresh()
  }

  // The device stays unlocked an hour after its last use (identity.js, keepUnlocked): Lock ends
  // that now, and the next page of this origin asks the passkey.
  async function lockDevice() {
    await id()?.lock()
    toast(t('This device is locked: the next page asks its passkey.'), 'ok')
  }

  return (
    <section class="form" data-testid="members">
      <Show when={error()}>
        <p class="err">{t(error())}</p>
      </Show>
      <Show when={isLoopback()}>
        <p class="muted small">
          {t('A passkey cannot be made on an IP address: open the pod on {link} to manage the members of this account.', { link: localhostLink() })}
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
                <th>{t('Member')}</th>
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
                    {/* An invite no device opened yet has no key: nothing to compare nor approve. */}
                    <td class="mono small">{p.key ? fp()[p.key] || p.key : <span class="muted">{t('waiting for the device')}</span>}</td>
                    <td>
                      <Show when={!p.used && p.key}>
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

        <h3>{t('Members of the account')}</h3>
        <table class="rules" data-testid="member-list">
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
            <For each={members()}>
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

        <Show when={revoked()?.revoked.length && keyServers().length}>
          <div class="field-row" data-testid="renew-keys">
            <p class="muted small">
              {t('A member was revoked: the API keys the vault shares with the other pods must be renewed — {servers}.', { servers: keyServers().map((s) => s.name || s.id).join(', ') })}
            </p>
          </div>
        </Show>

        <dl class="props" data-testid="github-link">
          <dt>{t('GitHub')}</dt>
          <dd>
            <Show when={gh()?.login}>{'@' + gh()!.login}</Show>
            <Show when={!gh()?.login}>{t('not linked')}</Show>
            <Show when={levelBadge(gh())}>
              {' '}<span class="badge" data-testid="level-badge">{levelBadge(gh())}</span>
            </Show>
          </dd>
        </dl>
        <div class="field-row">
          <Show when={!gh()?.login}>
            <button class="btn small" onClick={() => linkGitHub()}>{t('Link a GitHub account')}</button>
          </Show>
          <Show when={gh()?.login}>
            <button class="btn small" onClick={() => unlinkGitHub()}>{t('Unlink GitHub')}</button>
          </Show>
        </div>
        <Show when={gh()?.login}>
          <TeamSection teams={teams().teams} mayOwn={teams().mayOwn} send={(m) => link?.send(m)} manage={manageTeam} />
        </Show>
        <p class="muted small">
          {t('Sponsoring the project on GitHub sets the monthly TURN quota of the hosted relay: a Backer ($2 a month) has it raised, a Supporter ($5) and a VIP ($10) have none. The relay keeps the GitHub id it was shown, never a token, and no e-mail is asked.')}
        </p>

        <div class="field-row">
          <button class="btn" onClick={() => void inviteDevice()}>{t('Invite a device')}</button>
          <button class="btn" data-testid="lock-device" onClick={() => void lockDevice()}>{t('Lock this device now')}</button>
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
        <p class="muted small">{t('The relay only carries the pairing link: it cannot approve a member or sign a revocation list, and it never holds the phrase.')}</p>
      </Show>
    </section>
  )
}
