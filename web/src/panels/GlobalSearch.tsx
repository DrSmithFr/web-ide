// Global search in the project (run by the pod: local walk, or grep on the SSH host).
import { createEffect, createSignal, For, on, onCleanup, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { openFile, relPath } from '../state/project'
import { registerAction } from '../keys/bindings'

interface Match {
  path: string
  line: number
  col: number
  text: string
  ranges: [number, number][]
}

const [focusSeq, setFocusSeq] = createSignal(0)
export function focusGlobalSearch() {
  setFocusSeq((n) => n + 1)
}

export function GlobalSearch() {
  let input!: HTMLInputElement
  const [query, setQuery] = createSignal('')
  const [caseSensitive, setCase] = createSignal(false)
  const [word, setWord] = createSignal(false)
  const [regex, setRegex] = createSignal(false)
  const [include, setInclude] = createSignal('')
  const [results, setResults] = createSignal<Match[]>([])
  const [info, setInfo] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  const [collapsed, setCollapsed] = createSignal<Set<string>>(new Set())
  let ctrl: AbortController | null = null
  let timer: number | undefined

  createEffect(on(focusSeq, () => queueMicrotask(() => input?.select())))

  createEffect(() => {
    const q = query()
    const opts = { query: q, regex: regex(), caseSensitive: caseSensitive(), wholeWord: word(), include: include(), max: 2000 }
    clearTimeout(timer)
    ctrl?.abort()
    if (!q) {
      setResults([])
      setInfo('')
      return
    }
    timer = window.setTimeout(async () => {
      const c = new AbortController()
      ctrl = c
      setBusy(true)
      try {
        const r = await request('search.grep', opts, c.signal)
        setResults(r.matches)
        const files = new Set(r.matches.map((m: Match) => m.path)).size
        setInfo(`${r.matches.length}${r.truncated ? '+' : ''} résultat(s) dans ${files} fichier(s)`)
      } catch (e) {
        if (!c.signal.aborted) setInfo((e as Error).message)
      } finally {
        if (!c.signal.aborted) setBusy(false)
      }
    }, 300)
  })
  onCleanup(() => ctrl?.abort())

  const groups = () => {
    const m = new Map<string, Match[]>()
    for (const r of results()) {
      const l = m.get(r.path) ?? []
      l.push(r)
      m.set(r.path, l)
    }
    return [...m.entries()]
  }

  const off = registerAction('search.global', () => {
    focusGlobalSearch()
    return false
  })
  onCleanup(off)

  const highlight = (m: Match) => {
    const parts: { t: string; hit: boolean }[] = []
    let pos = 0
    for (const [s, e] of m.ranges ?? []) {
      if (s > pos) parts.push({ t: m.text.slice(pos, s), hit: false })
      parts.push({ t: m.text.slice(s, e), hit: true })
      pos = e
    }
    parts.push({ t: m.text.slice(pos), hit: false })
    return parts
  }

  return (
    <div class="panel search-panel">
      <div class="panel-head">
        <span class="panel-title">Recherche</span>
      </div>
      <div class="search-form">
        <div class="search-row">
          <input ref={input} class="input" placeholder="Rechercher dans le projet" value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
          <button class="toggle" classList={{ on: caseSensitive() }} title="Respecter la casse" onClick={() => setCase(!caseSensitive())}>
            Aa
          </button>
          <button class="toggle" classList={{ on: word() }} title="Mot entier" onClick={() => setWord(!word())}>
            ab|
          </button>
          <button class="toggle" classList={{ on: regex() }} title="Expression régulière (syntaxe RE2)" onClick={() => setRegex(!regex())}>
            .*
          </button>
        </div>
        <input class="input small" placeholder="Fichiers : *.go, *.ts" value={include()} onInput={(e) => setInclude(e.currentTarget.value)} />
        <div class="muted small">{busy() ? 'Recherche…' : info()}</div>
      </div>
      <div class="panel-body search-results">
        <For each={groups()}>
          {([path, list]) => (
            <div class="search-group">
              <div
                class="search-file"
                onClick={() => {
                  const s = new Set(collapsed())
                  s.has(path) ? s.delete(path) : s.add(path)
                  setCollapsed(s)
                }}
              >
                <span class="tree-twist" classList={{ open: !collapsed().has(path) }}>
                  ›
                </span>
                <span class="search-path">{relPath(path)}</span>
                <span class="badge">{list.length}</span>
              </div>
              <Show when={!collapsed().has(path)}>
                <For each={list}>
                  {(m) => (
                    <div
                      class="search-hit"
                      onClick={() => openFile({ path: m.path, line: m.line - 1, col: m.col })}
                    >
                      <span class="search-line">{m.line}</span>
                      <span class="search-text">
                        <For each={highlight(m)}>{(p) => (p.hit ? <mark>{p.t}</mark> : <>{p.t}</>)}</For>
                      </span>
                    </div>
                  )}
                </For>
              </Show>
            </div>
          )}
        </For>
      </div>
    </div>
  )
}
