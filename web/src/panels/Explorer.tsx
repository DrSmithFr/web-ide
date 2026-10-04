// File explorer: lazy tree (one fs.list per expanded folder), refreshed by the pod watch events.
// A click selects, a double click (or Enter) opens the file and gives it the focus.
import { createEffect, createSignal, For, onMount, Show, untrack } from 'solid-js'
import { createStore } from 'solid-js/store'
import { on as onPod, request } from '../pod/rpc'
import { activeTab, basename, mutate, openFile, project, relPath, root, session } from '../state/project'
import { contextMenu, prompt, type MenuItem } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { FileIcon } from './fileIcons'
import { newConsole } from '../console/consoles'
import { dirState, fileState } from '../state/git'
import { folderMark, inExcluded, loadFolderMarks, markFolder, type FolderMark } from '../state/folders'
import { t } from '../i18n'
import { copyText } from '../ui/clipboard'

interface Entry {
  name: string
  path: string
  dir: boolean
  link?: boolean
  size: number
}

const [children, setChildren] = createStore<Record<string, Entry[] | undefined>>({})
const [errors, setErrors] = createStore<Record<string, string | undefined>>({})
// Paths left out by .gitignore (their content is too).
const [ignored, setIgnored] = createStore<Record<string, boolean | undefined>>({})
const [selected, setSelected] = createSignal<string | null>(null)

/** Folders never opened by "Expand all". */
const heavyDirs = new Set(['.git', 'node_modules'])

const markLabels: Record<FolderMark, string> = { source: 'Source folder', tests: 'Test folder', excluded: 'Excluded folder' }

async function load(dir: string) {
  let list: Entry[]
  try {
    list = await request<Entry[]>('fs.list', { path: dir })
    setChildren(dir, list)
    setErrors(dir, undefined)
  } catch (e) {
    setErrors(dir, (e as Error).message)
    return
  }
  try {
    const ign = new Set(await request<string[]>('git.ignored', { paths: list.map((e) => e.path) }))
    for (const e of list) setIgnored(e.path, ign.has(e.path) || undefined)
  } catch {
    // Not a repository, or git missing: nothing is ignored.
  }
}

onPod('fs.dir', (e: { path: string }) => {
  if (children[e.path]) load(e.path)
})

function isExpanded(p: string) {
  return session.expanded.includes(p)
}

function setExpanded(p: string, open: boolean) {
  mutate((s) => {
    const i = s.expanded.indexOf(p)
    if (open && i < 0) s.expanded.push(p)
    if (!open && i >= 0) s.expanded.splice(i, 1)
  })
  if (open && !children[p]) load(p)
}

const rowOf = (path: string) => document.querySelector<HTMLElement>(`.explorer .tree-row[data-path="${CSS.escape(path)}"]`)

/** Expands the folders down to a file and selects it (focusing its row when asked). */
export function revealInExplorer(path: string, focus = false) {
  const r = root()
  if (!path.startsWith(r + '/')) return
  const parts = path.slice(r.length + 1).split('/')
  let cur = r
  for (const part of parts.slice(0, -1)) {
    cur += '/' + part
    if (!isExpanded(cur)) setExpanded(cur, true)
  }
  setSelected(path)
  // The rows of the folders just opened appear once listed.
  let n = 0
  const show = () => {
    const row = rowOf(path)
    if (row) {
      row.scrollIntoView({ block: 'nearest' })
      if (focus) row.focus()
    } else if (n++ < 30) setTimeout(show, 50)
  }
  requestAnimationFrame(show)
}

/** Opens a file and moves the focus to its editor. */
async function openAndFocus(path: string) {
  await openFile(path)
  let n = 0
  const focus = () => {
    const ed = document.querySelector<HTMLElement>('.pane.active .ed-content')
    if (ed && activeTab()?.path === path) ed.focus()
    else if (n++ < 20) requestAnimationFrame(focus)
  }
  requestAnimationFrame(focus)
}

/** Opens every folder, level by level, except the ignored, excluded and heavy ones. */
async function expandAll() {
  const opened: string[] = []
  let level = [root()]
  while (level.length && opened.length < 300) {
    await Promise.all(level.filter((d) => !children[d]).map(load))
    const next: string[] = []
    for (const d of level)
      for (const e of children[d] ?? []) {
        if (!e.dir || e.link || heavyDirs.has(e.name) || ignored[e.path] || inExcluded(e.path)) continue
        if (!session.explorer.hidden && e.name.startsWith('.')) continue
        next.push(e.path)
      }
    level = next.slice(0, 300 - opened.length)
    opened.push(...level)
  }
  mutate((s) => {
    for (const p of opened) if (!s.expanded.includes(p)) s.expanded.push(p)
  })
}

function collapseAll() {
  mutate((s) => {
    s.expanded = []
  })
}

