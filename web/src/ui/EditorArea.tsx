// Editor area: recursive split tree (split/right, split/down), tab bar per pane, and the
// content of each tab (file editor, SQL console, table view, read-only text).
import { createEffect, createMemo, createResource, createSignal, For, Match, on, onCleanup, onMount, Show, Switch, untrack } from 'solid-js'
import {
  activateTab, basename, closeTab, docError, findLeaf, getDoc, loadDoc, moveTab, openFile, relPath, reveal, saveCursor,
  saveDoc, session, setActivePane, setSplitSizes, splitPane, diagnostics, type LayoutNode, type TabState, docsVersion,
} from '../state/project'
import { EditorView, type Diagnostic } from '../editor/view'
import { Doc } from '../editor/doc'
import { FindBar } from '../editor/FindBar'
import { grammarGeneration, languageName } from '../editor/languages'
import { settings } from '../state/settings'
import { registerAction, shortcutOf } from '../keys/bindings'
import { contextMenu, pick, prompt } from './overlay'
import { openConflict } from '../conflict/ConflictDialog'
import { request } from '../pod/rpc'
import * as lspc from '../lsp/client'
import { toast } from './toast'
import { SqlConsole } from '../db/SqlConsole'
import { TableView } from '../db/TableView'
import { setCursorInfo } from './status'
import { useCompletion } from './Completion'
import { DiffView } from './DiffView'
import { Board } from '../kanban/Board'
import { TicketView } from '../kanban/TicketView'
import { summary } from '../kanban/state'
import { gitRevision, gitStatus } from '../state/git'
import { lineMarks } from '../editor/linediff'
import { formatDocument, renameSymbol } from '../lsp/refactor'

