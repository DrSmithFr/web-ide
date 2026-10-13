// The editor of a message box, shared by the assistant (Composer) and the Agents tool: a
// Markdown editor that grows with its content, Ctrl+S or Ctrl+Enter sends, completion of
// /commands (at the start) and @paths (anywhere), pasted files handed to the owner.
import { createEffect, createMemo, createResource, createSignal, For, on, onCleanup, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { fuzzy } from '../ui/overlay'
import { request } from '../pod/rpc'
import { relPath } from '../state/project'
import { registerAction } from '../keys/bindings'
import { Doc } from '../editor/doc'
import { EditorView } from '../editor/view'
import { settings } from '../state/settings'

export interface CompletionItem {
  kind: 'command' | 'skill' | 'file' | 'dir'
  label: string
  detail?: string
  insert: string
}

let pathsCache: { at: number; list: string[] } | null = null

/** Files and folders of the project, relative to its root (cached 15 s). */
export async function projectPaths(): Promise<string[]> {
  if (pathsCache && Date.now() - pathsCache.at < 15_000) return pathsCache.list
  const files = (await request<string[]>('search.files').catch(() => [])).map((f) => relPath(f))
  const dirs = new Set<string>()
  for (const f of files) {
    const parts = f.split('/')
    for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/') + '/')
  }
  const list = [...dirs, ...files]
  pathsCache = { at: Date.now(), list }
  return list
}

/** Inserts text at the caret, with a space before it when needed. */
export function insertAtCaret(v: EditorView, text: string) {
  if (!text) return
  const sel = v.getSelection()
  const from = Math.min(sel.anchor, sel.head)
  const before = v.doc.text.slice(0, from)
  v.edit(from, Math.max(sel.anchor, sel.head), (before && !/\s$/.test(before) ? ' ' : '') + text)
}

export function MessageBox(props: {
  draft: () => string
  setDraft: (text: string) => void
  placeholder: () => string
  /** Every /command offered (filtered here). */
  slashItems: () => CompletionItem[]
  onSubmit: () => void
  /** Keys seen after the completion and before the editor; true when consumed. */
  onKey?: (e: KeyboardEvent) => boolean
  /** Files pasted in the box. */
  onFiles: (files: File[]) => void
  onView?: (v: EditorView) => void
  testid?: string
}) {
  let host!: HTMLDivElement
  const [view, setView] = createSignal<EditorView>()
  // The editor edits its own Doc; the draft mirrors it both ways.
  const doc = new Doc('', props.draft(), { lang: 'markdown' })
  onCleanup(doc.onChange(() => props.setDraft(doc.text)))
  createEffect(
    on(props.draft, (text) => {
      if (text === doc.text) return
      doc.replace(0, doc.text.length, text, 'composer')
      const v = view()
      if (v?.hasFocus()) v.setSelection(text.length)
      else v?.restoreSelection({ anchor: text.length, head: text.length })
    }),
  )

  const [caret, setCaret] = createSignal(0)
  const [selIndex, setSelIndex] = createSignal(0)
  const [dismissed, setDismissed] = createSignal('')
  const query = createMemo(() => {
    const before = props.draft().slice(0, caret())
    let m = /^\/(\S*)$/.exec(before)
    if (m) return { mode: 'slash' as const, q: m[1], start: 0, end: caret() }
    m = /(^|\s)@([^\s@]*)$/.exec(before)
    if (m) return { mode: 'mention' as const, q: m[2], start: caret() - m[2].length - 1, end: caret() }
    return null
  })
  const [paths] = createResource(() => (query()?.mode === 'mention' ? true : null), projectPaths)
  const items = createMemo<CompletionItem[]>(() => {
    const qy = query()
    if (!qy) return []
    if (qy.mode === 'slash')
      return props
        .slashItems()
        .map((it) => ({ it, score: fuzzy(qy.q, it.label.slice(1)) }))
        .filter((x) => x.score > 0)
        .sort((a, b) => b.score - a.score)
        .map((x) => x.it)
    return (paths() ?? [])
      .map((p) => ({ p, score: fuzzy(qy.q, p) - (p.endsWith('/') ? 0.5 : 0) }))
      .filter((x) => x.score > 0)
      .sort((a, b) => b.score - a.score || a.p.length - b.p.length)
      .slice(0, 40)
      .map(({ p }) => ({ kind: p.endsWith('/') ? ('dir' as const) : ('file' as const), label: p, insert: p.endsWith('/') ? `@${p}` : `@${p} ` }))
  })
  const queryKey = () => (query() ? `${query()!.mode}:${query()!.start}` : '')
  const completionOpen = () => !!query() && items().length > 0 && dismissed() !== queryKey()
  createEffect(on(items, () => setSelIndex(0)))
  const accept = (it: CompletionItem) => {
    const qy = query()
    if (qy) view()?.edit(qy.start, qy.end, it.insert)
  }

  const onKey = (e: KeyboardEvent): boolean => {
    if (completionOpen()) {
      const n = items().length
      if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !e.shiftKey && !e.ctrlKey && !e.altKey) {
        setSelIndex((i) => (i + (e.key === 'ArrowDown' ? 1 : n - 1)) % n)
        return true
      }
      const it = items()[selIndex()]
      const qy = query()!
      // Enter on a token already complete (e.g. "/clear") goes to the next line.
      const complete = it.insert.trimEnd() === props.draft().slice(qy.start, qy.end)
      if ((e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !complete) || (e.key === 'Tab' && !e.shiftKey)) {
        accept(it)
        return true
      }
      if (e.key === 'Escape') {
        setDismissed(queryKey())
        return true
      }
    }
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && !e.isComposing) {
      props.onSubmit()
      return true
    }
    return props.onKey?.(e) ?? false
  }

  // Pasted files go to the owner (capture: before the editor pastes text).
  const onPaste = (e: ClipboardEvent) => {
    const files = [...(e.clipboardData?.files ?? [])]
    if (files.length) {
      e.preventDefault()
      e.stopPropagation()
      props.onFiles(files)
    }
  }

  onMount(() => {
    const v = new EditorView(doc, {
      tabSize: settings.editor.tabSize,
      insertSpaces: true,
      highlightLine: false,
      wrap: true,
      free: true,
      placeholder: props.placeholder(),
      onKey,
      onSelection: (sel) => setCaret(sel.head),
    })
    v.mount(host)
    host.addEventListener('paste', onPaste, true)
    setView(v)
    props.onView?.(v)
    onCleanup(() => v.destroy())
  })
  createEffect(() => view()?.setOptions({ placeholder: props.placeholder(), tabSize: settings.editor.tabSize }))
  // @path and /command in the text.
  createEffect(() => {
    const text = props.draft()
    const spans: [number, number][] = []
    const cmd = /^\/\S+/.exec(text)
    if (cmd) spans.push([0, cmd[0].length])
    for (const m of text.matchAll(/(^|\s)(@[^\s@]+)/g)) spans.push([m.index! + m[1].length, m.index! + m[0].length])
    view()?.setLiveRanges('ai-ref', spans)
  })
  // The editor actions of the editor tabs, on this box when it has the focus.
  const own = (f: (v: EditorView) => void) => () => {
    const v = view()
    if (!v?.hasFocus()) return false
    f(v)
  }
  const offs = [
    registerAction('file.save', own(() => props.onSubmit())),
    registerAction('edit.undo', own((v) => v.undo())),
    registerAction('edit.redo', own((v) => v.redo())),
    registerAction('edit.duplicateLine', own((v) => v.duplicateLine())),
    registerAction('edit.deleteLine', own((v) => v.deleteLine())),
    registerAction('edit.nextOccurrence', own((v) => v.addNextOccurrence())),
    registerAction('edit.unselectOccurrence', own((v) => v.removeLastOccurrence())),
    registerAction('edit.allOccurrences', own((v) => v.selectAllOccurrences())),
    registerAction('nav.subwordLeft', own((v) => v.moveSubword(-1, false))),
    registerAction('nav.subwordRight', own((v) => v.moveSubword(1, false))),
    registerAction('nav.subwordLeftSelect', own((v) => v.moveSubword(-1, true))),
    registerAction('nav.subwordRightSelect', own((v) => v.moveSubword(1, true))),
  ]
  onCleanup(() => offs.forEach((off) => off()))

  return (
    <>
      <Show when={completionOpen()}>
        <div class="ai-complete" role="listbox" data-testid="ai-complete">
          <For each={items()}>
            {(it, i) => (
              <div
                class="ai-complete-item"
                classList={{ active: i() === selIndex() }}
                role="option"
                onMouseDown={(e) => {
                  e.preventDefault()
                  accept(it)
                }}
                onMouseEnter={() => setSelIndex(i())}
                ref={(el) => createEffect(() => i() === selIndex() && el.scrollIntoView({ block: 'nearest' }))}
              >
                <Icon name={it.kind === 'dir' ? 'folder' : it.kind === 'file' ? 'file' : it.kind === 'skill' ? 'puzzle' : 'chevron'} size={13} />
                <span class="ai-complete-label">{it.label}</span>
                <Show when={it.detail}>
                  <span class="ai-complete-detail ellipsis">{it.detail}</span>
                </Show>
              </div>
            )}
          </For>
        </div>
      </Show>
      <div class="ai-editor" ref={host} data-testid={props.testid ?? 'ai-editor'} />
    </>
  )
}
