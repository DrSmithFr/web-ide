// Search Everywhere: one popup with a tab per search provider (searchProviders.ts): the files,
// the symbols of the language servers and the actions of the core, and those of the plugins (the
// text of the project: Search). Double Shift opens it on All; Go to file, Go to symbol and the
// command palette open it on their own tab. Tab / Shift+Tab change the tab.
import { batch, createEffect, createSignal, For, on, onCleanup, onMount, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import { request } from '../pod/rpc'
import { activeTab, basename, openFile, relPath } from '../state/project'
import { desks } from '../state/desks'
import { actionLabel, allActions, isCapturingKeys, runAction, shortcutOf } from '../keys/bindings'
import { lspLanguage } from '../editor/languages'
import * as lspc from '../lsp/client'
import { fuzzy } from '../ui/overlay'
import { FileIcon } from '../panels/fileIcons'
import { Icon } from '../ui/icons'
import { keepFocus } from '../state/focus'
import { t } from '../i18n'
import { searchProviders, setCoreProviders, type SearchHit } from './searchProviders'
import './popups.css'

/** All, or the id of a provider (files, symbols, actions, text…). */
export type SearchTab = string

type Hit = SearchHit

interface Source {
  delay: number
  find: (q: string, signal: AbortSignal) => Promise<Hit[]>
}

const [open, setOpen] = createSignal(false)
const [tab, setTab] = createSignal<SearchTab>('all')
let lastQuery = ''

let onClosed: (() => void) | undefined

/** Opens the popup on a tab; already open, switches to it. onClose runs once it closes (a choice run or not). */
export function searchEverywhere(on: SearchTab = 'all', opts: { onClose?: () => void } = {}) {
  onClosed = opts.onClose
  batch(() => {
    setTab(on)
    setOpen(true)
  })
}

// ---------- sources ----------

let fileCache: { at: number; files: string[] } | null = null
async function projectFiles() {
  if (!fileCache || Date.now() - fileCache.at > 15000) fileCache = { at: Date.now(), files: await request<string[]>('search.files') }
  return fileCache.files
}

const fileHit = (f: string): Hit => ({ label: basename(f), detail: relPath(f), path: true, icon: () => <FileIcon name={basename(f)} />, run: () => void openFile(f) })

async function findFiles(q: string, max: number) {
  const list = await projectFiles()
  // Without a query, the recent files come first.
  if (!q) return [...new Set([...desks.recent, ...list])].slice(0, max).map(fileHit)
  return list
    .map((f) => ({ f, s: fuzzy(q, basename(f)) * 2 + fuzzy(q, relPath(f)) }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s)
    .slice(0, max)
    .map(({ f }) => fileHit(f))
}

/** A path of a language with a running server: the active file, else the detected language. */
async function symbolPath() {
  const path = activeTab()?.path ?? ''
  if (lspLanguage(path)) return path
  const st = await request<any[]>('lsp.status').catch(() => [])
  const lang = st.find((s) => s.detected && s.command)?.lang
  const ext: Record<string, string> = { go: '/x.go', php: '/x.php', python: '/x.py', typescript: '/x.ts' }
  return lang ? ext[lang] : ''
}

async function findSymbols(q: string, max: number, signal: AbortSignal) {
  if (q.length < 2) return []
  const path = await symbolPath()
  if (!path) return []
  const list = await lspc.workspaceSymbols(path, q, signal).catch(() => [])
  return list
    .filter((s) => s.loc)
    .slice(0, max)
    .map(
      (s): Hit => ({
        label: s.name,
        code: true,
        path: true,
        icon: () => <span class="se-glyph">{lspc.symbolKinds[s.kind]?.[1] ?? '·'}</span>,
        detail: `${s.container ? s.container + ' · ' : ''}${relPath(s.loc!.path)}`,
        run: () => lspc.jump(s.loc!),
      }),
    )
}

/** The actions, the saved commands of the project first (dynamic actions with an icon). */
function findActions(q: string, max: number) {
  const list = allActions()
  const all = [...list.filter((a) => a.run), ...list.filter((a) => !a.run)].map((a) => ({ a, label: actionLabel(a), category: t(a.category) }))
  const ranked = !q ? all : all.map((x) => ({ ...x, s: Math.max(fuzzy(q, x.label), fuzzy(q, x.category) * 0.5) })).filter((x) => x.s > 0).sort((a, b) => b.s - a.s)
  return ranked.slice(0, max).map(
    ({ a, label, category }): Hit => ({
      label,
      detail: a.detail ?? category,
      hint: shortcutOf(a.id),
      icon: () => (a.icon ? <Icon name={a.icon} size={13} /> : <span class="se-glyph">›</span>),
      run: () => requestAnimationFrame(() => runAction(a.id)),
    }),
  )
}

// The providers of the core; the text of the project is the Search plugin's.
setCoreProviders([
  { id: 'files', label: 'Files', placeholder: 'Go to file (name or path, fuzzy search)', order: 10, inAll: 8, delay: 0, max: 100, find: (q, _s, max) => findFiles(q, max) },
  { id: 'symbols', label: 'Symbols', placeholder: 'Go to symbol (class, function, method…)', order: 20, inAll: 8, delay: 150, minChars: 2, find: (q, s, max) => findSymbols(q, max, s) },
  { id: 'actions', label: 'Actions', placeholder: 'Command…', order: 30, inAll: 8, delay: 0, find: async (q, _s, max) => findActions(q, max) },
])

/** The tab shown: the one asked, All when its provider is not there (its plugin is off). */
const shown = () => (tabs().some(([id]) => id === tab()) ? tab() : 'all')

/** The tabs: All, then a tab per provider. */
const tabs = () => [['all', 'All'] as [string, string], ...searchProviders().map((p): [string, string] => [p.id, p.label])]

/** The sources of a tab: All shows the first results of the providers that take part in it. */
function sourcesOf(tab: SearchTab): [string, Source][] {
  if (tab === 'all') return searchProviders().filter((p) => p.inAll).map((p) => [p.label, { delay: p.delay, find: (q, s) => p.find(q, s, p.inAll!) }])
  const p = searchProviders().find((x) => x.id === tab)
  return p ? [['', { delay: p.delay, find: (q, s) => p.find(q, s, p.max ?? 200) }]] : []
}

const placeholderOf = (tab: SearchTab) => (tab === 'all' ? 'Search files, symbols and actions' : (searchProviders().find((p) => p.id === tab)?.placeholder ?? ''))

// ---------- popup ----------

/** Label with its highlighted ranges. */
function Marked(props: { text: string; ranges?: [number, number][] }) {
  const parts = () => {
    const out: { s: string; on: boolean }[] = []
    let at = 0
    for (const [a, b] of props.ranges ?? []) {
      if (a < at || a < 0) continue
      out.push({ s: props.text.slice(at, a), on: false }, { s: props.text.slice(a, b), on: true })
      at = b
    }
    out.push({ s: props.text.slice(at), on: false })
    return out
  }
  return <For each={parts()}>{(p) => (p.on ? <mark>{p.s}</mark> : p.s)}</For>
}

export function SearchEverywhereHost() {
  let input!: HTMLInputElement
  let list!: HTMLDivElement
  const [query, setQuery] = createSignal('')
  const [groups, setGroups] = createSignal<{ title: string; hits: Hit[] }[]>([])
  const [index, setIndex] = createSignal(0)
  const [busy, setBusy] = createSignal(false)
  const hits = () => groups().flatMap((g) => g.hits)
  let restoreFocus: () => HTMLElement | null = () => null
  let ctrl: AbortController | null = null

  const close = (hit?: Hit) => {
    ctrl?.abort()
    lastQuery = query()
    setOpen(false)
    restoreFocus()
    hit?.run()
    const f = onClosed
    onClosed = undefined
    f?.()
  }

  createEffect(
    on(open, (o) => {
      if (!o) return
      restoreFocus = keepFocus()
      setQuery(lastQuery)
      queueMicrotask(() => input?.select())
    }),
  )

  // Each source fills its group as soon as it answers; the slow ones wait for the typing to pause.
  createEffect(() => {
    if (!open()) return
    const q = query().trim()
    const list = sourcesOf(shown())
    ctrl?.abort()
    const c = new AbortController()
    ctrl = c
    setIndex(0)
    setGroups(list.map(([title]) => ({ title, hits: [] })))
    let pending = list.length
    setBusy(true)
    list.forEach(([, src], i) => {
      const go = () =>
        src
          .find(q, c.signal)
          .then((res) => !c.signal.aborted && setGroups((g) => g.map((x, j) => (j === i ? { ...x, hits: res } : x))))
          .catch(() => {})
          .finally(() => !c.signal.aborted && --pending === 0 && setBusy(false))
      if (!src.delay) return void go()
      const timer = window.setTimeout(go, src.delay)
      c.signal.addEventListener('abort', () => clearTimeout(timer))
    })
  })

  createEffect(() => list?.querySelector(`[data-index="${index()}"]`)?.scrollIntoView({ block: 'nearest' }))

  const key = (e: KeyboardEvent) => {
    const n = hits().length
    if (e.key === 'ArrowDown') setIndex((i) => (n ? (i + 1) % n : 0))
    else if (e.key === 'ArrowUp') setIndex((i) => (n ? (i - 1 + n) % n : 0))
    else if (e.key === 'PageDown') setIndex((i) => Math.min(n - 1, i + 10))
    else if (e.key === 'PageUp') setIndex((i) => Math.max(0, i - 10))
    else if (e.key === 'Tab') {
      const list = tabs()
      const i = list.findIndex(([id]) => id === shown())
      setTab(list[(i + (e.shiftKey ? -1 : 1) + list.length) % list.length][0])
    } else if (e.key === 'Enter') {
      const h = hits()[index()]
      if (h) close(h)
    } else if (e.key === 'Escape') close()
    else return
    e.preventDefault()
    e.stopPropagation()
  }

  // Double Shift: two presses of Shift alone, close together.
  onMount(() => {
    let last = 0
    let alone = false
    const down = (e: KeyboardEvent) => {
      alone = e.key === 'Shift' && !e.ctrlKey && !e.altKey && !e.metaKey && !e.repeat
      if (!alone) last = 0
    }
    const up = (e: KeyboardEvent) => {
      if (e.key !== 'Shift' || !alone || isCapturingKeys()) return
      const now = performance.now()
      if (now - last < 400) {
        last = 0
        if (!open()) searchEverywhere('all')
      } else last = now
    }
    window.addEventListener('keydown', down, true)
    window.addEventListener('keyup', up, true)
    onCleanup(() => {
      window.removeEventListener('keydown', down, true)
      window.removeEventListener('keyup', up, true)
    })
  })

  return (
    <Show when={open()}>
      <Portal>
        <div class="pick-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
          <div class="pick se" role="dialog" aria-label={t('Search everywhere')} onKeyDown={key}>
            <div class="se-tabs" role="tablist">
              <For each={tabs()}>
                {([id, label]) => (
                  <button role="tab" tabindex="-1" class="se-tab" aria-selected={shown() === id} classList={{ active: shown() === id }} onMouseDown={(e) => e.preventDefault()} onClick={() => setTab(id)}>
                    {t(label)}
                  </button>
                )}
              </For>
              <span class="grow" />
              <span class="se-help">{t('Tab: next tab')}</span>
            </div>
            <input ref={input} class="pick-input" placeholder={t(placeholderOf(shown()))} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
            <div class="pick-list" ref={list} role="listbox">
              <For each={groups()}>
                {(g) => (
                  <Show when={g.hits.length}>
                    <Show when={g.title}>
                      <div class="se-section">{t(g.title)}</div>
                    </Show>
                    <For each={g.hits}>
                      {(h) => {
                        const i = () => hits().indexOf(h)
                        return (
                          <div
                            class="pick-item"
                            role="option"
                            data-index={i()}
                            aria-selected={index() === i()}
                            classList={{ selected: index() === i() }}
                            onMouseEnter={() => setIndex(i())}
                            onMouseDown={(e) => {
                              e.preventDefault()
                              close(h)
                            }}
                          >
                            <Show when={h.icon}>
                              <span class="pick-icon">{h.icon!()}</span>
                            </Show>
                            <span class="pick-label" classList={{ mono: h.code }}>
                              <Marked text={h.label} ranges={h.ranges} />
                            </span>
                            <Show when={h.detail}>
                              <span class="pick-detail" classList={{ mono: h.path }}>{h.detail}</span>
                            </Show>
                            <Show when={h.hint}>
                              <kbd class="pick-hint">{h.hint}</kbd>
                            </Show>
                          </div>
                        )
                      }}
                    </For>
                  </Show>
                )}
              </For>
              <Show when={!hits().length}>
                <div class="pick-empty">{busy() ? t('Searching…') : query().trim().length < (searchProviders().find((p) => p.id === shown())?.minChars ?? 0) ? t('Type at least two characters') : t('No result')}</div>
              </Show>
            </div>
          </div>
        </div>
      </Portal>
    </Show>
  )
}