/** Asks the name of a new file or folder in dir, creates it and opens the file. */
export async function createIn(dir: string, isDir: boolean) {
  const name = await prompt({ title: isDir ? t('New folder') : t('New file'), label: t('In {dir}', { dir: relPath(dir) || '/' }), placeholder: isDir ? t('name') : t('name.ext (or sub/folder/name.ext)') })
  if (!name) return
  const path = dir + '/' + name.replace(/^\/+/, '')
  try {
    await request('fs.create', { path, dir: isDir })
    setExpanded(dir, true)
    await load(dir)
    if (!isDir) openAndFocus(path)
  } catch (e) {
    errorToast(e)
  }
}

async function rename(e: Entry) {
  const name = await prompt({ title: t('Rename'), value: e.name })
  if (!name || name === e.name) return
  const dir = e.path.slice(0, e.path.lastIndexOf('/'))
  try {
    await request('fs.rename', { path: e.path, to: dir + '/' + name })
    load(dir)
  } catch (err) {
    errorToast(err)
  }
}

async function remove(e: Entry) {
  if (!confirm(e.dir ? t('Delete the folder “{path}” and all its content?', { path: relPath(e.path) }) : t('Delete the file “{path}”?', { path: relPath(e.path) }))) return
  try {
    await request('fs.delete', { path: e.path })
    load(e.path.slice(0, e.path.lastIndexOf('/')))
  } catch (err) {
    errorToast(err)
  }
}

function refreshAll() {
  for (const k of Object.keys(children)) if (children[k]) load(k)
  toast(t('Explorer refreshed'), 'info')
}

function menuFor(ev: MouseEvent, e: Entry) {
  const dir = e.dir ? e.path : e.path.slice(0, e.path.lastIndexOf('/'))
  const items: MenuItem[] = [
    { label: t('New file…'), action: () => createIn(dir, false) },
    { label: t('New folder…'), action: () => createIn(dir, true) },
    { separator: true, label: '' },
    { label: t('Rename…'), action: () => rename(e), disabled: e.path === root() },
    { label: t('Delete'), action: () => remove(e), danger: true, disabled: e.path === root() },
    { separator: true, label: '' },
    { label: t('Copy the path'), action: () => copyText(e.path) },
    { label: t('Copy the relative path'), action: () => copyText(relPath(e.path)) },
    { label: t('Open a terminal here'), action: () => newConsole({ cwd: dir }) },
    { label: t('Refresh'), action: () => load(dir) },
  ]
  if (e.dir && e.path !== root()) {
    const cur = folderMark(e.path)
    items.push(
      { separator: true, label: '' },
      ...(Object.keys(markLabels) as FolderMark[]).map((m) => ({ label: t(markLabels[m]), checked: cur === m, action: () => markFolder(e.path, cur === m ? '' : m) })),
    )
  }
  contextMenu(ev, items)
}

function optionsMenu(ev: MouseEvent) {
  const r = (ev.currentTarget as HTMLElement).getBoundingClientRect()
  const toggle = (k: keyof typeof session.explorer) => () =>
    mutate((s) => {
      s.explorer[k] = !s.explorer[k]
    })
  contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.bottom + 2 }), [
    { label: t('New file…'), action: () => createIn(root(), false) },
    { label: t('New folder…'), action: () => createIn(root(), true) },
    { separator: true, label: '' },
    { label: t('Show the hidden files'), checked: session.explorer.hidden, action: toggle('hidden') },
    { label: t('Show the excluded folders'), checked: session.explorer.excluded, action: toggle('excluded') },
    { label: t('Open files with a single click'), checked: session.explorer.singleClick, action: toggle('singleClick') },
    { label: t('Always select the opened file'), checked: session.explorer.follow, action: toggle('follow') },
    { separator: true, label: '' },
    { label: t('Refresh all'), action: refreshAll },
  ])
}

/** What a folder passes to its content: ignored by git, excluded. */
interface Inherited {
  ignored: boolean
  excluded: boolean
}

function Rows(props: { dir: string; depth: number; inherited: Inherited }) {
  const shown = () =>
    (children[props.dir] ?? []).filter((e) => (session.explorer.hidden || !e.name.startsWith('.')) && (session.explorer.excluded || folderMark(e.path) !== 'excluded'))
  return (
    <>
      <Show when={errors[props.dir]}>
        <div class="tree-error" style={{ 'padding-left': `${props.depth * 14 + 22}px` }}>
          {errors[props.dir]}
        </div>
      </Show>
      <For each={shown()}>{(e) => <Row entry={e} depth={props.depth} inherited={props.inherited} />}</For>
    </>
  )
}

