// Service Worker of the relay bootstrap. It intercepts the fetches of the app (the page,
// its assets, the previews) and answers them from the pod through the tunnel: the
// bootstrap page owns the PeerConnection, so each request is forwarded to it as an
// "http:<n>" channel and streamed back here as a Response. Only the bootstrap page and the
// Service Worker come from the relay; everything else is the user's own pod.
const pending = new Map()
// Which preview each client belongs to: a page opened under /__preview/<id>/ keeps fetching
// its assets there, even with an absolute path.
const owners = new Map()
// Which bootstrap window owns which pod, as the page declared it: a page that picked its pod
// itself (no #pod= in the link) has nothing in its URL to be recognised by.
const ownPods = new Map()

const appFiles = new Set(['/manifest.webmanifest', '/icon.svg', '/icon-192.png', '/icon-512.png'])

self.addEventListener('install', (e) => self.skipWaiting())
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))

async function bootstrap() {
  const all = await self.clients.matchAll({ type: 'window' })
  // A page that picked its pod itself has no "pod=" in the URL of its client (the hash was set
  // after the page loaded): it declared itself the owner of a pod instead.
  return all.find((c) => ownPods.has(c.id)) || all.find((c) => c.url.includes('pod=')) || null
}

self.addEventListener('message', (e) => {
  if (e.data?.kind === 'own' && e.source) ownPods.set(e.source.id, e.data.pod || '')
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)
  // The bootstrap page and the Service Worker itself come from the relay: the tunnel is not
  // open yet when the bootstrap page is loaded, so answering it from the pod would hang.
  // The manifest and the icons of the installable app come from the relay as well.
  if (url.pathname.startsWith('/_relay') || url.pathname === '/sw.js' || url.pathname === '/' || url.pathname === '/index.html' || appFiles.has(url.pathname)) return
  event.respondWith(handle(req, url, event.clientId))
})

async function handle(req, url, clientId) {
  const win = await bootstrap(clientId)
  if (!win) return new Response('The bootstrap page is not open.', { status: 503 })

  let preview = ''
  let path = url.pathname
  if (path.startsWith('/__preview/')) {
    preview = path.split('/')[2]
    path = path.slice(('/__preview/' + preview).length) || '/'
    if (clientId) owners.set(clientId, preview)
  } else if (clientId) {
    preview = owners.get(clientId) ?? ''
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
  win.postMessage({ kind: 'http', id, url: path, preview, method: req.method, headers, body, port: chan.port2 }, [chan.port2])

  const h = await Promise.race([head, new Promise((r) => setTimeout(() => r(null), 60000))])
  if (!h) return new Response('The pod did not answer.', { status: 504 })

  const stream = new ReadableStream({
    start(controller) {
      for (const m of got.slice(1)) controller.enqueue(bytes(m))
      chan.port1.onmessage = (e) => {
        if (!e.data) {
          controller.close()
          pending.delete(id)
          return
        }
        controller.enqueue(bytes(e.data))
      }
    },
    cancel() {
      pending.delete(id)
      chan.port1.close()
    },
  })
  const out = new Headers(h.headers || {})
  out.set('access-control-allow-origin', '*')
  return new Response(stream, { status: h.status || 200, headers: out })
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
