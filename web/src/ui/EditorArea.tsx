// Editor area: recursive split tree (split/right, split/down), tab bar per pane, and the
// content of each tab (file editor, SQL console, table view, read-only text).
import { createEffect, createMemo, createSignal, For, Match, on, onCleanup, onMount, Show, Switch, untrack } from 'solid-js'
import { pluginFileView, pluginView, pluginViewLabel } from '../plugins/registry'
import { activateTab, basename, closeTab, findLeaf, getDoc, moveTab, relPath, session, setActivePane, setSplitSizes, splitPane, type LayoutNode, type TabState, docsVersion, moveTabToDesk } from '../state/project'
import { distinctDirs } from './tabNames'
import { EditorView } from '../editor/view'
import { hintAt, lockEditor, phone, unlocked } from '../state/mobile'
import { Icon } from './icons'
import { Doc } from '../editor/doc'
import { grammarGeneration } from '../editor/languages'
import { fontFor, settings } from '../state/settings'
import { shortcutOf } from '../keys/bindings'
import { contextMenu, type MenuItem } from './overlay'
import { toast } from './toast'
import { StatusBar } from './StatusBar'
import { DiffView } from './DiffView'
import { Board } from '../kanban/Board'
import { TicketView } from '../kanban/TicketView'
import { MilestoneView } from '../kanban/MilestoneView'
import { ObjectiveView } from '../kanban/ObjectiveView'
import { board, summary } from '../kanban/state'
import { t } from '../i18n'
import { dropClasses, dropIndex, setDropAt } from './tabDrop'
import { focusPart } from '../state/focus'
import { copyText } from './clipboard'
import { WorktreeChip } from './WorktreeChip'
import { addDesk, desk, deskLabel, desks } from '../state/desks'
import { route } from '../app/router'
import { detachTab, hiddenTools, tileMode, nextSplit, resizing, scrollMode, tileTools } from '../state/tiles'
import { ScrollArea } from './ScrollArea'
import { capability } from '../plugins/capabilities'
import { Dynamic } from 'solid-js/web'
import { DocView } from './DocView'

export function EditorArea(props: { detached?: boolean; statusBar?: boolean }) {
  return (
    <div class="editor-area">
      <div class="editor-panes">
        <Show when={scrollMode() && !props.detached} fallback={<Node node={session.layout} detached={props.detached} />}>
          <ScrollArea cell={(c) => <Node node={c} />} />
        </Show>
      </div>
      {/* Only the phone keeps the bar inside the editor pane (pages/ProjectPage.tsx). */}
      <Show when={props.statusBar}>
        <StatusBar />
      </Show>
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
    <div ref={el} class={`split split-${props.node.dir}`} data-split={props.node.id}>
      <For each={props.node.children}>
        {(child, i) => (
          <>
            {/* A hidden pane of tools (Full mode) takes no room, nor its handle. */}
            <Show when={i() > 0 && !hiddenTools(child) && props.node.children.slice(0, i()).some((c) => !hiddenTools(c))}>
              <div class="split-handle" onPointerDown={(e) => drag(i() - 1, e)} />
            </Show>
            <div class="split-cell" classList={{ hidden: hiddenTools(child) }} style={{ flex: `${props.node.sizes[i()] ?? 1} 1 0` }}>
              <Node node={child} />
            </div>
          </>
        )}
      </For>
    </div>
  )
}

let dragged: { pane: string; tab: string } | null = null
/** The tab being dragged (a desk of the project bar takes it). */
export const draggedTab = () => dragged

/** Items of the tab menu sending the tab to another desk, or to a new one. */
function deskItems(pane: string, tab: string): MenuItem[] {
  const send = (n: number) => void moveTabToDesk(pane, tab, n).catch((e) => toast((e as Error).message, 'warn'))
  return [
    { separator: true, label: '' },
    ...desks.desks.filter((d) => d.n !== desk()).map((d) => ({ label: t('Move to desk {name}', { name: deskLabel(d) }), hint: shortcutOf(`desk.send.${d.n}`), action: () => send(d.n) })),
    { label: t('Move to a new desk'), action: () => void addDesk().then(send) },
  ]
}

