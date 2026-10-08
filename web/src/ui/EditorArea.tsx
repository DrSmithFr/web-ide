// Editor area: recursive split tree (split/right, split/down), tab bar per pane, and the
// content of each tab (file editor, SQL console, table view, read-only text).
import { createEffect, createMemo, createResource, createSignal, For, Match, on, onCleanup, onMount, Show, Switch, untrack } from 'solid-js'
import {
  activateTab, basename, closeTab, docError, findLeaf, getDoc, loadDoc, moveTab, openFile, relPath, reveal, saveCursor,
  saveDoc, session, setActivePane, setSplitSizes, splitPane, diagnostics, type LayoutNode, type TabState, docsVersion, mutate,
} from '../state/project'
import { showTool } from '../state/zones'
import { revealInExplorer } from '../panels/Explorer'
import { EditorView, type Diagnostic } from '../editor/view'
import { hintAt, lockEditor, phone, unlocked } from '../state/mobile'
import { Icon } from './icons'
import { Doc } from '../editor/doc'
import { FindBar } from '../editor/FindBar'
import { grammarGeneration, languageName } from '../editor/languages'
import { settings } from '../state/settings'
import { actions, registerAction, runAction, shortcutOf } from '../keys/bindings'
import { contextMenu, pick, prompt } from './overlay'
import { openConflict } from '../conflict/ConflictDialog'
import { request } from '../pod/rpc'
import * as lspc from '../lsp/client'
import { toast } from './toast'
import { SqlConsole } from '../db/SqlConsole'
import { TableView } from '../db/TableView'
import { setCursorInfo } from './status'
import { StatusBar } from './StatusBar'
import { useCompletion } from './Completion'
import { DiffView } from './DiffView'
import { Board } from '../kanban/Board'
import { TicketView } from '../kanban/TicketView'
import { summary } from '../kanban/state'
import { gitRevision, gitStatus } from '../state/git'
import { lineMarks } from '../editor/linediff'
import { formatDocument, renameSymbol } from '../lsp/refactor'
import { t } from '../i18n'
import { dropClasses, dropIndex, setDropAt } from './tabDrop'
import { focusPart } from '../state/focus'
import { copyText } from './clipboard'
import { WorktreeChip } from './WorktreeChip'

export function EditorArea(props: { detached?: boolean }) {
  return (
    <div class="editor-area">
      <div class="editor-panes">
        <Node node={session.layout} detached={props.detached} />
      </div>
      <StatusBar />
    </div>
  )
}

function Node(props: { node: LayoutNode; detached?: boolean }) {
  return (
    <Switch>
      <Match when={props.node.type === 'leaf' && props.node}>{(leaf) => <Pane id={leaf().id} />}</Match>
      <Match when={props.node.type === 'split' && props.node}>{(split) => <Split node={split()} />}</Match>
    </Switch>
  )
}

