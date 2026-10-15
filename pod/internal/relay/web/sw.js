// Service Worker of the relay bootstrap. It intercepts the fetches of the app (the page,
// its assets, the previews) and answers them from the pod through the tunnel: the
// bootstrap page owns the PeerConnection, so each request is forwarded to it as an
// "http:<n>" channel and streamed back here as a Response. Only the bootstrap page and the
// Service Worker come from the relay; everything else is the user's own pod.
const pending = new Map()
// Which preview each client belongs to: a page opened under /__preview/<id>/ keeps fetching
// its assets there, even with an absolute path.
const owners = new Map()
// Which bootstrap window owns which pod, as the page declared it: {pod, key, keys, version}. A page that
// picked its pod itself (no #pod= in the link) has nothing in its URL to be recognised by, and a
// page of a pod reached through a gateway is served by the window that opened that gateway.
const ownPods = new Map()
// The assets a pod says are immutable are kept here: a reload of the page answers them from this
// origin instead of carrying a megabyte of JavaScript through the DataChannel (#71). One cache per
// pod and per version of that pod, so two pods of the account never share an asset and an updated
// pod leaves its old bundle behind. The page itself, its /api calls and the previews always go
// through the tunnel: a cached asset never stands for a live connection.
const ASSETS = 'webide-assets:' // then <id>:<version>, ':' appears in neither a pod id nor a key
const CACHE_MAX = 8 * 1024 * 1024 // a phone keeps the app, not the 26 MB speech runtime
const MAX_AGE = 30 * 24 * 3600 * 1000
// The Cache API stores no date of its own: each cache carries a marker of when it was made.
const CREATED = '/_relay/assets-created'
const versions = new Map() // what each pod was last declared as, to drop its older cache

const appFiles = new Set(['/manifest.webmanifest', '/icon.svg', '/icon-192.png', '/icon-512.png'])

// The shell of the relay: the bootstrap page, its scripts and the installable app. It is kept here,
// so a reload pays the connection only, not the page (#73). The relay writes into SHELL a hash of
// these files: a deploy that changes one of them changes this script, so the browser installs a
// new Service Worker, which fetches the new shell while the old one keeps answering.
const SHELL = '__SHELL__'
const SHELL_CACHE = 'webide-shell:'
const shellFiles = ['/', '/_relay/bootstrap.js', '/_relay/bootstrap.css', '/_relay/routes.js', '/_relay/identity.js', '/_relay/bip39.txt', ...appFiles]
const inShell = new Set(shellFiles)

const cacheName = (id, version) => ASSETS + id + ':' + (version ?? '')

// The first Service Worker of this browser takes over at once. A newer one waits while a page runs
// on the old shell: the page offers to reload (bootstrap.js), and only then is it told to skip —
// a reload cuts the tunnels of the page, the user chooses when.
self.addEventListener('install', (e) =>
  e.waitUntil(
    (async () => {
      const c = await caches.open(SHELL_CACHE + SHELL)
      const fresh = (p) => new Request(p, { cache: 'reload' })
      // The page and its scripts or nothing: a half shell must not replace a whole one. The
      // installable app is kept when the relay has it (a relay without a front end has not).
      await c.addAll(shellFiles.filter((p) => !appFiles.has(p)).map(fresh))
      await Promise.all([...appFiles].map((p) => c.add(fresh(p)).catch(() => {})))
      if (!self.registration.active) await self.skipWaiting()
    })(),
  ),
)
self.addEventListener('activate', (e) => e.waitUntil(Promise.all([self.clients.claim(), dropOldCaches()])))

// The shell, from the cache of this Service Worker; the network when it is not there (a cache
// the browser evicted).
async function shell(req, path) {
  try {
    const hit = await (await caches.open(SHELL_CACHE + SHELL)).match(path)
    if (hit) return hit
  } catch {}
  return fetch(req)
}

// A pod that never came back must not leave its assets on the disk of a phone.
async function dropOldCaches() {
  for (const name of await caches.keys()) {
    if (name.startsWith(SHELL_CACHE) && name !== SHELL_CACHE + SHELL) await caches.delete(name)
    if (!name.startsWith(ASSETS)) continue
    try {
      const r = await (await caches.open(name)).match(CREATED)
      const at = r ? Number(await r.text()) : 0
      if (!at || Date.now() - at > MAX_AGE) await caches.delete(name)
    } catch {}
  }
}