function tabTitle(tab: TabState) {
  if (tab.kind === 'file') return basename(tab.path ?? '')
  if (tab.kind === 'ticket') {
    const s = summary(tab.ticket!)
    return s ? `#${s.id} ${s.title.length > 28 ? s.title.slice(0, 27) + '…' : s.title}` : `#${tab.ticket}`
  }
  if (tab.kind === 'milestone') {
    const m = board.milestones.find((x) => x.id === tab.milestone)
    return m ? (m.title.length > 32 ? m.title.slice(0, 31) + '…' : m.title) : t('Milestone')
  }
  if (tab.kind === 'objective') {
    const o = board.objectives.find((x) => x.id === tab.objective)
    return o ? (o.title.length > 32 ? o.title.slice(0, 31) + '…' : o.title) : t('Objective')
  }
  if (tab.kind === 'tool') return tileTools[tab.tool!] ? t(tileTools[tab.tool!].label) : tab.tool!
  if (tab.kind === 'console') return capability('terminal')?.consoles().find((c) => c.id === tab.console)?.title ?? t('Console')
  if (tab.kind === 'doc') return tab.title ?? t('Documentation')
  if (tab.kind.startsWith('plugin:')) return tab.title ?? t(pluginViewLabel(tab.kind) ?? tab.kind)
  return tab.title ?? tab.kind
}

