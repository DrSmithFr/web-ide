// Service Worker for the relay bootstrap. It intercepts the app's HTTP requests and
// forwards them to the pod through a WebRTC DataChannel, streamed back as a Response.
// The bootstrap page (bootstrap.js) holds the PeerConnection and relays each request
// as an "http:<n>" channel; this file turns those into streaming fetches and answers
// them here, so the iframe app loads as if it were served locally.
const pending = new Map();
let seq = 0;

self.addEventListener("install", (e) => { self.skipWaiting(); });
self.addEventListener("activate", (e) => {
  e.waitUntil(self.clients.claim());
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.mode !== "navigate" && req.method !== "GET" && req.method !== "HEAD" && req.method !== "POST") return;
  const url = new URL(req.url);
  // Leave the relay's own static paths to the relay itself.
  if (url.pathname.startsWith("/_relay")) return;
  event.respondTo(handle(req, url));
});

async function handle(req, url) {
  const client = await self.clients.get(await self.registration.active ? await self.clients.matchAll() : []);
  const target = (client && client.id) || null;
  const id = "http:" + (seq++);
  const controller = new AbortController();
  pending.set(id, { controller });
  const res = await new Promise((resolve, reject) => {
    const t = setTimeout(() => { pending.delete(id); reject(new Error("timeout")); }, 60000);
    const onMsg = (e) => {
      if (e.data && e.data.type === "http" && e.data.id === id) {
        clearTimeout(t);
        resolve(e.data);
      }
    };
    // Message the bootstrap page (which owns the PeerConnection) to open the channel.
    const win = globalThis.__bootstrapWin;
    if (win) {
      win.postMessage({ kind: "http", id, target, url: url.toString(), method: req.method, body: null });
      self.addEventListener("message", onMsg, { once: true });
    } else {
      clearTimeout(t);
      reject(new Error("no bootstrap page"));
    }
  });
  const headers = new Headers(res.headers);
  headers.set("access-control-allow-origin", "*");
  return new Response(res.body ? res.body : "", { status: res.status || 200, headers });
}
