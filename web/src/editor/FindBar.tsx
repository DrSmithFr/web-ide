// Floating search bar of the editor (F3). Matches are computed once into an array of
// offsets; previous / next only walk that array. Rendering uses the Highlight API:
// "search-match" for all occurrences, "search-current" (higher priority) for the current one.
import { createEffect, createSignal, onCleanup, onMount, Show } from 'solid-js'
import type { Doc } from './doc'
import type { EditorView } from './view'
import { t } from '../i18n'

const MAX = 20000

export function FindBar(props: { view: EditorView; doc: Doc; initial: string; onClose: () => void; focusSignal: number }) {
  let input!: HTMLInputElement
  const [query, setQuery] = createSignal(props.initial)
  const [caseSensitive, setCase] = createSignal(false)
  const [word, setWord] = createSignal(false)
  const [regex, setRegex] = createSignal(false)
  const [matches, setMatches] = createSignal<[number, number][]>([])
  const [current, setCurrent] = createSignal(-1)
  const [error, setError] = createSignal('')
  const [version, setVersion] = createSignal(0)

  let timer: number | undefined
  const off = props.doc.onChange(() => {
    clearTimeout(timer)
    timer = window.setTimeout(() => setVersion((v) => v + 1), 150)
  })
  onCleanup(() => {
    off()
    clearTimeout(timer)
    props.view.setLiveRanges('search-match', [])
    props.view.setLiveRanges('search-current', [])
  })

  createEffect(() => {
    version()
    const q = query()
    setError('')
    if (!q) {
      setMatches([])
      setCurrent(-1)
      return
    }
    let re: RegExp
    try {
      let src = regex() ? q : q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
      if (word()) src = `\\b(?:${src})\\b`
      re = new RegExp(src, caseSensitive() ? 'gm' : 'gim')
    } catch (e) {
      setError((e as Error).message.replace(/^Invalid regular expression: /, ''))
      setMatches([])
      setCurrent(-1)
      return
    }
    const text = props.doc.text
    const out: [number, number][] = []
    let m: RegExpExecArray | null
    while ((m = re.exec(text)) && out.length < MAX) {
      if (m[0].length === 0) {
        re.lastIndex++
        continue
      }
      out.push([m.index, m.index + m[0].length])
    }
    setMatches(out)
    // Current occurrence: the first one after the caret.
    const caret = Math.min(props.view.getSelection().anchor, props.view.getSelection().head)
    const i = out.findIndex(([s]) => s >= caret)
    setCurrent(out.length ? (i < 0 ? 0 : i) : -1)
  })

  createEffect(() => {
    const ms = matches()
    const c = current()
    props.view.setLiveRanges('search-match', ms, 2)
    props.view.setLiveRanges('search-current', c >= 0 && ms[c] ? [ms[c]] : [], 3)
    if (c >= 0 && ms[c]) props.view.scrollToOffset(ms[c][0])
  })

  const go = (dir: 1 | -1) => {
    const n = matches().length
    if (!n) return
    setCurrent((c) => (c + dir + n) % n)
  }

  const close = (select: boolean) => {
    const m = matches()[current()]
    props.onClose()
    if (select && m) props.view.setSelection(m[0], m[1])
    else props.view.focus()
  }

  onMount(() => input.select())
  createEffect(() => {
    props.focusSignal
    queueMicrotask(() => input?.select())
  })

  const counter = () => {
    if (error()) return null
    const n = matches().length
    if (!query()) return ''
    if (!n) return t('No result')
    return `${current() + 1} / ${n}${n >= MAX ? '+' : ''}`
  }

  return (
    <div class="findbar" role="search" onMouseDown={(e) => e.stopPropagation()}>
      <input
        ref={input}
        class="find-input"
        placeholder={t('Find')}
        aria-label={t('Find in the file')}
        value={query()}
        onInput={(e) => setQuery(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            go(e.shiftKey ? -1 : 1)
          } else if (e.key === 'Escape') {
            e.preventDefault()
            e.stopPropagation()
            close(true)
          }
        }}
      />
      <button class="toggle" classList={{ on: caseSensitive() }} aria-pressed={caseSensitive()} title={t('Match case')} onClick={() => setCase(!caseSensitive())}>
        Aa
      </button>
      <button class="toggle" classList={{ on: word() }} aria-pressed={word()} title={t('Whole word')} onClick={() => setWord(!word())}>
        ab|
      </button>
      <button class="toggle" classList={{ on: regex() }} aria-pressed={regex()} title={t('Regular expression')} onClick={() => setRegex(!regex())}>
        .*
      </button>
      <Show when={error()} fallback={<span class="find-count">{counter()}</span>}>
        <span class="find-error" title={error()}>
          {error()}
        </span>
      </Show>
      <button class="icon-btn" title={t('Previous (Shift+Enter)')} disabled={!matches().length} onClick={() => go(-1)}>
        ↑
      </button>
      <button class="icon-btn" title={t('Next (Enter)')} disabled={!matches().length} onClick={() => go(1)}>
        ↓
      </button>
      <button class="icon-btn" title={t('Close (Esc)')} onClick={() => close(false)}>
        ✕
      </button>
    </div>
  )
}