// The pod declared a new version: its old cache is worth nothing now (the names of the assets are
// hashed by the build, so an old one is never asked again — it only takes place).
async function dropOldVersions(id, version) {
  const keep = cacheName(id, version)
  for (const name of await caches.keys()) {
    if (name.startsWith(ASSETS + id + ':') && name !== keep) await caches.delete(name)
  }
}

async function bootstrap(key) {
  const all = await self.clients.matchAll({ type: 'window' })
  // The window that shows that pod wins; else one that declared it can reach it (its page moved
  // to another pod, or the pod rides the gateway this window opened); else any bootstrap window.
  const owned = all.filter((c) => ownPods.has(c.id))
  return (
    owned.find((c) => ownPods.get(c.id).key === key) ||
    owned.find((c) => (ownPods.get(c.id).keys ?? []).includes(key)) ||
    owned[0] ||
    all.find((c) => c.url.includes('pod=')) ||
    null
  )
}

self.addEventListener('message', (e) => {
  if (e.data?.kind === 'skip') void self.skipWaiting()
  if (e.data?.kind === 'own' && e.source) {
    // The build of the pod's front end names its cache when the pod told it (#73): a development
    // rebuild keeps its version, never its build. Else the version, as before.
    const build = e.data.build ?? ''
    const version = build || (e.data.version ?? '')
    ownPods.set(e.source.id, { pod: e.data.pod || '', key: e.data.key || '', keys: e.data.keys ?? [], version, build })
    const id = e.data.key || e.data.pod || e.source.id
    const prev = versions.get(id)
    versions.set(id, version)
    if (prev !== undefined && prev !== version) void dropOldVersions(id, version)
  }
})

// Which pod the assets of a window belong to: the key in the URL of the request when there is one
// (/pod/<key>/ of the relay origin), else the pod the window declared, else the pod in its link
// (the token mode, where the page has no prefix to be recognised by).
function owner(win, key) {
  const own = ownPods.get(win.id) ?? {}
  if (key || own.key) return { id: key || own.key, version: own.version ?? '', build: own.build ?? '' }
  const m = /[#&]pod=([^&]+)/.exec(win.url ?? '')
  const pod = own.pod || (m ? decodeURIComponent(m[1]) : '')
  return { id: pod || win.id, version: own.version ?? '', build: own.build ?? '' }
}

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)
  // The bootstrap page and the Service Worker itself come from the relay: the tunnel is not
  // open yet when the bootstrap page is loaded, so answering it from the pod would hang.
  // The manifest and the icons of the installable app come from the relay as well. The shell is
  // answered from the cache of this Service Worker (#73), the rest of /_relay is the network.
  const path = url.pathname === '/index.html' ? '/' : url.pathname
  if (url.origin === self.location.origin && req.method === 'GET' && inShell.has(path)) return event.respondWith(shell(req, path))
  if (url.pathname.startsWith('/_relay') || url.pathname === '/sw.js' || url.pathname === '/') return
  event.respondWith(handle(req, url, event.clientId))
})

