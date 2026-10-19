// Rendered view of a Markdown file, the default view of .md files: the text of the buffer
// (unsaved edits included), read only. A double-click goes back to the source at the line of
// the block; links open the files of the project, images are read through the pod.
import { createEffect, createSignal, onCleanup, onMount } from 'solid-js'
import type { Doc } from '../editor/doc'
import { onMarkdownClick, renderMarkdownFile, renderMermaid } from './markdown'
import { openFile, root } from '../state/project'
import { request } from '../pod/rpc'
import './markdown.css'

// Tabs shown raw: the others show their Markdown rendered (until the page reloads).
const [rawTabs, setRawTabs] = createSignal(new Set<string>())

export function isMarkdownPath(path: string) {
  return /\.(md|markdown|mdown|mkd)$/i.test(path)
}

export function shownRaw(tab: string) {
  return rawTabs().has(tab)
}

export function showRaw(tab: string, raw: boolean) {
  setRawTabs((s) => {
    const next = new Set(s)
    if (raw) next.add(tab)
    else next.delete(tab)
    return next
  })
}

/** Folder of a path, and a relative path resolved from a folder. */
function dirname(p: string) {
  return p.slice(0, p.lastIndexOf('/')) || '/'
}

function resolve(dir: string, rel: string) {
  const parts = (rel.startsWith('/') ? rel : dir + '/' + rel).split('/')
  const out: string[] = []
  for (const x of parts) {
    if (x === '..') out.pop()
    else if (x && x !== '.') out.push(x)
  }
  return '/' + out.join('/')
}

export interface MarkdownViewApi {
  /** Line of the source at the top of the view. */
  topLine(): number
  scrollToLine(line: number): void
  focus(): void
}

export function MarkdownView(props: { doc: Doc; path: string; onEdit: (line: number) => void; api: (a: MarkdownViewApi) => void }) {
  let el!: HTMLDivElement
  let body!: HTMLDivElement
  const images = new Map<string, Promise<string>>()
  const blocks = () => [...body.querySelectorAll<HTMLElement>('.md-block')]

  const image = (img: HTMLImageElement) => {
    const rel = decodeURI(img.dataset.path ?? '').split(/[?#]/)[0]
    // A path from / is from the root of the project, as on GitHub.
    const abs = rel.startsWith('/') ? resolve(root(), rel) : resolve(dirname(props.path), rel)
    let p = images.get(abs)
    if (!p) {
      p = request<{ url: string }>('fs.image', { path: abs }).then((r) => r.url)
      images.set(abs, p)
    }
    p.then(
      (url) => (img.src = url),
      () => img.classList.add('md-img-missing'),
    )
  }

  let timer: number | undefined
  const render = () => {
    const top = el.scrollTop
    body.replaceChildren(renderMarkdownFile(props.doc.text))
    body.querySelectorAll<HTMLImageElement>('img[data-path]').forEach(image)
    renderMermaid(body, true).catch(() => {})
    el.scrollTop = top
  }
  createEffect(() => {
    props.doc.changed()
    clearTimeout(timer)
    // At once when shown, then a moment after the edits of the source (another pane).
    if (!body.firstChild) render()
    else timer = window.setTimeout(render, 200)
  })
  onCleanup(() => clearTimeout(timer))

  onMount(() =>
    props.api({
      topLine() {
        const top = el.getBoundingClientRect().top
        const b = blocks().find((x) => x.getBoundingClientRect().bottom > top + 4)
        return Number(b?.dataset.line ?? 0)
      },
      scrollToLine(line) {
        let target: HTMLElement | undefined
        for (const b of blocks()) {
          if (Number(b.dataset.line) > line) break
          target = b
        }
        el.scrollTop = target ? target.getBoundingClientRect().top - body.getBoundingClientRect().top : 0
      },
      focus: () => el.focus({ preventScroll: true }),
    }),
  )

  const click = (e: MouseEvent) => {
    const a = (e.target as HTMLElement).closest('a')
    const href = a?.getAttribute('href')
    if (!a || !href) return onMarkdownClick(e)
    // Other schemes (https:, mailto:) open in the browser.
    if (/^([a-z][a-z0-9+.-]*:|\/\/)/i.test(href)) return
    e.preventDefault()
    const [file, hash = ''] = href.split('#')
    if (!file) {
      const id = decodeURIComponent(hash)
      body.querySelector(`[id="${CSS.escape(id)}"]`)?.scrollIntoView({ block: 'start' })
      return
    }
    const rel = decodeURI(file)
    const path = rel.startsWith('/') ? resolve(root(), rel) : resolve(dirname(props.path), rel)
    // #L12 (GitHub) or #12: a line of the file.
    const m = /^L?(\d+)/.exec(hash)
    openFile(m ? { path, line: Number(m[1]) - 1 } : path)
  }

  const dblclick = (e: MouseEvent) => {
    if ((e.target as HTMLElement).closest('a, button')) return
    const b = (e.target as HTMLElement).closest<HTMLElement>('.md-block')
    props.onEdit(Number(b?.dataset.line ?? 0))
  }

  return (
    <div class="md-file-view" ref={el} tabindex="0" onClick={click} onDblClick={dblclick} data-testid="md-view">
      <div class="md md-file" ref={body} />
    </div>
  )
}
