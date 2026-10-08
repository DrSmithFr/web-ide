// Recent Files (Ctrl+E) and the switcher (Ctrl+Tab): the tools on the left, the files last
// shown on the right. Ctrl+E again keeps the changed files only (unsaved, or changed for git).
// The switcher opens while the modifier is held: the shortcut again moves down, Shift+Tab up,
// releasing the modifier opens the selected entry.
import { batch, createEffect, createSignal, For, on, onCleanup, onMount, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import { activeTab, basename, forgetRecent, getDoc, openFile, relPath, session } from '../state/project'
import { fileState } from '../state/git'
import { fuzzy } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { FileIcon } from '../panels/fileIcons'
import { keepFocus } from '../state/focus'
import { WorktreeChip } from '../ui/WorktreeChip'
import { t } from '../i18n'
import './popups.css'

export interface SwitcherTool {
  id: string
  label: string
  icon: string
  shortcut: string
}

type Column = 'tools' | 'files'

const [open, setOpen] = createSignal(false)
const [switcher, setSwitcher] = createSignal(false)
const [changedOnly, setChangedOnly] = createSignal(false)
const [query, setQuery] = createSignal('')
const [column, setColumn] = createSignal<Column>('files')
const [index, setIndex] = createSignal(0)

const changed = (p: string) => !!getDoc(p)?.dirty() || !!fileState(p)

function files() {
  const q = query().trim()
  const list = session.recent.filter((p) => !changedOnly() || changed(p))
  return q ? list.filter((p) => fuzzy(q, basename(p)) > 0) : list
}

let tools: () => SwitcherTool[] = () => []
function shownTools() {
  const q = query().trim()
  return q ? tools().filter((x) => fuzzy(q, t(x.label)) > 0) : tools()
}

const size = (c: Column) => (c === 'files' ? files().length : shownTools().length)

/** Moves the selection, wrapping around in the column. */
function step(d: number) {
  const n = size(column())
  if (n) setIndex((i) => (i + d + n) % n)
}

/** First entry: the previous file when the active one leads the list, so that Enter goes back to it. */
function firstFile() {
  const list = files()
  return list.length > 1 && list[0] === activeTab()?.path ? 1 : 0
}

/** Recent Files (mode recent) or the switcher; already open: the shortcut again filters or moves down. */
export function recentFiles(mode: 'recent' | 'switcher') {
  if (open()) {
    if (switcher()) step(1)
    else {
      setChangedOnly((v) => !v)
      setColumn('files')
      setIndex(firstFile())
    }
    return
  }
  batch(() => {
    setSwitcher(mode === 'switcher')
    setChangedOnly(false)
    setQuery('')
    setColumn(session.recent.length ? 'files' : 'tools')
    setIndex(firstFile())
    setOpen(true)
  })
}

export function RecentFilesHost(props: { tools: () => SwitcherTool[]; onTool: (id: string) => void }) {
  let input!: HTMLInputElement
  let body!: HTMLDivElement
  let restoreFocus: () => HTMLElement | null = () => null
  tools = props.tools

  const close = (run?: () => void) => {
    setOpen(false)
    restoreFocus()
    run?.()
  }

  const accept = () => {
    const i = index()
    if (column() === 'files') {
      const p = files()[i]
      if (p) close(() => void openFile(p))
    } else {
      const tool = shownTools()[i]
      if (tool) close(() => props.onTool(tool.id))
    }
  }

  createEffect(
    on(open, (o) => {
      if (!o) return
      restoreFocus = keepFocus()
      queueMicrotask(() => input?.focus())
    }),
  )
  // Typing filters both columns: the selection goes back to the top of the column.
  createEffect(
    on(
      query,
      () => {
        setIndex(0)
        const other: Column = column() === 'files' ? 'tools' : 'files'
        if (!size(column()) && size(other)) setColumn(other)
      },
      { defer: true },
    ),
  )
  createEffect(() => {
    if (!open()) return
    const n = size(column())
    if (index() >= n) setIndex(Math.max(0, n - 1))
    body?.querySelector(`.rf-${column()} [data-index="${index()}"]`)?.scrollIntoView({ block: 'nearest' })
  })

  const key = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || (e.key === 'Tab' && !e.shiftKey)) step(1)
    else if (e.key === 'ArrowUp' || e.key === 'Tab') step(-1)
    else if ((e.key === 'ArrowLeft' || e.key === 'ArrowRight') && !query()) {
      const c: Column = e.key === 'ArrowLeft' ? 'tools' : 'files'
      if (c === column() || !size(c)) return
      setColumn(c)
      setIndex((i) => Math.min(i, size(c) - 1))
    } else if (e.key === 'Enter') accept()
    else if (e.key === 'Escape') close()
    else if (e.key === 'Delete' && column() === 'files' && !query()) {
      const p = files()[index()]
      if (p) forgetRecent(p)
    } else return
    e.preventDefault()
    e.stopPropagation()
  }

  // Switcher: releasing the last modifier opens the selection.
  onMount(() => {
    const up = (e: KeyboardEvent) => {
      if (!open() || !switcher() || !['Control', 'Alt', 'Meta'].includes(e.key)) return
      if (!e.ctrlKey && !e.altKey && !e.metaKey) accept()
    }
    window.addEventListener('keyup', up, true)
    onCleanup(() => window.removeEventListener('keyup', up, true))
  })

  const selected = (c: Column, i: number) => column() === c && index() === i
  const pick = (c: Column, i: number) => {
    setColumn(c)
    setIndex(i)
  }

  return (
    <Show when={open()}>
      <Portal>
        <div class="pick-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
          <div class="pick rf" role="dialog" aria-label={t('Recent files')} onKeyDown={key}>
            <div class="rf-head">
              <span class="rf-title">{changedOnly() ? t('Recently changed files') : t('Recent files')}</span>
              <input ref={input} class="rf-filter" placeholder={t('Filter')} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
            </div>
            <div class="rf-body" ref={body}>
              <div class="rf-tools" role="listbox" aria-label={t('Tools')}>
                <For each={shownTools()}>
                  {(tool, i) => (
                    <div
                      class="pick-item"
                      role="option"
                      data-index={i()}
                      aria-selected={selected('tools', i())}
                      classList={{ selected: selected('tools', i()) }}
                      onMouseEnter={() => pick('tools', i())}
                      onMouseDown={(e) => {
                        e.preventDefault()
                        pick('tools', i())
                        accept()
                      }}
                    >
                      <span class="pick-icon">
                        <Icon name={tool.icon} size={14} />
                      </span>
                      <span class="pick-label">{t(tool.label)}</span>
                      <Show when={tool.shortcut}>
                        <kbd class="pick-hint">{tool.shortcut}</kbd>
                      </Show>
                    </div>
                  )}
                </For>
              </div>
              <div class="rf-files" role="listbox" aria-label={t('Files')}>
                <For each={files()} fallback={<div class="pick-empty">{changedOnly() ? t('No changed file') : t('No recent file')}</div>}>
                  {(p, i) => (
                    <div
                      class="pick-item"
                      role="option"
                      data-index={i()}
                      data-path={p}
                      aria-selected={selected('files', i())}
                      classList={{ selected: selected('files', i()) }}
                      onMouseEnter={() => pick('files', i())}
                      onMouseDown={(e) => {
                        e.preventDefault()
                        pick('files', i())
                        accept()
                      }}
                    >
                      <span class="pick-icon">
                        <FileIcon name={basename(p)} />
                      </span>
                      <span class={`pick-label rf-name git-${fileState(p) ?? 'clean'}`}>{basename(p)}</span>
                      <WorktreeChip path={p} />
                      <Show when={getDoc(p)?.dirty()}>
                        <span class="rf-dirty" title={t('Unsaved changes')}>●</span>
                      </Show>
                    </div>
                  )}
                </For>
              </div>
            </div>
            <div class="rf-foot mono">{column() === 'files' ? relPath(files()[index()] ?? '') : ''}</div>
          </div>
        </div>
      </Portal>
    </Show>
  )
}