export function EditorArea(props: { detached?: boolean }) {
  return (
    <div class="editor-area">
      <Node node={session.layout} detached={props.detached} />
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

  const tabMenu = (e: MouseEvent, t: TabState) =>
    contextMenu(e, [
      { label: 'Fermer', hint: shortcutOf('view.closeTab'), action: () => closeTab(props.id, t.id) },
      { label: 'Fermer les autres', action: () => leaf()?.tabs.filter((x) => x !== t.id).forEach((x) => closeTab(props.id, x)) },
      { label: 'Tout fermer', action: () => [...(leaf()?.tabs ?? [])].forEach((x) => closeTab(props.id, x)) },
      { separator: true, label: '' },
      { label: 'Diviser à droite', hint: shortcutOf('view.splitRight'), action: () => (activateTab(props.id, t.id), splitPane(props.id, 'row')) },
      { label: 'Diviser en bas', hint: shortcutOf('view.splitDown'), action: () => (activateTab(props.id, t.id), splitPane(props.id, 'col')) },
      ...(t.kind === 'file'
        ? [
            { separator: true, label: '' },
            { label: 'Copier le chemin', action: () => navigator.clipboard.writeText(t.path!) },
            { label: 'Copier le chemin relatif', action: () => navigator.clipboard.writeText(relPath(t.path!)) },
          ]
        : []),
    ])

  return (
    <div class="pane" classList={{ active: isActivePane() }} onMouseDown={() => setActivePane(props.id)}>
      <div
        class="tabbar"
        role="tablist"
        onDragOver={(e) => dragged && e.preventDefault()}
        onDrop={(e) => {
          if (!dragged) return
          e.preventDefault()
          moveTab(dragged.pane, dragged.tab, props.id, leaf()?.tabs.length ?? 0)
          dragged = null
        }}
      >
        <For each={leaf()?.tabs ?? []}>
          {(id, i) => {
            const t = () => session.tabs[id]
            const doc = createMemo(() => {
              docsVersion()
              const tab = t()
              return tab?.kind === 'file' && tab.path ? getDoc(tab.path) : null
            })
            return (
              <Show when={t()}>
                <div
                  class="tab"
                  role="tab"
                  aria-selected={leaf()?.active === id}
                  classList={{ active: leaf()?.active === id, dirty: !!doc()?.dirty(), conflict: !!doc()?.conflict(), readonly: !!doc()?.readOnly }}
                  title={t().kind === 'file' ? relPath(t().path!) : t().title}
                  draggable
                  onDragStart={() => (dragged = { pane: props.id, tab: id })}
                  onDragOver={(e) => dragged && e.preventDefault()}
                  onDrop={(e) => {
                    if (!dragged) return
                    e.preventDefault()
                    e.stopPropagation()
                    moveTab(dragged.pane, dragged.tab, props.id, i())
                    dragged = null
                  }}
                  onMouseDown={(e) => {
                    if (e.button === 1) {
                      e.preventDefault()
                      closeTab(props.id, id)
                    } else if (e.button === 0) activateTab(props.id, id)
                  }}
                  onContextMenu={(e) => tabMenu(e, t())}
                >
                  <span class={`tab-kind kind-${t().kind}`} />
                  <span class="tab-title">{tabTitle(t())}</span>
                  <Show when={doc()?.conflict()}>
                    <span class="tab-badge conflict" title="En conflit">!</span>
                  </Show>
                  <button
                    class="tab-close"
                    title="Fermer"
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
        <button class="icon-btn small" title={`Diviser à droite (${shortcutOf('view.splitRight')})`} onClick={() => splitPane(props.id, 'row')}>
          ◫
        </button>
      </div>
      <div class="pane-body">
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
        <span>Aller au fichier</span>
        <kbd>{shortcutOf('nav.gotoFile')}</kbd>
        <span>Rechercher dans le projet</span>
        <kbd>{shortcutOf('search.global')}</kbd>
        <span>Palette de commandes</span>
        <kbd>{shortcutOf('palette.open')}</kbd>
        <span>Nouveau terminal</span>
        <kbd>{shortcutOf('console.new')}</kbd>
        <span>Réglages</span>
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
}

export function useEditorView(doc: () => Doc | null, host: () => HTMLElement | undefined, opts: UseViewOptions = {}) {
  const [view, setView] = createSignal<EditorView | null>(null)
  createEffect(
    on([doc, host], ([d, h]) => {
      if (!d || !h) return
      const v = new EditorView(d, {
        tabSize: untrack(() => settings.editor.tabSize),
        insertSpaces: untrack(() => settings.editor.insertSpaces),
        highlightLine: untrack(() => settings.editor.highlightLine),
        readOnly: opts.readOnly,
        // Untracked: a callback run inside an effect (setSelection from a jump) must not
        // subscribe that effect to what the callback reads.
        onSelection: () => untrack(() => opts.onSelection?.(v)),
        onCtrlClick: (off) => untrack(() => opts.onCtrlClick?.(v, off)),
        onFocus: () => untrack(() => opts.onFocus?.()),
        onKey: (e) => untrack(() => opts.onKey?.(e) ?? false),
        onType: (t) => untrack(() => opts.onType?.(t)),
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
    v.setOptions({ tabSize: settings.editor.tabSize, insertSpaces: settings.editor.insertSpaces, highlightLine: settings.editor.highlightLine })
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
      saveCursor(path, { anchor: sel.anchor, head: sel.head, scroll: v.scroller.scrollTop })
      const { line, col } = v.doc.pos(sel.head)
      setCursorInfo({ line: line + 1, col: col + 1, sel: Math.abs(sel.head - sel.anchor), lang: languageName(v.doc.lang) })
    },
    onCtrlClick: (v, off) => lspc.gotoDeclaration(v.doc, off),
    onFocus: () => setActivePane(props.paneId),
  })
  const comp = useCompletion(view, doc)
  completion = comp

  // Restore the cursor and scroll of the file, then focus when this pane is the active one.
  createEffect(
    on(view, (v) => {
      if (!v) return
      const c = untrack(() => session.cursors[path])
      if (c) {
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
      else v.setLineMarks(lineMarks(h.content, d.text))
    }, 250)
  })
  onCleanup(() => clearTimeout(markTimer))

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
        else toast('Pas de documentation à cet endroit', 'info')
      } catch (e) {
        toast((e as Error).message, 'info')
      }
    })),
    registerAction('nav.gotoLine', when(async (v, d) => {
      const { line, col } = d.pos(v.getSelection().head)
      const s = await prompt({ title: 'Aller à la ligne', label: `Ligne[:colonne] (1 – ${d.lineCount})`, value: `${line + 1}:${col + 1}` })
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
          placeholder: `Structure de ${basename(d.path)}`,
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
        toast(r.isTest ? 'Source du test introuvable' : 'Aucun test trouvé pour ce fichier', 'info')
        return
      }
      if (list.length === 1) return void openFile(list[0])
      const p = await pick({ placeholder: r.isTest ? 'Sources testées' : 'Tests', items: list.map((x) => ({ label: basename(x), detail: relPath(x), value: x })) })
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
        toast('Aucun fichier lié', 'info')
        return
      }
      const p = await pick<string>({ placeholder: 'Symboles liés', items })
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
    toast('Votre version est conservée (enregistrer pour écraser le fichier)', 'info')
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
          <span>Le fichier a changé sur le disque pendant vos modifications, et la fusion automatique a échoué. Votre version est conservée.</span>
          <button class="btn small primary" onClick={() => openConflict(doc()!)}>
            Résoudre ({shortcutOf('conflict.resolve')})
          </button>
          <button class="btn small" onClick={keepMine}>
            Garder ma version
          </button>
          <button class="btn small" onClick={takeTheirs}>
            Prendre la nouvelle version
          </button>
        </div>
      </Show>
      <Show when={doc()?.deleted()}>
        <div class="banner banner-warn">Ce fichier a été supprimé du disque. L'enregistrer le recrée.</div>
      </Show>
      <Show when={doc()?.readOnly}>
        <div class="banner banner-info">Lecture seule : fichier hors du projet ({path})</div>
      </Show>
      <Show when={error()}>
        <div class="empty-pane">{error() === 'binary' ? 'Fichier binaire ou trop volumineux : pas d’aperçu.' : `Lecture impossible : ${error()}`}</div>
      </Show>
      <div class="editor-host">
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

