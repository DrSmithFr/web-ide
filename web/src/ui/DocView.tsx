// A page of the documentation embedded in the pod, read only (tab kind doc): rendered Markdown
// with Mermaid, pictures read through docs.image, links that stay inside the docs (a page opens
// in the same tab, Back returns, #heading scrolls). Paths are those of the repo.
import { createEffect, createSignal, on, Show } from 'solid-js'
import { onMarkdownClick, renderMarkdownFile, renderMermaid } from './markdown'
import { request } from '../pod/rpc'
import { t } from '../i18n'
import { mutate, setActivePane, type TabState } from '../state/project'
import { backDoc, canGoBack, docsIndex, goDoc } from '../state/docs'
import { toast } from './toast'
import './markdown.css'

/** A path resolved from a folder, from the root of the docs ("docs/howto", "../guide.md"). */
function resolve(dir: string, rel: string) {
  const out: string[] = []
  for (const x of (rel.startsWith('/') ? rel : dir + '/' + rel).split('/')) {
    if (x === '..') out.pop()
    else if (x && x !== '.') out.push(x)
  }
  return out.join('/')
}

const dirname = (p: string) => p.slice(0, Math.max(0, p.lastIndexOf('/')))

export function DocView(props: { tab: TabState; paneId: string }) {
  let el!: HTMLDivElement
  let body!: HTMLDivElement
  const [error, setError] = createSignal('')
  // The page on screen 
  const [shown, setShown] = createSignal('')
  const images = new Map<string, Promise<string>>()
  // The newest request wins: a link followed before the page came does not show the old one.
  let seq = 0
  let loaded: string | undefined
  const target = (doc: string, anchor?: string) => doc + (anchor ? '#' + anchor : '')

  const image = (img: HTMLImageElement) => {
    const abs = resolve(dirname(props.tab.doc ?? ''), decodeURI(img.dataset.path ?? '').split(/[?#]/)[0])
    let p = images.get(abs)
    if (!p) {
      p = request<{ url: string }>('docs.image', { path: abs }).then((r) => r.url)
      images.set(abs, p)
    }
    p.then(
      (url) => (img.src = url),
      () => img.classList.add('md-img-missing'),
    )
  }

  const scrollTo = (anchor?: string) => {
    const target = anchor ? body.querySelector(`[id="${CSS.escape(decodeURIComponent(anchor))}"]`) : null
    if (target) target.scrollIntoView({ block: 'start' })
    else el.scrollTop = 0
  }

  createEffect(
    on(
      () => [props.tab.doc, props.tab.anchor] as const,
      async ([doc, anchor]) => {
        const mine = ++seq
        // Same page, another heading: no need to read it again.
        if (doc === loaded && !error()) {
          setShown(target(doc!, anchor))
          return scrollTo(anchor)
        }
        try {
          const r = await request<{ text: string }>('docs.read', { path: doc })
          if (mine !== seq) return
          setError('')
          body.replaceChildren(renderMarkdownFile(r.text))
          body.querySelectorAll<HTMLImageElement>('img[data-path]').forEach(image)
          // The title of the tab is the title of the page.
          const h1 = body.querySelector('h1')?.textContent
          if (h1 && props.tab.title !== h1) mutate((s) => (s.tabs[props.tab.id].title = h1))
          loaded = doc
          setShown(target(doc!, anchor))
          scrollTo(anchor)
          // The diagrams change the height: scroll again once they are drawn.
          renderMermaid(body, true).then(() => mine === seq && scrollTo(anchor), () => {})
        } catch (e) {
          if (mine !== seq) return
          body.replaceChildren()
          loaded = undefined
          setError((e as Error).message)
        }
      },
    ),
  )

  const click = (e: MouseEvent) => {
    const a = (e.target as HTMLElement).closest('a')
    const href = a?.getAttribute('href')
    if (!a || !href) return onMarkdownClick(e)
    // Other schemes (https:, mailto:) open in the browser.
    if (/^([a-z][a-z0-9+.-]*:|\/\/)/i.test(href)) return
    e.preventDefault()
    const [file, hash = ''] = href.split('#')
    if (!file) return scrollTo(hash)
    const path = resolve(dirname(props.tab.doc ?? ''), decodeURI(file))
    // The docs hold Markdown pages; a link to the code, a folder or a file outside is not here.
    if (!/\.md$/i.test(path)) return toast(t('Not in the embedded documentation: {path}', { path }), 'info')
    goDoc(props.tab.id, hash ? `${path}#${hash}` : path)
  }

  return (
    <div class="file-editor doc-view" data-testid="doc-view" data-doc={props.tab.doc} data-shown={shown()} onMouseDown={() => setActivePane(props.paneId)}>
      <div class="doc-bar">
        <button class="btn small" disabled={!canGoBack(props.tab.id)} onClick={() => backDoc(props.tab.id)} data-testid="doc-back">
          ← {t('Back')}
        </button>
        <button class="btn small" disabled={props.tab.doc === docsIndex && !props.tab.anchor} onClick={() => goDoc(props.tab.id, docsIndex)} data-testid="doc-index">
          {t('Documentation')}
        </button>
      </div>
      <div class="md-file-view doc-scroll" ref={el} tabindex="0" onClick={click}>
        <Show when={error()}>
          <p class="muted doc-error">{error()}</p>
        </Show>
        <div class="md md-file" ref={body} />
      </div>
    </div>
  )
}