function Split(props: { node: Extract<LayoutNode, { type: 'split' }> }) {
  let el!: HTMLDivElement
  const drag = (i: number, e: PointerEvent) => {
    e.preventDefault()
    const rect = el.getBoundingClientRect()
    const row = props.node.dir === 'row'
    const total = row ? rect.width : rect.height
    const start = row ? e.clientX : e.clientY
    const sizes = [...props.node.sizes]
    const move = (ev: PointerEvent) => {
      const d = ((row ? ev.clientX : ev.clientY) - start) / total
      const a = Math.max(0.08, sizes[i] + d)
      const b = Math.max(0.08, sizes[i] + sizes[i + 1] - a)
      const next = [...sizes]
      next[i] = sizes[i] + sizes[i + 1] - b
      next[i + 1] = b
      setSplitSizes(props.node.id, next)
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }
  return (
    <div ref={el} class={`split split-${props.node.dir}`}>
      <For each={props.node.children}>
        {(child, i) => (
          <>
            <Show when={i() > 0}>
              <div class="split-handle" onPointerDown={(e) => drag(i() - 1, e)} />
            </Show>
            <div class="split-cell" style={{ flex: `${props.node.sizes[i()] ?? 1} 1 0` }}>
              <Node node={child} />
            </div>
          </>
        )}
      </For>
    </div>
  )
}

let dragged: { pane: string; tab: string } | null = null

function tabTitle(t: TabState) {
  if (t.kind === 'file') return basename(t.path ?? '')
  if (t.kind === 'ticket') {
    const s = summary(t.ticket!)
    return s ? `#${s.id} ${s.title.length > 28 ? s.title.slice(0, 27) + '…' : s.title}` : `#${t.ticket}`
  }
  return t.title ?? t.kind
}

function Pane(props: { id: string }) {
  const leaf = () => findLeaf(session.layout, props.id)
  const active = () => {
    const l = leaf()
    return l?.active ? session.tabs[l.active] : undefined
  }
  const isActivePane = () => session.activePane === props.id

  const tabMenu = (e: MouseEvent, tab: TabState) =>
    contextMenu(e, [
      { label: t('Close'), hint: shortcutOf('view.closeTab'), action: () => closeTab(props.id, tab.id) },
      { label: t('Close the others'), action: () => leaf()?.tabs.filter((x) => x !== tab.id).forEach((x) => closeTab(props.id, x)) },
      { label: t('Close all'), action: () => [...(leaf()?.tabs ?? [])].forEach((x) => closeTab(props.id, x)) },
      { separator: true, label: '' },
      { label: t('Split right'), hint: shortcutOf('view.splitRight'), action: () => (activateTab(props.id, tab.id), splitPane(props.id, 'row')) },
      { label: t('Split down'), hint: shortcutOf('view.splitDown'), action: () => (activateTab(props.id, tab.id), splitPane(props.id, 'col')) },
      ...(tab.kind === 'file'
        ? [
            { separator: true, label: '' },
            { label: t('Copy the path'), action: () => copyText(tab.path!) },
            { label: t('Copy the relative path'), action: () => copyText(relPath(tab.path!)) },
          ]
        : []),
    ])

  return (
    <div class="pane" classList={{ active: isActivePane(), focused: isActivePane() && focusPart() === 'editor' }} onMouseDown={() => setActivePane(props.id)}>
      <div
        class="tabbar"
        role="tablist"
        onDragOver={(e) => {
          if (!dragged) return
          e.preventDefault()
          if (e.target === e.currentTarget || (e.target as HTMLElement).classList.contains('tabbar-fill')) setDropAt({ bar: props.id, index: leaf()?.tabs.length ?? 0 })
        }}
        onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDropAt(null)}
        onDrop={(e) => {
          if (!dragged) return
          e.preventDefault()
          moveTab(dragged.pane, dragged.tab, props.id, leaf()?.tabs.length ?? 0)
          dragged = null
          setDropAt(null)
        }}
      >
        <For each={leaf()?.tabs ?? []}>
          {(id, i) => {
            const tab = () => session.tabs[id]
            const doc = createMemo(() => {
              docsVersion()
              const cur = tab()
              return cur?.kind === 'file' && cur.path ? getDoc(cur.path) : null
            })
            return (
              <Show when={tab()}>
                <div
                  class="tab"
                  role="tab"
                  aria-selected={leaf()?.active === id}
                  classList={{ active: leaf()?.active === id, dirty: !!doc()?.dirty(), conflict: !!doc()?.conflict(), readonly: !!doc()?.readOnly, ...dropClasses(props.id, i(), leaf()?.tabs.length ?? 0) }}
                  title={tab().kind === 'file' ? relPath(tab().path!) : tab().title}
                  draggable="true"
                  onDragStart={() => (dragged = { pane: props.id, tab: id })}
                  onDragEnd={() => ((dragged = null), setDropAt(null))}
                  onDragOver={(e) => {
                    if (!dragged) return
                    e.preventDefault()
                    e.stopPropagation()
                    setDropAt({ bar: props.id, index: dropIndex(e, i()) })
                  }}
                  onDrop={(e) => {
                    if (!dragged) return
                    e.preventDefault()
                    e.stopPropagation()
                    moveTab(dragged.pane, dragged.tab, props.id, dropIndex(e, i()))
                    dragged = null
                    setDropAt(null)
                  }}
                  onMouseDown={(e) => {
                    if (e.button === 1) {
                      e.preventDefault()
                      closeTab(props.id, id)
                    } else if (e.button === 0) {
                      // A click shows the tab and gives the keyboard to its content (the tab keeps
                      // the focus only from the keyboard).
                      activateTab(props.id, id)
                      requestAnimationFrame(() => document.querySelector<HTMLElement>('.pane.active .ed-content')?.focus())
                    }
                  }}
                  onKeyDown={(e) => {
                    if (e.key !== 'Enter' && e.key !== ' ') return
                    e.preventDefault()
                    activateTab(props.id, id)
                  }}
                  onContextMenu={(e) => tabMenu(e, tab())}
                >
                  <span class={`tab-kind kind-${tab().kind}`} />
                  <span class="tab-title">{tabTitle(tab())}</span>
                  <Show when={tab().kind === 'file'}>
                    <WorktreeChip path={tab().path} />
                  </Show>
                  <Show when={doc()?.conflict()}>
                    <span class="tab-badge conflict" title={t('In conflict')}>!</span>
                  </Show>
                  <button
                    class="tab-close"
                    title={t('Close')}
                    onMouseDown={(e) => e.stopPropagation()}
                    onClick={(e) => {
                      e.stopPropagation()
                      closeTab(props.id, id)
                    }}
                  >
                    <span class="dot" />
                    <span class="x">✕</span>
                  </button>
                </div>
              </Show>
            )
          }}
        </For>
        <div class="tabbar-fill" />
        <button class="icon-btn small" title={`${t('Split right')} (${shortcutOf('view.splitRight')})`} onClick={() => splitPane(props.id, 'row')}>
          ◫
        </button>
      </div>
      <div class="pane-body">
        <Show when={phone() && isActivePane() && active()?.kind === 'file'}>
          <LockLayer />
        </Show>
        <Show when={active()} keyed fallback={<EmptyPane />}>
          {(t) => (
            <Switch>
              <Match when={t.kind === 'file'}>
                <FileEditor tab={t} paneId={props.id} />
              </Match>
              <Match when={t.kind === 'sql'}>
                <SqlConsole tab={t} paneId={props.id} />
              </Match>
              <Match when={t.kind === 'table'}>
                <TableView tab={t} paneId={props.id} />
              </Match>
              <Match when={t.kind === 'text'}>
                <TextViewer tab={t} paneId={props.id} />
              </Match>
              <Match when={t.kind === 'diff'}>
                <DiffView tab={t} paneId={props.id} />
              </Match>
              <Match when={t.kind === 'kanban'}>
                <Board />
              </Match>
              <Match when={t.kind === 'ticket'}>
                <TicketView tab={t} paneId={props.id} />
              </Match>
            </Switch>
          )}
        </Show>
      </div>
    </div>
  )
}