async function handle(req, url, clientId) {
  // A page of another pod lives under /pod/<key>/ of this origin: the same origin for every pod
  // of the account, so the installed app never shows the bar of an outside site.
  let key = ''
  let path = url.pathname
  if (path.startsWith('/pod/')) {
    key = path.split('/')[2] ?? ''
    path = path.slice(('/pod/' + key).length) || '/'
  }
  const win = await bootstrap(key)
  if (!win) return new Response('The bootstrap page is not open.', { status: 503 })

  let preview = ''
  if (path.startsWith('/__preview/')) {
    preview = path.split('/')[2]
    path = path.slice(('/__preview/' + preview).length) || '/'
    if (clientId) owners.set(clientId, preview)
  } else if (clientId) {
    preview = owners.get(clientId) ?? ''
  }

  // An asset of this pod already carried through the tunnel once: answer it from here, without
  // waking the tunnel nor the pod. Only a plain GET of a pod page, and only the cache of the pod
  // that owns this window — two pods of the account never share an entry. The page of the pod is
  // there too, under the URL it was answered for, once the pod named the build it belongs to.
  const own = owner(win, key)
  if (own.id && !preview && req.method === 'GET' && !req.headers.has('range')) {
    try {
      const hit = await (await caches.open(cacheName(own.id, own.version))).match(req)
      if (hit) return hit
    } catch {}
  }

  const id = 'http:' + (pending.size + 1)
  const chan = new MessageChannel()
  const body = req.method === 'GET' || req.method === 'HEAD' ? null : await req.text()
  const headers = {}
  req.headers.forEach((v, k) => (headers[k] = v))
  pending.set(id, chan)

  // The answer of the pod: the head as JSON, then the body as raw chunks, closed by an
  // empty one. The handler must be installed before the first message can arrive, so the
  // head and the chunks already received are buffered here.
  const got = []
  const head = new Promise((resolve) => {
    chan.port1.onmessage = (e) => {
      if (!e.data) {
        resolve(null)
        return
      }
      got.push(e.data)
      if (got.length === 1) resolve(e.data.head)
    }
  })
  win.postMessage(
    { kind: 'http', id, key, base: key ? '/pod/' + key : '', url: path, preview, method: req.method, headers, body, port: chan.port2 },
    [chan.port2],
  )

  const h = await Promise.race([head, new Promise((r) => setTimeout(() => r(null), 60000))])
  if (!h) return new Response('The pod did not answer.', { status: 504 })

  const out = new Headers(h.headers || {})
  out.set('access-control-allow-origin', '*')

  // Keep a copy of what the pod says is immutable, for the next reload of the page, and of its
  // page when it names the build this window declared: the same build serves the same page for
  // the same URL. The copy is dropped as soon as it goes over the cap: a big file is streamed,
  // never stored.
  const page = own.build && header(h.headers, 'x-webide-build') === own.build
  let keep = own.id && !preview && req.method === 'GET' && h.status === 200 && (immutable(h.headers) || page) ? [] : null
  let kept = 0
  const collect = (b) => {
    if (!keep) return
    if (kept + b.length > CACHE_MAX) {
      keep = null
      return
    }
    keep.push(b)
    kept += b.length
  }
  const store = () => {
    const parts = keep
    keep = null
    if (!parts) return
    void caches
      .open(cacheName(own.id, own.version))
      .then(async (c) => {
        if (!(await c.match(CREATED))) await c.put(CREATED, new Response(String(Date.now())))
        await c.put(req.url, new Response(join(parts), { status: h.status, headers: out }))
      })
      .catch(() => {})
  }

  const stream = new ReadableStream({
    start(controller) {
      const push = (m) => {
        const b = bytes(m)
        controller.enqueue(b)
        collect(b)
      }
      for (const m of got.slice(1)) push(m)
      chan.port1.onmessage = (e) => {
        if (!e.data) {
          controller.close()
          pending.delete(id)
          store()
          return
        }
        push(e.data)
      }
    },
    cancel() {
      pending.delete(id)
      chan.port1.close()
    },
  })
  return new Response(stream, { status: h.status || 200, headers: out })
}

// The pod names its headers in their canonical form (Go), so cache-control is looked for
// whatever the case: only an asset the pod says never changes is kept.
function immutable(headers) {
  return /immutable/i.test(header(headers, 'cache-control'))
}

function header(headers, name) {
  for (const k of Object.keys(headers ?? {})) if (k.toLowerCase() === name) return String(headers[k])
  return ''
}

function join(parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let at = 0
  for (const p of parts) {
    out.set(p, at)
    at += p.length
  }
  return out
}

function bytes(d) {
  return d instanceof Uint8Array ? d : new TextEncoder().encode(String(d))
}

// Push notifications: the pod encrypts the payload for this browser (RFC 8291), the relay only
// forwards the ciphertext, and the browser hands the decrypted JSON here: {title, body, url,
// tag}. The url is a bootstrap link (/#pod=<id>&open=<path of the app>).
self.addEventListener('push', (e) => {
  let d = {}
  try {
    d = e.data ? e.data.json() : {}
  } catch {}
  e.waitUntil(self.registration.showNotification(d.title || 'Web IDE', {
    body: d.body || '', tag: d.tag || undefined, icon: '/icon-192.png', data: { url: d.url || '/' },
  }))
})

// A click opens the conversation or the ticket: in the bootstrap window of that pod when one is
// open (it moves its app there), else in a new window.
self.addEventListener('notificationclick', (e) => {
  e.notification.close()
  const done = openFrom(e.notification.data?.url || '/')
  try {
    e.waitUntil(done)
  } catch {} // a click dispatched by the tests has no waitUntil
})

async function openFrom(link) {
  const u = new URL(link, self.location.origin)
  const params = new URLSearchParams(u.hash.slice(1))
  const pod = params.get('pod')
  const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
  const win = pod && all.find((c) => new URLSearchParams(new URL(c.url).hash.slice(1)).get('pod') === pod)
  if (win) {
    win.postMessage({ kind: 'open', url: params.get('open') || '/app' })
    try {
      await win.focus()
    } catch {} // focus needs the click of the user
    return
  }
  await self.clients.openWindow(u.href)
}
