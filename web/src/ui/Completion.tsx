// Completion list of an editor. It opens on the trigger characters of the language server,
// after a short pause in an identifier, or with Ctrl+Space; it filters while typing and is
// driven from the keyboard without leaving the editor.
import { createEffect, createSignal, For, on, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import type { Doc } from '../editor/doc'
import type { EditorView } from '../editor/view'
import {
  applyItem, complete, completionKinds, docText, filterItems, isWordChar, resolveItem, triggerCharacters, wordStart, type CompletionItem,
} from '../lsp/completion'
import { toast } from './toast'

interface State {
  items: CompletionItem[]
  list: CompletionItem[]
  index: number
  from: number
  incomplete: boolean
  resolve: boolean
  left: number
  top: number
}

export function useCompletion(view: () => EditorView | null, doc: () => Doc | null) {
  const [state, setState] = createSignal<State | null>(null)
  const [detail, setDetail] = createSignal<CompletionItem | null>(null)
  let ctrl: AbortController | null = null
  let timer: number | undefined
  let triggers: string[] = []
  createEffect(
    on(doc, (d) => {
      triggers = []
      if (d) triggerCharacters(d).then((t) => (triggers = t)).catch(() => {})
    }),
  )

  const close = () => {
    ctrl?.abort()
    clearTimeout(timer)
    setState(null)
    setDetail(null)
  }

  const caret = () => view()?.getSelection().head ?? 0

  async function open(trigger: string | null, manual: boolean) {
    const v = view()
    const d = doc()
    if (!v || !d || v.readOnly) return
    ctrl?.abort()
    const c = new AbortController()
    ctrl = c
    const at = caret()
    const from = trigger ? at : wordStart(d, at)
    try {
      const res = await complete(d, at, trigger, c.signal)
      if (c.signal.aborted) return
      const now = caret()
      const prefix = d.text.slice(from, now)
      // The user kept typing meanwhile: only show the answer if it still applies.
      if (now < from || d.lineAt(now) !== d.lineAt(from) || ![...prefix].every((ch) => isWordChar(ch, d.lang))) return
      const list = filterItems(res.items, prefix)
      if (!list.length) {
        if (manual) toast('Aucune proposition', 'info', undefined, 1500)
        setState(null)
        return
      }
      const pos = v.coordsAt(from)
      setState({ items: res.items, list, index: 0, from, incomplete: res.incomplete, resolve: res.resolve, left: pos.left, top: pos.bottom + 2 })
    } catch (e) {
      if (!c.signal.aborted && manual) toast((e as Error).message, 'info')
    }
  }

  function refilter() {
    const s = state()
    const d = doc()
    if (!s || !d) return
    const now = caret()
    if (now < s.from || d.lineAt(now) !== d.lineAt(s.from)) return close()
    const prefix = d.text.slice(s.from, now)
    if (s.incomplete) {
      clearTimeout(timer)
      timer = window.setTimeout(() => open(null, false), 120)
    }
    const list = filterItems(s.items, prefix)
    if (!list.length) return close()
    setState({ ...s, list, index: 0 })
  }

  async function accept(i: number) {
    const s = state()
    const v = view()
    const d = doc()
    if (!s || !v || !d) return
    let item = s.list[i]
    close()
    if (s.resolve && !item.local) item = await resolveItem(d, item)
    const sel = applyItem(d, item, s.from, caret(), v)
    v.setSelection(sel.anchor, sel.head)
  }

  // Documentation of the selected item, resolved lazily.
  let resolveTimer: number | undefined
  createEffect(() => {
    const s = state()
    const d = doc()
    clearTimeout(resolveTimer)
    if (!s || !d) return
    const item = s.list[s.index]
    setDetail(item)
    if (s.resolve && !item.local && !item.documentation) {
      resolveTimer = window.setTimeout(async () => {
        const r = await resolveItem(d, item)
        if (state()?.list[state()!.index] === item) setDetail(r)
      }, 150)
    }
  })

  const onKey = (e: KeyboardEvent): boolean => {
    const s = state()
    if (!s) return false
    const n = s.list.length
    const move = (i: number) => setState({ ...s, index: (i + n) % n })
    switch (e.key) {
      case 'ArrowDown':
        move(s.index + 1)
        return true
      case 'ArrowUp':
        move(s.index - 1)
        return true
      case 'PageDown':
        setState({ ...s, index: Math.min(n - 1, s.index + 8) })
        return true
      case 'PageUp':
        setState({ ...s, index: Math.max(0, s.index - 8) })
        return true
      case 'Enter':
      case 'Tab':
        if (e.shiftKey || e.ctrlKey || e.altKey) return false
        accept(s.index)
        return true
      case 'Escape':
        close()
        return true
    }
    return false
  }

  const onType = (text: string) => {
    const d = doc()
    if (!d) return
    const last = text.slice(-1)
    const word = text !== '' && [...text].every((ch) => isWordChar(ch, d.lang))
    if (state()) {
      if (text === '' || word) return refilter()
      close()
    }
    clearTimeout(timer)
    if (!word) ctrl?.abort()
    if (!text) return
    // Two-character triggers ("->", "::") are announced by their last character.
    if (triggers.includes(last) || triggers.includes(d.text.slice(caret() - 2, caret()))) {
      timer = window.setTimeout(() => open(last, false), 30)
      return
    }
    if (isWordChar(last, d.lang)) timer = window.setTimeout(() => open(null, false), 150)
  }

  const onSelection = () => {
    const s = state()
    const d = doc()
    if (!s || !d) return
    const now = caret()
    if (now < s.from || d.lineAt(now) !== d.lineAt(s.from)) close()
  }

  function Popup() {
    let list!: HTMLDivElement
    createEffect(() => {
      const i = state()?.index ?? 0
      list?.children[i]?.scrollIntoView({ block: 'nearest' })
    })
    return (
      <Show when={state()}>
        {(s) => (
          <Portal>
            <div
              class="completion"
              style={{ left: `${Math.min(s().left, innerWidth - 460)}px`, top: `${s().top + 240 > innerHeight ? s().top - 260 : s().top}px` }}
              onMouseDown={(e) => e.preventDefault()}
            >
              <div class="completion-list" ref={list} role="listbox">
                <For each={s().list}>
                  {(it, i) => (
                    <div
                      class="completion-item"
                      role="option"
                      aria-selected={i() === s().index}
                      classList={{ selected: i() === s().index }}
                      // Only a real move selects: the list may open under a still pointer.
                      onMouseMove={(e) => (e.movementX || e.movementY) && i() !== s().index && setState({ ...s(), index: i() })}
                      onClick={() => accept(i())}
                    >
                      <span class={`completion-kind kind-${it.kind ?? 1}`} title={completionKinds[it.kind ?? 1]?.[0]}>
                        {completionKinds[it.kind ?? 1]?.[1] ?? '·'}
                      </span>
                      <span class="completion-label">
                        {it.label}
                        <Show when={it.labelDetails?.detail}>
                          <span class="muted">{it.labelDetails!.detail}</span>
                        </Show>
                      </span>
                      <span class="completion-detail">{it.labelDetails?.description ?? (it.local ? 'mot du fichier' : it.detail ?? '')}</span>
                    </div>
                  )}
                </For>
              </div>
              <Show when={detail() && (detail()!.detail || docText(detail()!))}>
                <div class="completion-doc">
                  <Show when={detail()!.detail}>
                    <div class="mono small">{detail()!.detail}</div>
                  </Show>
                  <Show when={docText(detail()!)}>
                    <div class="completion-doc-text">{docText(detail()!)}</div>
                  </Show>
                </div>
              </Show>
            </div>
          </Portal>
        )}
      </Show>
    )
  }

  return { open: () => open(null, true), close, onKey, onType, onSelection, Popup, isOpen: () => !!state() }
}
