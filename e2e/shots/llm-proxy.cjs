// Model server for the screenshots: records the answers of a real OpenAI-compatible server
// (record mode), or plays them back without it (replay mode), so the pictures can be made
// again after a change of the interface with the same conversations.
//
// In record mode the answers already recorded are played back too: a scenario that failed half
// way goes on from where it stopped without asking the model again.
//
// An answer is found by the conversation it belongs to (its first user message) and the
// number of messages sent: the scenario drives the same conversations in the same order.
const fs = require('fs')
const http = require('http')
const zlib = require('zlib')

/** Drops what the page does not need from the streamed events but the last ones (timings, ids). */
const slim = (chunks) =>
  chunks.map(([pause, s], i) =>
    i >= chunks.length - 3
      ? [pause, s]
      : [pause, s.replace(/^data: (\{.*\})$/gm, (all, json) => {
          const e = JSON.parse(json)
          for (const k of ['timings', 'id', 'created', 'system_fingerprint']) delete e[k]
          return 'data: ' + JSON.stringify(e)
        })],
  )

/** The recording, gzipped: the streams of a long session weigh a few megabytes. */
exports.load = (file) => JSON.parse(zlib.gunzipSync(fs.readFileSync(file)))
const write = (file, rec) => fs.writeFileSync(file, zlib.gzipSync(JSON.stringify(rec), { level: 9 }))
exports.slim = slim
exports.write = write

const key = (r) => {
  const text = (m) => (typeof m?.content === 'string' ? m.content : (m?.content ?? []).map((p) => p.text ?? '').join(' '))
  const first = r.messages.find((m) => m.role === 'user')
  return `${text(first).slice(0, 200)}|${r.messages.length}`
}

/**
 * Starts the server. opts: { mode: 'record' | 'replay', file, upstream (record), speed (replay:
 * the recorded pauses are divided by it, so that an answer streams in maxTotal ms at most) }.
 */
exports.start = async ({ mode, file, upstream, speed = 6, maxTotal = 2000 }) => {
  const rec = fs.existsSync(file) ? exports.load(file) : { commit: '', get: {}, chats: [] }
  const used = new Set()
  const save = () => write(file, rec)

  const server = http.createServer(async (req, res) => {
    let body = ''
    for await (const c of req) body += c
    if (req.method === 'GET') {
      if (mode === 'replay' || rec.get[req.url]) {
        const g = rec.get[req.url]
        return g ? res.writeHead(g.status, { 'Content-Type': 'application/json' }).end(g.body) : res.writeHead(404).end()
      }
      const r = await fetch(upstream + req.url)
      const text = await r.text()
      rec.get[req.url] = { status: r.status, body: text }
      save()
      return res.writeHead(r.status, { 'Content-Type': r.headers.get('content-type') ?? 'application/json' }).end(text)
    }
    const r = JSON.parse(body)
    const k = key(r)
    const i = rec.chats.findIndex((c, i) => !used.has(i) && c.key === k)
    if (mode === 'replay' || i >= 0) {
      if (i < 0) {
        console.error('llm-proxy: no recorded answer for ' + k)
        return res.writeHead(500).end('{"error":{"message":"no recorded answer"}}')
      }
      used.add(i)
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      const chunks = rec.chats[i].chunks
      const total = chunks.reduce((n, [pause]) => n + pause, 0)
      const div = Math.max(speed, total / maxTotal)
      for (const [pause, chunk] of chunks) {
        if (pause / div >= 4) await new Promise((ok) => setTimeout(ok, pause / div))
        if (res.destroyed) return
        res.write(chunk)
      }
      return res.end()
    }
    // Record: forward, stream back, keep every chunk with the pause before it.
    const up = await fetch(upstream + req.url, { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: req.headers.authorization ?? '' }, body })
    res.writeHead(up.status, { 'Content-Type': up.headers.get('content-type') ?? 'text/event-stream' })
    const chunks = []
    let last = Date.now()
    const dec = new TextDecoder()
    for await (const c of up.body) {
      const s = dec.decode(c, { stream: true })
      chunks.push([Date.now() - last, s])
      last = Date.now()
      res.write(s)
    }
    res.end()
    rec.chats.push({ key: k, chunks: slim(chunks) })
    save()
  })
  await new Promise((ok) => server.listen(0, '127.0.0.1', ok))
  return {
    port: server.address().port,
    rec,
    save,
    close: () => server.close(),
  }
}
