// The sandbox API of an external plugin (docs/design/plugins.md, *Sandboxed views*): its views
// run in an <iframe sandbox="allow-scripts"> (an opaque origin) whose CSP forbids the network;
// one MessageChannel per frame carries what they may ask. The page is the boundary: a method of
// the core needs its grant, an event must be of the plugin's namespace.
import { on, request } from '../pod/rpc'
import { openFile, openTab, mutate, type TabState } from '../state/project'
import { toast } from '../ui/toast'
import { lang, t } from '../i18n'

/** The CSP of a frame: inline scripts and styles, images as data, no network at all. */
export const frameCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'"

/**
 * The bootstrap run in the frame before the plugin's module: `ide`, the API the module calls
 * (register, rpc, on, openFile, openView, setViewState, t, tn, toast, theme), over the port the
 * page sends once. Plain ES2020, no import.
 */
export const bootstrap = `(() => {
  let port, info = null, registered = false, mounted = false
  let views = {}, commands = {}
  const pending = new Map(), handlers = {}, themeFns = []
  let next = 1
  const send = (m) => port && port.postMessage(m)
  const call = (m) => new Promise((resolve, reject) => { const id = next++; pending.set(id, { resolve, reject }); send({ ...m, id }) })
  const fill = (s, p) => (p ? s.replace(/\\{(\\w+)\\}/g, (m, k) => (k in p ? String(p[k]) : m)) : s)
  const applyTheme = (th) => {
    for (const [k, v] of Object.entries(th || {})) document.documentElement.style.setProperty(k, v)
    themeFns.forEach((f) => f(th))
  }
  const ide = {
    register(c) { views = (c && c.views) || {}; commands = (c && c.commands) || {}; registered = true; start() },
    get view() { return info && info.view },
    get lang() { return info && info.lang },
    get theme() { return info && info.theme },
    rpc: (method, params) => call({ call: 'rpc', method, params }),
    on(event, f) { (handlers[event] = handlers[event] || []).push(f); send({ call: 'on', event }) },
    openFile: (path, line) => send({ call: 'openFile', path, line }),
    openView: (view, state, title) => send({ call: 'openView', view, state, title }),
    setViewState: (state) => send({ call: 'setViewState', state }),
    toast: (text, kind) => send({ call: 'toast', text: String(text), kind }),
    t: (k, p) => fill((info && info.catalog && info.catalog[k]) || k.slice(k.indexOf('|') + 1), p),
    tn: (n, one, other, p) => ide.t(n === 1 ? one : other, { n, ...p }),
    onTheme: (f) => themeFns.push(f),
  }
  window.ide = ide
  function start() {
    if (!info || !registered || mounted) return
    mounted = true
    const v = info.view && views[info.view.id]
    if (v) v(document.getElementById('root'), ide)
    send({ call: 'ready' })
  }
  function receive(e) {
    const m = e.data || {}
    if (m.init) { info = m.init; applyTheme(info.theme); start(); return }
    if (m.reply && pending.has(m.reply)) {
      const p = pending.get(m.reply); pending.delete(m.reply)
      if ('error' in m) p.reject(new Error(m.error)); else p.resolve(m.result)
      return
    }
    if (m.theme) { info.theme = m.theme; applyTheme(m.theme) }
    if (m.lang) { info.lang = m.lang; info.catalog = m.catalog }
    if (m.event) (handlers[m.event] || []).forEach((f) => f(m.data))
    if (m.command && commands[m.command]) commands[m.command](ide)
  }
  window.addEventListener('message', (e) => {
    if (e.source !== parent || port || !e.ports[0]) return
    port = e.ports[0]
    port.onmessage = receive
  })
  // The shortcuts of the IDE work from the frame: the keys with a modifier, Escape and F1-F12.
  document.addEventListener('keydown', (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.key === 'Escape' || /^F\\d+$/.test(e.key))
      send({ call: 'key', key: e.key, code: e.code, ctrl: e.ctrlKey, shift: e.shiftKey, alt: e.altKey, meta: e.metaKey })
  })
  window.addEventListener('focus', () => send({ call: 'focus' }))
})()`

/** The theme of the IDE as CSS variables: those the page set on its root. */
export function themeVars(): Record<string, string> {
  const out: Record<string, string> = {}
  const s = document.documentElement.style
  for (let i = 0; i < s.length; i++) {
    const k = s[i]
    if (k.startsWith('--')) out[k] = s.getPropertyValue(k)
  }
  return out
}

const escapeHTML = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

