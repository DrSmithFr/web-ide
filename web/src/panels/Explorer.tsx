// File explorer: lazy tree (one fs.list per expanded folder), refreshed by the pod watch events.
import { createEffect, createSignal, For, onMount, Show } from 'solid-js'
import { createStore } from 'solid-js/store'
import { on as onPod, request } from '../pod/rpc'
import { activeTab, basename, mutate, openFile, project, relPath, root, session } from '../state/project'
import { contextMenu, prompt } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { newConsole } from '../console/consoles'

interface Entry {
  name: string
  path: string
  dir: boolean
  link?: boolean
  size: number
}

const [children, setChildren] = createStore<Record<string, Entry[] | undefined>>({})
const [errors, setErrors] = createStore<Record<string, string | undefined>>({})

async function load(dir: string) {
  try {
    const list = await request<Entry[]>('fs.list', { path: dir })
    setChildren(dir, list)
    setErrors(dir, undefined)
  } catch (e) {
    setErrors(dir, (e as Error).message)
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

/** Expands the folders down to a file and selects it. */
export function revealInExplorer(path: string) {
  const r = root()
  if (!path.startsWith(r + '/')) return
  const parts = path.slice(r.length + 1).split('/')
  let cur = r
  for (const part of parts.slice(0, -1)) {
    cur += '/' + part
    setExpanded(cur, true)
  }
  requestAnimationFrame(() => document.querySelector(`.tree-row[data-path="${CSS.escape(path)}"]`)?.scrollIntoView({ block: 'nearest' }))
}

async function createIn(dir: string, isDir: boolean) {
  const name = await prompt({ title: isDir ? 'Nouveau dossier' : 'Nouveau fichier', label: `Dans ${relPath(dir) || '/'}`, placeholder: isDir ? 'nom' : 'nom.ext (ou sous/dossier/nom.ext)' })
  if (!name) return
  const path = dir + '/' + name.replace(/^\/+/, '')
  try {
    await request('fs.create', { path, dir: isDir })
    setExpanded(dir, true)
    await load(dir)
    if (!isDir) openFile(path)
  } catch (e) {
    errorToast(e)
  }
}

async function rename(e: Entry) {
  const name = await prompt({ title: 'Renommer', value: e.name })
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
  if (!confirm(`Supprimer ${e.dir ? 'le dossier' : 'le fichier'} « ${relPath(e.path)} »${e.dir ? ' et tout son contenu' : ''} ?`)) return
  try {
    await request('fs.delete', { path: e.path })
    load(e.path.slice(0, e.path.lastIndexOf('/')))
  } catch (err) {
    errorToast(err)
  }
}

function menuFor(ev: MouseEvent, e: Entry) {
  const dir = e.dir ? e.path : e.path.slice(0, e.path.lastIndexOf('/'))
  contextMenu(ev, [
    { label: 'Nouveau fichier…', action: () => createIn(dir, false) },
    { label: 'Nouveau dossier…', action: () => createIn(dir, true) },
    { separator: true, label: '' },
    { label: 'Renommer…', action: () => rename(e), disabled: e.path === root() },
    { label: 'Supprimer', action: () => remove(e), danger: true, disabled: e.path === root() },
    { separator: true, label: '' },
    { label: 'Copier le chemin', action: () => navigator.clipboard.writeText(e.path) },
    { label: 'Copier le chemin relatif', action: () => navigator.clipboard.writeText(relPath(e.path)) },
    { label: 'Ouvrir un terminal ici', action: () => newConsole({ cwd: dir }) },
    { label: 'Rafraîchir', action: () => load(dir) },
  ])
}

function Rows(props: { dir: string; depth: number }) {
  return (
    <>
      <Show when={errors[props.dir]}>
        <div class="tree-error" style={{ 'padding-left': `${props.depth * 14 + 22}px` }}>
          {errors[props.dir]}
        </div>
      </Show>
      <For each={children[props.dir] ?? []}>{(e) => <Row entry={e} depth={props.depth} />}</For>
    </>
  )
}

function Row(props: { entry: Entry; depth: number }) {
  const e = props.entry
  const open = () => isExpanded(e.path)
  const active = () => activeTab()?.path === e.path
  const toggle = () => (e.dir ? setExpanded(e.path, !open()) : openFile(e.path))
  return (
    <>
      <div
        class="tree-row"
        role="treeitem"
        aria-expanded={e.dir ? open() : undefined}
        aria-selected={active()}
        tabIndex={-1}
        data-path={e.path}
        classList={{ active: active(), hidden: e.name.startsWith('.') }}
        style={{ 'padding-left': `${props.depth * 14 + 6}px` }}
        onClick={toggle}
        onKeyDown={(ev) => {
          if (ev.key === 'Enter') toggle()
          else if (ev.key === 'ArrowRight' && e.dir && !open()) setExpanded(e.path, true)
          else if (ev.key === 'ArrowLeft' && e.dir && open()) setExpanded(e.path, false)
          else if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
            const rows = [...document.querySelectorAll<HTMLElement>('.explorer .tree-row')]
            const i = rows.indexOf(ev.currentTarget)
            rows[i + (ev.key === 'ArrowDown' ? 1 : -1)]?.focus()
          } else if (ev.key === 'F2') rename(e)
          else if (ev.key === 'Delete') remove(e)
          else return
          ev.preventDefault()
        }}
        onContextMenu={(ev) => menuFor(ev, e)}
      >
        <span class="tree-twist" classList={{ open: open(), none: !e.dir }}>
          <Show when={e.dir}>
            <Icon name="chevron" size={12} />
          </Show>
        </span>
        <Icon name={e.dir ? 'folder' : 'file'} size={14} />
        <span class="tree-name">{e.name}</span>
        <Show when={e.link}>
          <span class="tree-link" title="Lien symbolique">↪</span>
        </Show>
      </div>
      <Show when={e.dir && open()}>
        <Rows dir={e.path} depth={props.depth + 1} />
      </Show>
    </>
  )
}

export function Explorer() {
  const [showHidden, setShowHidden] = createSignal(true)
  onMount(() => {
    const r = root()
    if (r) load(r)
    for (const p of session.expanded) if (!children[p]) load(p)
  })
  createEffect(() => {
    const r = root()
    if (r && !children[r]) load(r)
  })
  const rootEntry = (): Entry => ({ name: project()?.name ?? basename(root()), path: root(), dir: true, size: 0 })
  return (
    <div class="panel explorer" classList={{ 'hide-dotfiles': !showHidden() }}>
      <div class="panel-head">
        <span class="panel-title">Explorateur</span>
        <span class="grow" />
        <button class="icon-btn" title="Nouveau fichier" onClick={() => createIn(root(), false)}>
          <Icon name="plus" />
        </button>
        <button class="icon-btn" title="Localiser le fichier actif" onClick={() => activeTab()?.path && revealInExplorer(activeTab()!.path!)}>
          <Icon name="locate" />
        </button>
        <button class="icon-btn" title={showHidden() ? 'Masquer les fichiers cachés' : 'Afficher les fichiers cachés'} onClick={() => setShowHidden(!showHidden())}>
          <span class="dotfiles-toggle">.*</span>
        </button>
        <button
          class="icon-btn"
          title="Tout rafraîchir"
          onClick={() => {
            for (const k of Object.keys(children)) if (children[k]) load(k)
            toast('Explorateur rafraîchi', 'info')
          }}
        >
          <Icon name="refresh" />
        </button>
      </div>
      <div class="panel-body tree" role="tree" onContextMenu={(e) => e.target === e.currentTarget && menuFor(e, rootEntry())}>
        <div class="tree-root" onContextMenu={(e) => menuFor(e, rootEntry())}>
          {rootEntry().name} <span class="muted">{relPath(root()) === root() ? root() : ''}</span>
        </div>
        <Rows dir={root()} depth={0} />
      </div>
    </div>
  )
}