function EmptyPane() {
  return (
    <div class="empty-pane">
      <div class="empty-grid">
        <span>{t('Go to file')}</span>
        <kbd>{shortcutOf('nav.gotoFile')}</kbd>
        <span>{t('Search in the project')}</span>
        <kbd>{shortcutOf('search.global')}</kbd>
        <span>{t('Command palette')}</span>
        <kbd>{shortcutOf('palette.open')}</kbd>
        <span>{t('New terminal')}</span>
        <kbd>{shortcutOf('console.new')}</kbd>
        <span>{t('Settings')}</span>
        <kbd>{shortcutOf('settings.open')}</kbd>
      </div>
    </div>
  )
}

/** Creates an EditorView for a Doc inside a Solid component, following the settings. */
export interface UseViewOptions {
  readOnly?: boolean
  onSelection?: (v: EditorView) => void
  onCtrlClick?: (v: EditorView, offset: number) => void
  onFocus?: () => void
  onKey?: (e: KeyboardEvent) => boolean
  onType?: (text: string) => void
  onFolds?: (v: EditorView) => void
}

/** Tab size and kind of indentation: those of the file when known, else the settings. */
function indentOptions(d: Doc) {
  const i = d.indent()
  return {
    tabSize: i && !i.tabs ? i.size : settings.editor.tabSize,
    insertSpaces: i ? !i.tabs : settings.editor.insertSpaces,
  }
}

