// The view of a file buffer, the default view of the file tabs (fileView contribution): the
// editor, its find bar, rendered Markdown, the change markers against HEAD, the completion, the
// context menu and the editing actions while it is the active one.
import { createEffect, createResource, createSignal, on, onCleanup, onMount, Show, untrack } from 'solid-js'
import type { Diagnostic, Doc, EditorView, LspDiagnostic, MarkdownViewApi, TabState } from '../api'
import {
  actionOf, api, basename, capability, contextMenu, copyText, docError, docsVersion, FindBar, getDoc, Icon, isMarkdownPath, languageName,
  lineMarks, loadDoc, MarkdownView, openConflict, openFile, pick, prompt, registerAction, relPath, request, reveal, runAction, saveCursor,
  saveDoc, setActivePane, setCursorInfo, shortcutOf, showRaw, shownRaw, t, toast, toggleWrap, useCompletion, useEditorView,
} from './plugin'

function toDiagnostics(doc: Doc, list: LspDiagnostic[]): Diagnostic[] {
  return (list as any[]).map((d) => ({
    from: doc.offset(d.range.start.line, d.range.start.character),
    to: doc.offset(d.range.end.line, d.range.end.character),
    severity: d.severity === 1 ? 'error' : d.severity === 2 ? 'warning' : 'info',
    message: d.message,
    source: d.source,
  }))
}