function Pane(props: { id: string }) {
  const leaf = () => findLeaf(session.layout, props.id)
  const active = () => {
    const l = leaf()
    return l?.active ? session.tabs[l.active] : undefined
  }
  const isActivePane = () => session.activePane === props.id
  // File tabs of the window sharing a name: their folders (see tabNames.ts).
  const dirs = createMemo(() => distinctDirs(Object.values(session.tabs).flatMap((x) => (x.kind === 'file' && x.path ? [relPath(x.path)] : []))))
  let tabbar!: HTMLDivElement
  // The active tab stays in sight (a new one, first, when the bar is scrolled).
  createEffect(
    on(
      () => leaf()?.active,
      () => requestAnimationFrame(() => tabbar.querySelector('.tab.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })),
    ),
  )

  const tabMenu = (e: MouseEvent, tab: TabState) =>
    contextMenu(e, [
      { label: t('Close'), hint: shortcutOf('view.closeTab'), action: () => closeTab(props.id, tab.id) },
      { label: t('Close the others'), action: () => leaf()?.tabs.filter((x) => x !== tab.id).forEach((x) => closeTab(props.id, x)) },
      { label: t('Close all'), action: () => [...(leaf()?.tabs ?? [])].forEach((x) => closeTab(props.id, x)) },
      { separator: true, label: '' },
      { label: t('Split right'), hint: shortcutOf('view.splitRight'), action: () => (activateTab(props.id, tab.id), splitPane(props.id, 'row')) },
      { label: t('Split down'), hint: shortcutOf('view.splitDown'), action: () => (activateTab(props.id, tab.id), splitPane(props.id, 'col')) },
      ...(tab.kind === 'tool' || tab.kind === 'console' ? [{ label: t('Open in a window'), action: () => detachTab(tab) }] : []),
      ...(route().name === 'project' ? deskItems(props.id, tab.id) : []),
      ...(tab.kind === 'file'
        ? [
            { separator: true, label: '' },
            { label: t('Copy the path'), action: () => copyText(tab.path!) },
            { label: t('Copy the relative path'), action: () => copyText(relPath(tab.path!)) },
          ]
        : []),
    ])

  // A tool or a console alone in its tile has no tab bar: its buttons go in the corner.
  const bare = () => leaf()?.tabs.length === 1 && (active()?.kind === 'tool' || active()?.kind === 'console')
  const corner = (tab: TabState) => (
    <div class="tile-corner">
      <button class="icon-btn" title={t('Open in a window')} onClick={() => detachTab(tab)} data-testid="tile-detach">
        <Icon name="external" size={13} />
      </button>
      <button class="icon-btn" title={`${t('Close')} (${shortcutOf('view.closeTab')})`} onClick={() => closeTab(props.id, tab.id)} onContextMenu={(e) => tabMenu(e, tab)} data-testid="tile-close">
        <Icon name="close" size={13} />
      </button>
    </div>
  )

  // A tile whose console closed (or whose tool is gone) says so and closes.
  const gone = (tab: TabState, text: string) => (
    <div class="tile-gone" data-testid="tile-gone">
      <span>{text}</span>
      <button class="btn" onClick={() => closeTab(props.id, tab.id)}>
        {t('Close the tile')}
      </button>
    </div>
  )

  return (
    <div
      class="pane"
      data-pane={props.id}
      classList={{ active: isActivePane(), focused: isActivePane() && focusPart() === 'editor', [`next-${nextSplit()}`]: isActivePane() && tileMode(), resizing: isActivePane() && resizing(), bare: bare() }}
      onMouseDown={(e) => e.button < 3 && setActivePane(props.id)}>
      <div
        ref={tabbar}
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
                      requestAnimationFrame(() => document.querySelector<HTMLElement>('.pane.active .md-file-view, .pane.active .ed-content')?.focus())
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
                  <span class="tab-title">
                    <Show when={tab().kind === 'file' && dirs().get(relPath(tab().path ?? ''))}>{(d) => <span class="tab-dir">{d()}</span>}</Show>
                    {tabTitle(tab())}
                  </span>
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
          {(tab) => (
            <Switch>
              <Match when={tab.kind === 'file'}>
                <Show when={pluginFileView()} keyed fallback={gone(tab, t('The plugin of this tab is disabled'))}>
                  {(View) => <View tab={tab} paneId={props.id} />}
                </Show>
              </Match>
              <Match when={tab.kind === 'text'}>
                <TextViewer tab={tab} paneId={props.id} />
              </Match>
              <Match when={tab.kind === 'doc'}>
                <DocView tab={tab} paneId={props.id} />
              </Match>
              <Match when={tab.kind === 'diff'}>
                <DiffView tab={tab} paneId={props.id} />
              </Match>
              <Match when={tab.kind === 'kanban'}>
                <Board />
              </Match>
              <Match when={tab.kind === 'ticket'}>
                <TicketView tab={tab} paneId={props.id} />
              </Match>
              <Match when={tab.kind === 'milestone'}>
                <MilestoneView tab={tab} paneId={props.id} />
              </Match>
              <Match when={tab.kind === 'objective'}>
                <ObjectiveView tab={tab} />
              </Match>
              <Match when={tab.kind === 'tool'}>
                <div class="tile-tool panel-tile" data-tool={tab.tool}>
                  <Show when={bare()}>{corner(tab)}</Show>
                  <Show when={tileTools[tab.tool!]} keyed fallback={gone(tab, tab.tool!)}>
                    {(p) => <Dynamic component={p.component} />}
                  </Show>
                </div>
              </Match>
              <Match when={tab.kind.startsWith('plugin:')}>
                <Show when={pluginView(tab.kind)} keyed fallback={gone(tab, t('The plugin of this tab is disabled'))}>
                  {(View) => <View tab={tab} paneId={props.id} />}
                </Show>
              </Match>
              <Match when={tab.kind === 'console'}>
                <Show when={capability('terminal')?.consoles().some((c) => c.id === tab.console)} fallback={gone(tab, capability('terminal') ? t('This console is closed') : t('The plugin of this tab is disabled'))}>
                  <div class="tile-console">
                    <Show when={bare()}>{corner(tab)}</Show>
                    <Dynamic component={capability('terminal')!.TermView} id={tab.console!} focus={isActivePane()} />
                  </div>
                </Show>
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

// Soft wrap: the setting, unless Alt+Z toggled it for a document (until the page reloads).
const wrapToggled = new WeakMap<Doc, boolean>()
const [wrapGeneration, setWrapGeneration] = createSignal(0)

export function wrapOf(d: Doc) {
  wrapGeneration()
  const own = wrapToggled.get(d)
  if (own !== undefined) return own
  const w = settings.editor.wordWrap
  return w === 'on' || (w === 'markdown' && (d.lang === 'markdown' || d.lang === 'plaintext'))
}

export function toggleWrap(d: Doc) {
  wrapToggled.set(d, !untrack(() => wrapOf(d)))
  setWrapGeneration((g) => g + 1)
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
        wrap: untrack(() => wrapOf(d)),
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
      wrap: wrapOf(v.doc),
    })
  })
  createEffect(
    on(
      () => [settings.font.family, fontFor().size, fontFor().lineHeight],
      () => requestAnimationFrame(() => view()?.measure()),
      { defer: true },
    ),
  )
  createEffect(on(grammarGeneration, () => view()?.refreshGrammar(), { defer: true }))
  return view
}

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