function Row(props: { entry: Entry; depth: number; inherited: Inherited }) {
  const e = props.entry
  const open = () => isExpanded(e.path)
  const mark = () => (e.dir ? folderMark(e.path) : null)
  const isIgnored = () => props.inherited.ignored || !!ignored[e.path]
  const isExcluded = () => props.inherited.excluded || mark() === 'excluded'
  const git = () => (e.dir ? dirState(e.path) : fileState(e.path)) ?? (isIgnored() ? 'ignored' : 'clean')
  const toggle = () => setExpanded(e.path, !open())
  const activate = () => (e.dir ? toggle() : openAndFocus(e.path))
  return (
    <>
      <div
        class="tree-row"
        role="treeitem"
        aria-expanded={e.dir ? open() : undefined}
        aria-selected={selected() === e.path}
        tabIndex={-1}
        data-path={e.path}
        data-mark={mark() ?? undefined}
        title={mark() ? t(markLabels[mark()!]) : undefined}
        classList={{ selected: selected() === e.path, current: activeTab()?.path === e.path, excluded: isExcluded() }}
        style={{ 'padding-left': `${props.depth * 14 + 6}px` }}
        onFocus={() => setSelected(e.path)}
        onClick={(ev) => {
          setSelected(e.path)
          ;(ev.currentTarget as HTMLElement).focus()
          if (e.dir && (ev.target as Element).closest('.tree-twist')) toggle()
          else if (session.explorer.singleClick) e.dir ? toggle() : openFile(e.path)
        }}
        onDblClick={(ev) => {
          if (session.explorer.singleClick || (ev.target as Element).closest('.tree-twist')) return
          activate()
        }}
        onKeyDown={(ev) => {
          const rows = () => [...document.querySelectorAll<HTMLElement>('.explorer .tree-row')]
          if (ev.key === 'Enter') activate()
          else if (ev.key === 'ArrowRight' && e.dir) {
            if (!open()) setExpanded(e.path, true)
            else rows()[rows().indexOf(ev.currentTarget) + 1]?.focus()
          } else if (ev.key === 'ArrowLeft') {
            if (e.dir && open()) setExpanded(e.path, false)
            else rowOf(e.path.slice(0, e.path.lastIndexOf('/')))?.focus()
          } else if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
            const list = rows()
            list[list.indexOf(ev.currentTarget) + (ev.key === 'ArrowDown' ? 1 : -1)]?.focus()
          } else if (ev.key === 'F2') rename(e)
          else if (ev.key === 'Delete') remove(e)
          else return
          ev.preventDefault()
        }}
        onContextMenu={(ev) => {
          setSelected(e.path)
          menuFor(ev, e)
        }}
      >
        <span class="tree-twist" classList={{ open: open(), none: !e.dir }}>
          <Show when={e.dir}>
            <Icon name="chevron" size={12} />
          </Show>
        </span>
        <FileIcon name={e.name} dir={e.dir} open={open()} mark={mark()} />
        <span class={`tree-name git-${git()}`}>{e.name}</span>
        <Show when={e.link}>
          <span class="tree-link" title={t('Symbolic link')}>↪</span>
        </Show>
      </div>
      <Show when={e.dir && open()}>
        <Rows dir={e.path} depth={props.depth + 1} inherited={{ ignored: isIgnored(), excluded: isExcluded() }} />
      </Show>
    </>
  )
}

export function Explorer() {
  onMount(() => {
    const r = root()
    if (r) load(r)
    for (const p of session.expanded) if (!children[p]) load(p)
  })
  createEffect(() => {
    const r = root()
    if (r && !children[r]) load(r)
    if (r) loadFolderMarks()
  })
  // "Always select the opened file".
  createEffect(() => {
    const p = activeTab()?.path
    if (session.explorer.follow && p) untrack(() => revealInExplorer(p))
  })
  const rootEntry = (): Entry => ({ name: project()?.name ?? basename(root()), path: root(), dir: true, size: 0 })
  return (
    <div class="panel explorer">
      <div class="panel-head">
        <span class="panel-title">{t('Explorer')}</span>
        <span class="grow" />
        <button class="icon-btn" data-testid="explorer-locate" title={t('Locate the active file')} onClick={() => activeTab()?.path && revealInExplorer(activeTab()!.path!, true)}>
          <Icon name="locate" />
        </button>
        <button class="icon-btn" data-testid="explorer-expand" title={t('Expand all')} onClick={expandAll}>
          <Icon name="expandAll" />
        </button>
        <button class="icon-btn" data-testid="explorer-collapse" title={t('Collapse all')} onClick={collapseAll}>
          <Icon name="collapseAll" />
        </button>
        <button class="icon-btn" data-testid="explorer-options" title={t('Options')} onClick={optionsMenu}>
          <Icon name="gear" />
        </button>
      </div>
      <div class="panel-body tree" role="tree" onContextMenu={(e) => e.target === e.currentTarget && menuFor(e, rootEntry())}>
        <div class="tree-root" onContextMenu={(e) => menuFor(e, rootEntry())}>
          <span>{rootEntry().name}</span>
          <span class="muted" title={root()}>
            <bdi>{relPath(root()) === root() ? root() : ''}</bdi>
          </span>
        </div>
        <Rows dir={root()} depth={0} inherited={{ ignored: false, excluded: false }} />
      </div>
    </div>
  )
}
