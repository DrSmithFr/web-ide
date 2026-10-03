// Markdown of the answers: marked, sanitized by DOMPurify, code blocks colored with the
// editor grammars, Mermaid diagrams rendered once the answer is complete.
import { Marked, type Tokens } from 'marked'
import DOMPurify from 'dompurify'
import { grammar, grammarDefs } from '../editor/languages'
import { openDiagram } from './DiagramViewer'

const aliases: Record<string, string> = {
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', ts: 'typescript', tsx: 'typescript', py: 'python', golang: 'go',
  sh: 'shell', bash: 'shell', zsh: 'shell', console: 'shell', yml: 'yaml', htm: 'html', xml: 'html', vue: 'html', svelte: 'html',
  md: 'markdown', mysql: 'sql', pgsql: 'sql', postgres: 'sql', postgresql: 'sql', sqlite: 'sql', jsonc: 'json', json5: 'json',
  scss: 'css', less: 'css', conf: 'nginx',
}

function grammarId(lang: string): string | null {
  const l = lang.toLowerCase()
  const id = aliases[l] ?? l
  const defs = grammarDefs()
  if (defs.some((d) => d.id === id)) return id
  return defs.find((d) => d.extensions.includes('.' + l))?.id ?? null
}

function escape(s: string) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

/** HTML of a code block colored with the editor tokenizer (classes tok-*). */
export function highlightCode(code: string, lang: string): string {
  const id = grammarId(lang)
  if (!id || code.length > 200_000) return escape(code)
  const g = grammar(id)
  let state = 'root'
  const out: string[] = []
  for (const line of code.split('\n')) {
    const { tokens, end } = g.line(line, state)
    state = end
    let pos = 0
    let html = ''
    for (const [s, e, type] of tokens) {
      if (s > pos) html += escape(line.slice(pos, s))
      html += `<span class="tok-${type}">${escape(line.slice(s, e))}</span>`
      pos = e
    }
    out.push(html + escape(line.slice(pos)))
  }
  return out.join('\n')
}

const md = new Marked({
  gfm: true,
  breaks: false,
  renderer: {
    code({ text, lang, raw }: Tokens.Code) {
      const l = (lang ?? '').trim().split(/\s+/)[0]
      // URI-encoded: DOMPurify drops attributes containing "-->", the arrow of Mermaid.
      // data-closed: the closing fence has arrived (a streamed diagram can be drawn).
      if (l === 'mermaid') {
        const closed = /\n\s*(```|~~~)\s*$/.test(raw)
        return `<div class="md-mermaid${closed ? '' : ' md-mermaid-pending'}" data-src="${encodeURIComponent(text)}"${closed ? ' data-closed="1"' : ''}><div class="md-mermaid-wait">Diagramme en cours d’écriture…</div><pre class="md-code"><code>${escape(text)}</code></pre></div>`
      }
      return `<div class="md-codeblock"><div class="md-code-head"><span>${escape(l)}</span><button class="md-copy" type="button">Copier</button></div><pre class="md-code"><code>${highlightCode(text, l)}</code></pre></div>`
    },
    link({ href, title, tokens }: Tokens.Link) {
      const text = this.parser.parseInline(tokens)
      return `<a href="${escape(href)}"${title ? ` title="${escape(title)}"` : ''} target="_blank" rel="noopener noreferrer">${text}</a>`
    },
  },
})

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})

export function renderMarkdown(text: string): string {
  const html = md.parse(text, { async: false }) as string
  return DOMPurify.sanitize(html, { ADD_ATTR: ['target', 'data-src', 'data-closed'], FORBID_TAGS: ['style', 'form', 'input'] })
}

let mermaidSeq = 0
let mermaidLoad: Promise<any> | null = null
/** Diagrams already drawn, by source and theme: re-inserted at once on each render. */
const svgCache = new Map<string, { svg?: string; error?: string }>()
const drawing = new Map<string, Promise<void>>()

function place(b: HTMLElement, r: { svg?: string; error?: string }) {
  b.dataset.done = '1'
  if (r.svg) {
    const box = document.createElement('div')
    box.className = 'md-mermaid-svg'
    box.innerHTML = r.svg
    const open = document.createElement('button')
    open.type = 'button'
    open.className = 'md-mermaid-open'
    open.title = 'Ouvrir en plein écran'
    open.textContent = '⤢'
    box.append(open)
    b.prepend(box)
    b.querySelector('pre')?.classList.add('md-mermaid-src')
  } else {
    const err = document.createElement('div')
    err.className = 'md-mermaid-error'
    err.textContent = `Diagramme Mermaid invalide : ${r.error}`
    b.prepend(err)
  }
}

async function draw(key: string, src: string, dark: boolean) {
  mermaidLoad ??= import('mermaid').then((m) => m.default)
  const mermaid = await mermaidLoad
  // SVG text labels: the HTML ones (foreignObject) would not survive DOMPurify.
  mermaid.initialize({ startOnLoad: false, theme: dark ? 'dark' : 'default', securityLevel: 'strict', fontFamily: 'inherit', htmlLabels: false, flowchart: { htmlLabels: false } })
  const id = `mermaid-${++mermaidSeq}`
  try {
    const { svg } = await mermaid.render(id, src)
    svgCache.set(key, { svg: DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true } }) })
  } catch (e) {
    svgCache.set(key, { error: (e as Error).message?.split('\n')[0] ?? String(e) })
    // Mermaid leaves its error drawing in the body.
    document.getElementById(`d${id}`)?.remove()
  }
  if (svgCache.size > 100) svgCache.delete(svgCache.keys().next().value!)
}

/**
 * Draws the mermaid blocks of el (the source stays on error). While streaming (final
 * false), only the blocks whose closing fence has arrived; diagrams already drawn are put
 * back synchronously, so a re-render does not blink.
 */
export async function renderMermaid(el: HTMLElement, final = true) {
  const dark = document.documentElement.dataset.theme !== 'light'
  const blocks = [...el.querySelectorAll<HTMLElement>('.md-mermaid:not([data-done])')].filter((b) => final || b.dataset.closed)
  const todo: [HTMLElement, string, string][] = []
  for (const b of blocks) {
    const src = decodeURIComponent(b.dataset.src ?? '')
    const key = `${dark ? 'd' : 'l'}:${src}`
    const hit = svgCache.get(key)
    if (hit) place(b, hit)
    else todo.push([b, key, src])
  }
  for (const [b, key, src] of todo) {
    let p = drawing.get(key)
    if (!p) {
      p = draw(key, src, dark).finally(() => drawing.delete(key))
      drawing.set(key, p)
    }
    await p
    if (b.isConnected && !b.dataset.done) place(b, svgCache.get(key)!)
  }
}

/** Click handler of a rendered answer: copy buttons, full screen diagrams. */
export function onMarkdownClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  const diagram = target.closest('.md-mermaid-open, .md-mermaid-svg svg')
  if (diagram) {
    const block = diagram.closest('.md-mermaid') as HTMLElement
    const svg = block?.querySelector('.md-mermaid-svg svg')
    if (svg) openDiagram(svg.outerHTML, decodeURIComponent(block.dataset.src ?? ''))
    return
  }
  const btn = (e.target as HTMLElement).closest('.md-copy') as HTMLButtonElement | null
  if (!btn) return
  const code = btn.closest('.md-codeblock')?.querySelector('code')?.textContent ?? ''
  navigator.clipboard?.writeText(code).then(
    () => {
      btn.textContent = 'Copié'
      setTimeout(() => (btn.textContent = 'Copier'), 1200)
    },
    () => {},
  )
}