/** The document of a frame: its CSP, the base styles from the theme, the bootstrap, the module. */
export function frameDoc(js: string): string {
  // A </script> in the module would end its element: written as <\/script>, the same in JS.
  const module = js.replace(/<\/(script)/gi, '<\\/$1')
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${escapeHTML(frameCSP)}">
<style>html,body{margin:0;height:100%;background:var(--bg);color:var(--fg);font-family:var(--font-ui,sans-serif);font-size:var(--font-size-ui,13px)}#root{height:100%;overflow:auto}</style>
<script>${bootstrap}</script></head><body><div id="root"></div><script type="module">${module}</script></body></html>`
}

/** What the page knows of the frame it serves. */
export type FrameHost = {
  plugin: string
  name: string
  /** Its grants now: a grant revoked in Settings is refused at once. */
  grants: () => string[]
  catalogs: Record<string, Record<string, string>>
  /** The view of the frame ("" for the hidden one of the commands) and its tab (a tab view). */
  view: string
  tab?: TabState
  onReady?: () => void
}

/**
 * Serves a frame: sends it its port once loaded, then answers what it asks within its grants.
 * Returns the function that sends it a message and the one that stops serving it.
 */
export function serveFrame(frame: HTMLIFrameElement, host: FrameHost) {
  // A frame moved in the page loads again: each load gets a new channel.
  let port: MessagePort | undefined
  let offs: (() => void)[] = []
  const catalog = () => host.catalogs[lang()] ?? {}
  const handle = (port: MessagePort) => (e: MessageEvent) => {
    const reply = (id: number, r: { result?: unknown; error?: string }) => port.postMessage({ reply: id, ...r })
    const m = e.data ?? {}
    switch (m.call) {
      case 'rpc': {
        const method = String(m.method ?? '')
        const own = method.startsWith(host.plugin + '.')
        if (!own && !host.grants().includes('core:' + method)) {
          reply(m.id, { error: t('{method} is not granted to {name}', { method, name: host.name }) })
          return
        }
        request(method, m.params).then(
          (result) => reply(m.id, { result }),
          (err) => reply(m.id, { error: err instanceof Error ? err.message : String(err) }),
        )
        return
      }
      case 'on': {
        const event = String(m.event ?? '')
        if (event.startsWith(host.plugin + '.')) offs.push(on(event, (data: unknown) => port.postMessage({ event, data })))
        return
      }
      case 'openFile':
        if (typeof m.path === 'string') void openFile(typeof m.line === 'number' ? { path: m.path, line: m.line } : m.path)
        return
      case 'openView':
        if (typeof m.view === 'string') openTab({ kind: `plugin:${host.plugin}/${m.view}`, title: typeof m.title === 'string' ? m.title : undefined, state: m.state } as Omit<TabState, 'id'>)
        return
      case 'setViewState': {
        const tab = host.tab
        if (tab)
          mutate((s) => {
            const cur = s.tabs[tab.id]
            if (cur?.kind.startsWith(`plugin:${host.plugin}/`)) cur.state = m.state
          })
        return
      }
      case 'toast':
        toast(String(m.text ?? '').slice(0, 500), ['info', 'warn', 'error', 'ok'].includes(m.kind) ? m.kind : 'info')
        return
      case 'key':
        // Only while the user is in the frame: a plugin cannot press the IDE's keys on its own.
        if (document.activeElement !== frame) return
        frame.dispatchEvent(new KeyboardEvent('keydown', { key: m.key, code: m.code, ctrlKey: !!m.ctrl, shiftKey: !!m.shift, altKey: !!m.alt, metaKey: !!m.meta, bubbles: true, cancelable: true }))
        return
      case 'focus':
        frame.dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
        return
      case 'ready':
        host.onReady?.()
        return
    }
  }
  const send = (m: unknown) => port?.postMessage(m)
  const unsubscribe = () => {
    offs.forEach((f) => f())
    offs = []
  }
  const start = () => {
    unsubscribe()
    port?.close()
    const ch = new MessageChannel()
    port = ch.port1
    port.onmessage = handle(port)
    frame.contentWindow?.postMessage('ide', '*', [ch.port2])
    // The state of the tab is a proxy of the store: a plain copy crosses the channel.
    const state = host.tab?.state === undefined ? undefined : JSON.parse(JSON.stringify(host.tab.state))
    send({ init: { view: { id: host.view, state }, lang: lang(), catalog: catalog(), theme: themeVars() } })
  }
  frame.addEventListener('load', start)
  // The theme and the language follow the page.
  const observer = new MutationObserver(() => send({ theme: themeVars() }))
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['style'] })
  return {
    send,
    lang: () => send({ lang: lang(), catalog: catalog() }),
    stop: () => {
      frame.removeEventListener('load', start)
      observer.disconnect()
      unsubscribe()
      port?.close()
    },
  }
}
