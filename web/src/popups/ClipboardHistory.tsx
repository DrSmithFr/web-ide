// Paste from history (Ctrl+Shift+V): the texts copied in the IDE, the most recent first, with
// a preview of the selected one. Enter pastes where the focus was; several entries selected
// (Shift+arrows, Ctrl or Shift+click) are pasted one per line. Delete forgets the selection.
import { batch, createEffect, createSignal, For, on, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import { clipEntries, forgetCopy, loadClipboard, recordCopy, type ClipEntry } from '../ui/clipboard'
import { fuzzy } from '../ui/overlay'
import { keepFocus } from '../state/focus'
import { t, tn } from '../i18n'
import './popups.css'

const [open, setOpen] = createSignal(false)

export function pasteFromHistory() {
  setOpen(true)
}

/** Pastes into an element as the keyboard would: a paste event, else an insertion in a field. */
function pasteInto(el: HTMLElement, text: string) {
  const data = new DataTransfer()
  data.setData('text/plain', text)
  const handled = !el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
  if (!handled && (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el.isContentEditable)) document.execCommand('insertText', false, text)
}

const firstLine = (s: string) => s.split('\n').find((l) => l.trim())?.trim() ?? ''

export function ClipboardHistoryHost() {
  let input!: HTMLInputElement
  let list!: HTMLDivElement
  let restoreFocus: () => HTMLElement | null = () => null
  const [query, setQuery] = createSignal('')
  const [index, setIndex] = createSignal(0)
  // Other selected entries (texts), with the current one: a range from the anchor or picked ones.
  const [picked, setPicked] = createSignal<Set<string>>(new Set())
  const [anchor, setAnchor] = createSignal(0)

  const shown = () => {
    const q = query().trim()
    return q ? clipEntries().filter((e) => fuzzy(q, e.text) > 0) : clipEntries()
  }
  const isSelected = (e: ClipEntry, i: number) => i === index() || picked().has(e.text)
  const selection = () => shown().filter(isSelected)

  const close = (text?: string) => {
    setOpen(false)
    const target = restoreFocus()
    if (text === undefined) return
    // The chosen text goes to the clipboard and to the top of the history.
    recordCopy(text)
    navigator.clipboard?.writeText(text).catch(() => {})
    if (target) pasteInto(target, text)
  }
  const accept = () => {
    const l = selection()
    if (l.length) close(l.length === 1 ? l[0].text : l.map((e) => e.text.replace(/\n$/, '')).join('\n'))
  }

  createEffect(
    on(open, (o) => {
      if (!o) return
      restoreFocus = keepFocus()
      batch(() => {
        setQuery('')
        setIndex(0)
        setAnchor(0)
        setPicked(new Set<string>())
      })
      void loadClipboard()
      queueMicrotask(() => input?.focus())
    }),
  )
  createEffect(
    on(
      query,
      () => {
        setIndex(0)
        setAnchor(0)
        setPicked(new Set<string>())
      },
      { defer: true },
    ),
  )
  createEffect(() => {
    if (!open()) return
    const n = shown().length
    if (index() >= n) setIndex(Math.max(0, n - 1))
    list?.querySelector(`[data-index="${index()}"]`)?.scrollIntoView({ block: 'nearest' })
  })

  /** Moves to i; with extend, the selection becomes the range from the anchor. */
  const moveTo = (i: number, extend: boolean) => {
    const l = shown()
    if (!l.length) return
    i = Math.max(0, Math.min(l.length - 1, i))
    if (extend) {
      const [a, b] = [Math.min(anchor(), i), Math.max(anchor(), i)]
      setPicked(new Set(l.slice(a, b + 1).map((e) => e.text)))
    } else {
      setAnchor(i)
      setPicked(new Set<string>())
    }
    setIndex(i)
  }

  const key = (e: KeyboardEvent) => {
    const n = shown().length
    if (e.key === 'ArrowDown') moveTo(e.shiftKey ? index() + 1 : (index() + 1) % Math.max(1, n), e.shiftKey)
    else if (e.key === 'ArrowUp') moveTo(e.shiftKey ? index() - 1 : (index() - 1 + n) % Math.max(1, n), e.shiftKey)
    else if (e.key === 'PageDown') moveTo(index() + 10, e.shiftKey)
    else if (e.key === 'PageUp') moveTo(index() - 10, e.shiftKey)
    else if (e.key === 'Enter') accept()
    else if (e.key === 'Escape') close()
    else if (e.key === 'Delete' && !query()) {
      for (const x of selection()) void forgetCopy(x.text).catch(() => {})
      setPicked(new Set<string>())
    } else return
    e.preventDefault()
    e.stopPropagation()
  }

  const click = (e: MouseEvent, i: number, entry: ClipEntry) => {
    e.preventDefault()
    if (e.shiftKey) return moveTo(i, true)
    if (e.ctrlKey || e.metaKey) {
      const s = new Set(picked())
      const cur = shown()[index()]
      if (cur) s.add(cur.text)
      if (s.has(entry.text) && s.size > 1) s.delete(entry.text)
      else s.add(entry.text)
      setPicked(s)
      setIndex(i)
      setAnchor(i)
      return
    }
    moveTo(i, false)
    accept()
  }

  const preview = () => {
    const l = selection()
    return l.length > 1 ? l.map((e) => e.text.replace(/\n$/, '')).join('\n') : (shown()[index()]?.text ?? '')
  }

  return (
    <Show when={open()}>
      <Portal>
        <div class="pick-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
          <div class="pick se cb" role="dialog" aria-label={t('Paste from history')} onKeyDown={key}>
            <div class="rf-head">
              <span class="rf-title">{t('Paste from history')}</span>
              <input ref={input} class="rf-filter" placeholder={t('Filter')} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
            </div>
            <div class="pick-list" ref={list} role="listbox" aria-multiselectable="true">
              <For each={shown()} fallback={<div class="pick-empty">{query() ? t('No result') : t('Nothing copied yet')}</div>}>
                {(entry, i) => {
                  const lines = entry.text.replace(/\n$/, '').split('\n').length
                  return (
                    <div
                      class="pick-item"
                      role="option"
                      data-index={i()}
                      aria-selected={isSelected(entry, i())}
                      classList={{ selected: i() === index(), picked: isSelected(entry, i()) && i() !== index() }}
                      onMouseDown={(e) => click(e, i(), entry)}
                    >
                      <span class="pick-label mono">{firstLine(entry.text) || t('(whitespace)')}</span>
                      <span class="pick-detail">{lines > 1 ? tn(lines, '{n} line', '{n} lines') : ''}</span>
                      <span class="cb-time">{new Date(entry.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
                    </div>
                  )
                }}
              </For>
            </div>
            <pre class="cb-preview" data-testid="clipboard-preview">{preview()}</pre>
            <div class="rf-foot">{t('Enter: paste · Shift+arrows: select several · Delete: forget')}</div>
          </div>
        </div>
      </Portal>
    </Show>
  )
}
