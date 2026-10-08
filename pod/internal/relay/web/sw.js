// Service Worker of the relay bootstrap. It intercepts the fetches of the app (the page,
// its assets, the previews) and answers them from the pod through the tunnel: the
// bootstrap page owns the PeerConnection, so each request is forwarded to it as an
// "http:<n>" channel and streamed back here as a Response. Only the bootstrap page and the
// Service Worker come from the relay; everything else is the user's own pod.
const pending = new Map()
// Which preview each client belongs to: a page opened under /__preview/<id>/ keeps fetching
// its assets there, even with an absolute path.
const owners = new Map()

self.addEventListener('install', (e) => self.skipWaiting())
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))

async function bootstrap() {
  const all = await self.clients.matchAll({ type: 'window' })
  return all.find((c) => c.url.includes('pod=')) || null
}

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)
  if (url.pathname.startsWith('/_relay') || url.pathname === '/sw.js') return
  event.respondWith(handle(req, url, event.clientId))
})

async function handle(req, url, clientId) {
  const win = await bootstrap()
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
  win.postMessage({ kind: 'http', id, url: path, preview, method: req.method, headers, body, port: chan.port2 }, [chan.port2])

  const head = await once(chan, 60000)
  if (!head) return new Response('The pod did not answer.', { status: 504 })
  const stream = new ReadableStream({
    start(controller) {
      chan.port1.onmessage = (e) => {
        if (!e.data) {
          controller.close()
          pending.delete(id)
          return
        }
        controller.enqueue(e.data instanceof Uint8Array ? e.data : new TextEncoder().encode(String(e.data)))
      }
    },
    cancel() {
      pending.delete(id)
      chan.port1.close()
    },
  })
  const h = new Headers(head.headers || {})
  h.set('access-control-allow-origin', '*')
  return new Response(stream, { status: head.status || 200, headers: h })
}

function once(chan, ms) {
  return new Promise((resolve) => {
    const t = setTimeout(() => resolve(null), ms)
    chan.port1.onmessage = (e) => {
      clearTimeout(t)
      resolve(e.data?.head ?? e.data)
    }
  })
}