export function useEditorView(doc: () => Doc | null, host: () => HTMLElement | undefined, opts: UseViewOptions = {}) {
  const [view, setView] = createSignal<EditorView | null>(null)
  createEffect(
    on([doc, host], ([d, h]) => {
      if (!d || !h) return
      const v = new EditorView(d, {
        ...untrack(() => indentOptions(d)),
        highlightLine: untrack(() => settings.editor.highlightLine),
        indentGuides: untrack(() => settings.editor.indentGuides),
        showWhitespace: untrack(() => settings.editor.showWhitespace),
        readOnly: opts.readOnly,
        // Untracked: a callback run inside an effect (setSelection from a jump) must not
        // subscribe that effect to what the callback reads.
        onSelection: () => untrack(() => opts.onSelection?.(v)),
        onCtrlClick: (off) => untrack(() => opts.onCtrlClick?.(v, off)),
        onFocus: () => untrack(() => opts.onFocus?.()),
        onKey: (e) => untrack(() => opts.onKey?.(e) ?? false),
        onType: (t) => untrack(() => opts.onType?.(t)),
        onFolds: () => untrack(() => opts.onFolds?.(v)),
      })
      v.mount(h)
      setView(v)
      onCleanup(() => {
        v.destroy()
        setView(null)
      })
    }),
  )
  createEffect(() => {
    const v = view()
    if (!v) return
    v.setOptions({
      ...indentOptions(v.doc),
      highlightLine: settings.editor.highlightLine,
      indentGuides: settings.editor.indentGuides,
      showWhitespace: settings.editor.showWhitespace,
    })
  })
  createEffect(
    on(
      () => [settings.font.family, settings.font.size, settings.font.lineHeight],
      () => requestAnimationFrame(() => view()?.measure()),
      { defer: true },
    ),
  )
  createEffect(on(grammarGeneration, () => view()?.refreshGrammar(), { defer: true }))
  return view
}

function toDiagnostics(doc: Doc, list: lspc.Range[] | any[]): Diagnostic[] {
  return (list as any[]).map((d) => ({
    from: doc.offset(d.range.start.line, d.range.start.character),
    to: doc.offset(d.range.end.line, d.range.end.character),
    severity: d.severity === 1 ? 'error' : d.severity === 2 ? 'warning' : 'info',
    message: d.message,
    source: d.source,
  }))
}