export function FileEditor(props: { tab: TabState; paneId: string }) {
  const path = props.tab.path!
  let host!: HTMLDivElement
  const [hostEl, setHostEl] = createSignal<HTMLElement>()
  const [doc, setDoc] = createSignal<Doc | null>(getDoc(path))
  const [findOpen, setFindOpen] = createSignal(false)
  const [findInitial, setFindInitial] = createSignal('')
  const [findFocus, setFindFocus] = createSignal(0)
  const error = () => (docsVersion(), docError(path))

  onMount(() => {
    setHostEl(host)
    loadDoc(path).then((d) => setDoc(d))
  })

  const isActive = () => api.panes.isActive(props.paneId, props.tab.id)

  // Markdown: rendered by default, the source on demand, at about the same place.
  const markdown = isMarkdownPath(path)
  const rendered = () => markdown && !shownRaw(props.tab.id) && !!doc() && !error()
  let md: MarkdownViewApi | undefined
  const toggleMarkdown = (caretLine?: number) => {
    const v = view()
    if (!v) return
    if (rendered()) {
      const top = md?.topLine() ?? 0
      showRaw(props.tab.id, true)
      if (caretLine !== undefined) v.setSelection(v.doc.offset(caretLine, 0), undefined, false)
      v.scrollToLine(top)
      requestAnimationFrame(() => v.focus())
    } else {
      const top = v.visibleLines()[0]
      showRaw(props.tab.id, false)
      requestAnimationFrame(() => {
        md?.scrollToLine(top)
        md?.focus()
      })
    }
  }

  // The completion needs the view, the view forwards keys and keystrokes to the completion.
  let completion: ReturnType<typeof useCompletion> | null = null
  const view = useEditorView(doc, hostEl, {
    onKey: (e) => completion?.onKey(e) ?? false,
    onType: (t) => completion?.onType(t),
    onSelection: (v) => {
      completion?.onSelection()
      const sel = v.getSelection()
      save(v)
      const { line, col } = v.doc.pos(sel.head)
      setCursorInfo({ line: line + 1, col: col + 1, sel: Math.abs(sel.head - sel.anchor), lang: languageName(v.doc.lang) })
    },
    onFolds: (v) => save(v),
    onCtrlClick: (v, off) => capability('language')?.gotoDeclaration(v.doc, off),
    onFocus: () => setActivePane(props.paneId),
  })
  // Cursor, folds and chosen indentation of the file, kept in the session.
  function save(v: EditorView) {
    const sel = v.getSelection()
    const indent = v.doc.indentChosen ? (v.doc.indent() ?? undefined) : undefined
    saveCursor(path, { anchor: sel.anchor, head: sel.head, scroll: v.scroller.scrollTop, folds: v.foldedLines(), indent })
  }
  createEffect(
    on(
      () => doc()?.indent(),
      () => {
        const v = view()
        if (v && v.doc.indentChosen) save(v)
      },
      { defer: true },
    ),
  )
  const comp = useCompletion(view, doc)
  completion = comp

  // Restore the cursor and scroll of the file, then focus when this pane is the active one.
  createEffect(
    on(view, (v) => {
      if (!v) return
      const c = untrack(() => api.buffers.cursor(path))
      if (c) {
        if (c.indent && !v.doc.indentChosen) v.doc.chooseIndent(c.indent)
        if (c.folds?.length) v.restoreFolds(c.folds)
        v.scroller.scrollTop = c.scroll
        if (untrack(isActive)) v.setSelection(c.anchor, c.head, false)
        else v.restoreSelection({ anchor: c.anchor, head: c.head })
      } else if (untrack(isActive)) v.setSelection(0, 0, false)
      const { line, col } = v.doc.pos(c?.head ?? 0)
      if (untrack(isActive)) setCursorInfo({ line: line + 1, col: col + 1, sel: 0, lang: languageName(v.doc.lang) })
    }),
  )

  // Jumps requested by navigation (goto, search results, outline).
  createEffect(() => {
    const r = reveal()
    const v = view()
    if (!r || !v || r.path !== path || !untrack(isActive)) return
    untrack(() => {
      if (r.seq > revealSeen && rendered()) showRaw(props.tab.id, true)
      revealSeen = Math.max(revealSeen, r.seq)
      const t = r.target
      const from = t.offset ?? v.doc.offset(t.line ?? 0, t.col ?? 0)
      v.setSelection(from, t.end ?? from, false)
      v.scrollToOffset(from, true)
    })
  })

  createEffect(() => {
    const v = view()
    const d = doc()
    if (!v || !d) return
    d.changed()
    v.setDiagnostics(toDiagnostics(d, capability('language')?.diagnostics(path) ?? []))
  })

  // Change markers against HEAD, recomputed a moment after the edits.
  const [head] = createResource(
    () => (capability('git')?.status()?.repo ? { rev: capability('git')!.revision(), d: doc() } : null),
    async ({ d }) => {
      if (!d || d.readOnly) return null
      try {
        return (await capability('git')?.show(path, 'HEAD')) ?? null
      } catch {
        return null
      }
    },
  )
  let markTimer: number | undefined
  createEffect(() => {
    const v = view()
    const d = doc()
    const h = head()
    if (!v || !d) return
    d.changed()
    clearTimeout(markTimer)
    if (!h) return v.setLineMarks(new Map())
    const untracked = capability('git')?.status()?.files.some((f) => f.path === path && f.untracked)
    markTimer = window.setTimeout(() => {
      if (!h.exists) v.setLineMarks(untracked ? new Map(Array.from({ length: d.lineCount }, (_, i) => [i, 'add' as const])) : new Map())
      else v.setLineMarks(lineMarks(h.content.replace(/\r\n/g, '\n'), d.text))
    }, 250)
  })
  onCleanup(() => clearTimeout(markTimer))

  // Context menu of the text: code navigation, clipboard, comment, file.
  const editorMenu = (e: MouseEvent) => {
    const v = view()
    const d = doc()
    if (!v || !d || !(e.target as HTMLElement).closest('.ed')) return
    const content = host.querySelector<HTMLElement>('.ed-content')
    // An action of a plugin that is off (the language servers) is not offered.
    const item = (id: string, disabled = false) => {
      const a = actionOf(id)
      return a ? [{ label: t(a.label), hint: shortcutOf(id), disabled, action: () => void runAction(id) }] : []
    }
    const sep = { separator: true, label: '' }
    const paste = async () => {
      const data = new DataTransfer()
      data.setData('text/plain', await navigator.clipboard.readText().catch(() => ''))
      content?.focus()
      content?.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
    }
    contextMenu(e, [
      ...item('lsp.definition'),
      ...item('lsp.references'),
      ...item('lsp.implementation'),
      ...item('lsp.rename', d.readOnly),
      ...item('lsp.format', d.readOnly),
      sep,
      { label: t('Cut'), hint: 'Ctrl+X', disabled: d.readOnly, action: () => document.execCommand('cut') },
      { label: t('Copy'), hint: 'Ctrl+C', action: () => document.execCommand('copy') },
      { label: t('Paste'), hint: 'Ctrl+V', disabled: d.readOnly, action: () => void paste() },
      ...item('edit.pasteHistory', d.readOnly),
      sep,
      ...item('edit.toggleComment', d.readOnly),
      ...item('edit.duplicateLine', d.readOnly),
      sep,
      {
        label: t('Show in the explorer'),
        disabled: relPath(path) === path,
        action: () => {
          api.showTool('explorer')
          capability('files')?.reveal(path, true)
        },
      },
      { label: t('Copy the relative path'), action: () => copyText(relPath(path)) },
    ])
  }
  onCleanup(api.editor.register(() => (view() && doc() && isActive() ? { view: view()!, doc: doc()! } : null)))
  const when = (f: (v: EditorView, d: Doc) => void, needFocus = false) => () => {
    const v = view()
    const d = doc()
    if (!v || !d || !isActive() || (needFocus && !v.hasFocus())) return false
    // Another editor has the focus (the message box of the assistant): its own actions run.
    const other = api.editor.View.of(document.activeElement)
    if (other && other !== v) return false
    f(v, d)
  }
  const offs: (() => void)[] = [
    registerAction('file.save', when((_, d) => saveDoc(d))),
    registerAction('edit.undo', when((v) => v.undo(), true)),
    registerAction('edit.redo', when((v) => v.redo(), true)),
    registerAction('edit.duplicateLine', when((v) => v.duplicateLine(), true)),
    registerAction('edit.deleteLine', when((v) => v.deleteLine(), true)),
    registerAction('edit.toggleComment', when((v) => v.toggleComment(), true)),
    registerAction('edit.nextOccurrence', when((v) => v.addNextOccurrence(), true)),
    registerAction('edit.unselectOccurrence', when((v) => v.removeLastOccurrence(), true)),
    registerAction('edit.allOccurrences', when((v) => v.selectAllOccurrences(), true)),
    registerAction('edit.fold', when((v) => v.fold(), true)),
    registerAction('edit.unfold', when((v) => v.unfold(), true)),
    registerAction('edit.foldAll', when((v) => v.foldAll(), true)),
    registerAction('edit.unfoldAll', when((v) => v.unfoldAll(), true)),
    registerAction('view.wordWrap', when((_, d) => toggleWrap(d))),
    registerAction('view.markdown', () => (markdown && isActive() ? toggleMarkdown() : false)),
    registerAction('nav.subwordLeft', when((v) => v.moveSubword(-1, false), true)),
    registerAction('nav.subwordRight', when((v) => v.moveSubword(1, false), true)),
    registerAction('nav.subwordLeftSelect', when((v) => v.moveSubword(-1, true), true)),
    registerAction('nav.subwordRightSelect', when((v) => v.moveSubword(1, true), true)),
    registerAction('search.find', when((v) => {
      if (rendered()) toggleMarkdown()
      if (findOpen() && !v.hasFocus()) {
        setFindOpen(false)
        v.focus()
        return
      }
      const sel = v.selectedText()
      setFindInitial(sel && !sel.includes('\n') ? sel : findInitial())
      setFindOpen(true)
      setFindFocus((n) => n + 1)
    })),
    registerAction('conflict.resolve', when((_, d) => {
      if (d.conflict()) openConflict(d)
    })),
    registerAction('edit.complete', when(() => completion?.open(), true)),
    registerAction('nav.gotoLine', when(async (v, d) => {
      const { line, col } = d.pos(v.getSelection().head)
      const s = await prompt({ title: t('Go to line'), label: t('Line[:column] (1 – {n})', { n: d.lineCount }), value: `${line + 1}:${col + 1}` })
      if (!s) return
      const [l, c] = s.split(':').map((x) => parseInt(x, 10))
      if (!l) return
      const off = d.offset(l - 1, (c || 1) - 1)
      v.setSelection(off)
      v.scrollToOffset(off, true)
    })),
    registerAction('nav.test', when(async (_, d) => {
      const r = await request('fs.related', { path: d.path })
      const list: string[] = r.isTest ? r.sources : r.tests
      if (!list.length) {
        toast(r.isTest ? t('Source of the test not found') : t('No test found for this file'), 'info')
        return
      }
      if (list.length === 1) return void openFile(list[0])
      const p = await pick({ placeholder: r.isTest ? t('Tested sources') : t('Tests'), pathDetail: true, items: list.map((x) => ({ label: basename(x), detail: relPath(x), value: x })) })
      if (p) openFile(p)
    })),
    registerAction('nav.related', when(async (_, d) => {
      const r = await request('fs.related', { path: d.path })
      const items = [
        ...r.tests.map((x: string) => ({ label: basename(x), detail: relPath(x), hint: 'test', value: x })),
        ...r.sources.map((x: string) => ({ label: basename(x), detail: relPath(x), hint: 'source', value: x })),
        ...r.related.map((x: string) => ({ label: basename(x), detail: relPath(x), value: x })),
      ]
      if (!items.length) {
        toast(t('No related file'), 'info')
        return
      }
      const p = await pick<string>({ placeholder: t('Related symbols'), items })
      if (p) openFile(p)
    })),
  ]
  onCleanup(() => offs.forEach((f) => f()))

  const keepMine = () => {
    const d = doc()
    const c = d?.conflict()
    if (!d || !c) return
    d.setBase(c.remote, c.rev)
    d.setConflict(null)
    toast(t('Your version is kept (save to overwrite the file)'), 'info')
  }
  const takeTheirs = () => {
    const d = doc()
    const c = d?.conflict()
    if (!d || !c) return
    d.setText(c.remote, 'remote')
    d.setBase(c.remote, c.rev)
    d.setConflict(null)
  }

  return (
    <div class="file-editor">
      <Show when={doc()?.conflict()}>
        <div class="banner banner-conflict" role="alert">
          <span>{t('The file changed on disk while you were editing it, and the automatic merge failed. Your version is kept.')}</span>
          <button class="btn small primary" onClick={() => openConflict(doc()!)}>
            {t('Resolve')} ({shortcutOf('conflict.resolve')})
          </button>
          <button class="btn small" onClick={keepMine}>
            {t('Keep my version')}
          </button>
          <button class="btn small" onClick={takeTheirs}>
            {t('Take the new version')}
          </button>
        </div>
      </Show>
      <Show when={doc()?.deleted()}>
        <div class="banner banner-warn">{t('This file was deleted from the disk. Saving it creates it again.')}</div>
      </Show>
      <Show when={doc()?.readOnly}>
        <div class="banner banner-info">{t('Read-only: file outside the project ({path})', { path })}</div>
      </Show>
      <Show when={error()}>
        <div class="empty-pane">{error() === 'binary' ? t('Binary or too large file: no preview.') : t('Cannot read: {error}', { error: error() ?? '' })}</div>
      </Show>
      <div class="editor-host" onContextMenu={editorMenu}>
        {/* The view is mounted by hand: it gets its own node, never touched by Solid. */}
        <div class="editor-mount" ref={host} inert={rendered()} />
        <Show when={rendered()}>
          <MarkdownView doc={doc()!} path={path} api={(a) => (md = a)} onEdit={(line) => toggleMarkdown(line)} />
        </Show>
        <Show when={markdown && doc() && !error() && !findOpen()}>
          <button
            class="icon-btn md-toggle"
            classList={{ on: !rendered() }}
            title={`${rendered() ? t('Show the source') : t('Show the rendering')} (${shortcutOf('view.markdown')})`}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => toggleMarkdown()}
            data-testid="md-toggle"
          >
            <Icon name={rendered() ? 'code' : 'eye'} size={16} />
          </button>
        </Show>
        <Show when={findOpen() && view() && doc()}>
          <FindBar view={view()!} doc={doc()!} initial={findInitial()} focusSignal={findFocus()} onClose={() => setFindOpen(false)} />
        </Show>
        <comp.Popup />
      </div>
    </div>
  )
}

/** Reveals handled: an old one does not show the source of a Markdown file opened again. */
let revealSeen = 0

/** Read-only text (DDL, definitions, merge markers). */
