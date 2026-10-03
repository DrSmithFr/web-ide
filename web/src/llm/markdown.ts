// Markdown of the answers: marked, sanitized by DOMPurify, code blocks colored with the
// editor grammars, Mermaid diagrams rendered once the answer is complete.
import { Marked, type Tokens } from 'marked'
import DOMPurify from 'dompurify'
import { grammar, grammarDefs } from '../editor/languages'

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
    code({ text, lang }: Tokens.Code) {
      const l = (lang ?? '').trim().split(/\s+/)[0]
      // URI-encoded: DOMPurify drops attributes containing "-->", the arrow of Mermaid.
      if (l === 'mermaid') return `<div class="md-mermaid" data-src="${encodeURIComponent(text)}"><pre class="md-code"><code>${escape(text)}</code></pre></div>`
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
  return DOMPurify.sanitize(html, { ADD_ATTR: ['target', 'data-src'], FORBID_TAGS: ['style', 'form', 'input'] })
}

let mermaidSeq = 0
let mermaidLoad: Promise<any> | null = null

/** Replaces the mermaid blocks of el by their diagram (the source stays on error). */
export async function renderMermaid(el: HTMLElement) {
  const blocks = [...el.querySelectorAll<HTMLElement>('.md-mermaid:not([data-done])')]
  if (!blocks.length) return
  mermaidLoad ??= import('mermaid').then((m) => m.default)
  const mermaid = await mermaidLoad
  const dark = document.documentElement.dataset.theme !== 'light'
  // SVG text labels: the HTML ones (foreignObject) would not survive DOMPurify.
  mermaid.initialize({ startOnLoad: false, theme: dark ? 'dark' : 'default', securityLevel: 'strict', fontFamily: 'inherit', htmlLabels: false, flowchart: { htmlLabels: false } })
  for (const b of blocks) {
    b.dataset.done = '1'
    try {
      const { svg } = await mermaid.render(`mermaid-${++mermaidSeq}`, decodeURIComponent(b.dataset.src ?? ''))
      const box = document.createElement('div')
      box.className = 'md-mermaid-svg'
      box.innerHTML = DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true } })
      b.prepend(box)
      b.querySelector('pre')?.classList.add('md-mermaid-src')
    } catch (e) {
      const err = document.createElement('div')
      err.className = 'md-mermaid-error'
      err.textContent = `Diagramme Mermaid invalide : ${(e as Error).message?.split('\n')[0] ?? e}`
      b.prepend(err)
      // Mermaid leaves its error drawing in the body.
      document.getElementById(`dmermaid-${mermaidSeq}`)?.remove()
    }
  }
}

/** Click handler of a rendered answer: copy buttons. */
export function onMarkdownClick(e: MouseEvent) {
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