function FileEditor(props: { tab: TabState; paneId: string }) {
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

  const isActive = () => session.activePane === props.paneId && findLeaf(session.layout, props.paneId)?.active === props.tab.id

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
    onCtrlClick: (v, off) => lspc.gotoDeclaration(v.doc, off),
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
      const c = untrack(() => session.cursors[path])
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
    v.setDiagnostics(toDiagnostics(d, diagnostics[path] ?? []))
  })

  // Change markers against HEAD, recomputed a moment after the edits.
  const [head] = createResource(
    () => (gitStatus()?.repo ? { rev: gitRevision(), d: doc() } : null),
    async ({ d }) => {
      if (!d || d.readOnly) return null
      try {
        return await request<{ content: string; exists: boolean }>('git.show', { path, rev: 'HEAD' })
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
    const untracked = gitStatus()?.files.some((f) => f.path === path && f.untracked)
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
    const item = (id: string, disabled = false) => ({ label: t(actions.find((a) => a.id === id)!.label), hint: shortcutOf(id), disabled, action: () => void runAction(id) })
    const sep = { separator: true, label: '' }
    const paste = async () => {
      const data = new DataTransfer()
      data.setData('text/plain', await navigator.clipboard.readText().catch(() => ''))
      content?.focus()
      content?.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
    }
    contextMenu(e, [
      item('lsp.definition'),
      item('lsp.references'),
      item('lsp.implementation'),
      item('lsp.rename', d.readOnly),
      item('lsp.format', d.readOnly),
      sep,
      { label: t('Cut'), hint: 'Ctrl+X', disabled: d.readOnly, action: () => document.execCommand('cut') },
      { label: t('Copy'), hint: 'Ctrl+C', action: () => document.execCommand('copy') },
      { label: t('Paste'), hint: 'Ctrl+V', disabled: d.readOnly, action: () => void paste() },
      item('edit.pasteHistory', d.readOnly),
      sep,
      item('edit.toggleComment', d.readOnly),
      item('edit.duplicateLine', d.readOnly),
      sep,
      {
        label: t('Show in the explorer'),
        disabled: relPath(path) === path,
        action: () => {
          mutate((s) => showTool(s, 'explorer'))
          revealInExplorer(path, true)
        },
      },
      { label: t('Copy the relative path'), action: () => copyText(relPath(path)) },
    ])
  }
  const when = (f: (v: EditorView, d: Doc) => void, needFocus = false) => () => {
    const v = view()
    const d = doc()
    if (!v || !d || !isActive() || (needFocus && !v.hasFocus())) return false
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
    registerAction('nav.subwordLeft', when((v) => v.moveSubword(-1, false), true)),
    registerAction('nav.subwordRight', when((v) => v.moveSubword(1, false), true)),
    registerAction('nav.subwordLeftSelect', when((v) => v.moveSubword(-1, true), true)),
    registerAction('nav.subwordRightSelect', when((v) => v.moveSubword(1, true), true)),
    registerAction('search.find', when((v) => {
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
    registerAction('lsp.definition', when((v, d) => lspc.gotoDeclaration(d, v.getSelection().head))),
    registerAction('lsp.implementation', when((v, d) => lspc.gotoImplementation(d, v.getSelection().head))),
    registerAction('lsp.typeDefinition', when((v, d) => lspc.gotoTypeDefinition(d, v.getSelection().head))),
    registerAction('lsp.superMethod', when((v, d) => lspc.gotoSuperMethod(d, v.getSelection().head))),
    registerAction('lsp.references', when((v, d) => lspc.findReferences(d, v.getSelection().head))),
    registerAction('edit.complete', when(() => completion?.open(), true)),
    registerAction('lsp.rename', when((v, d) => renameSymbol(v, d))),
    registerAction('lsp.format', when((v, d) => formatDocument(v, d))),
    registerAction('lsp.hover', when(async (v, d) => {
      const off = v.getSelection().head
      try {
        const text = await lspc.hoverText(d, off)
        const c = v.coordsAt(off)
        if (text) v.showTooltip(c.left, c.bottom - 8, text, 'doc')
        else toast(t('No documentation here'), 'info')
      } catch (e) {
        toast((e as Error).message, 'info')
      }
    })),
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
    registerAction('nav.fileStructure', when(async (v, d) => {
      try {
        const symbols = lspc.flatten(await lspc.documentSymbols(d.path))
        const chosen = await pick({
          placeholder: t('Structure of {name}', { name: basename(d.path) }),
          items: symbols.map(({ s, depth }) => ({
            label: '  '.repeat(depth) + s.name,
            detail: s.detail,
            icon: lspc.symbolKinds[s.kind]?.[1],
            value: s,
          })),
        })
        if (chosen) {
          const off = d.offset(chosen.selectionRange.start.line, chosen.selectionRange.start.character)
          v.setSelection(off)
          v.scrollToOffset(off, true)
        }
      } catch (e) {
        toast((e as Error).message, 'info')
      }
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
        <div class="editor-mount" ref={host} />
        <Show when={findOpen() && view() && doc()}>
          <FindBar view={view()!} doc={doc()!} initial={findInitial()} focusSignal={findFocus()} onClose={() => setFindOpen(false)} />
        </Show>
        <comp.Popup />
      </div>
    </div>
  )
}

/** Read-only text (DDL, definitions, merge markers). */
function TextViewer(props: { tab: TabState; paneId: string }) {
  let host!: HTMLDivElement
  const [hostEl, setHostEl] = createSignal<HTMLElement>()
  const doc = new Doc(`text:${props.tab.id}`, props.tab.text ?? '', { readOnly: true, lang: props.tab.lang ?? 'plaintext' })
  onMount(() => setHostEl(host))
  useEditorView(() => doc, hostEl, { readOnly: true, onFocus: () => setActivePane(props.paneId) })
  return (
    <div class="file-editor">
      <div class="editor-host" ref={host} />
    </div>
  )
}


/** Phone: "Double-tap to edit" a moment after a tap on the locked editor, or the padlock that
 *  locks it again (state/mobile.ts). */
function LockLayer() {
  const [hint, setHint] = createSignal(false)
  createEffect(
    on(
      hintAt,
      () => {
        setHint(true)
        const id = setTimeout(() => setHint(false), 1400)
        onCleanup(() => clearTimeout(id))
      },
      { defer: true },
    ),
  )
  return (
    <>
      <Show when={hint() && !unlocked()}>
        <div class="ed-lock-hint" data-testid="ed-lock-hint">
          <span>{t('Double-tap to edit')}</span>
        </div>
      </Show>
      <Show when={unlocked()}>
        <button class="ed-lock-btn" title={t('Lock the editing')} onClick={lockEditor} data-testid="ed-lock">
          <Icon name="lock" size={16} />
        </button>
      </Show>
    </>
  )
}
