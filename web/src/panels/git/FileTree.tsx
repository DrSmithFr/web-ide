// Tree of changed files, drawn as the explorer: a click opens the file's diff, a double
// click the file; arrows move, Space toggles the check box of the row.
import { createEffect, createSignal, For, type JSX, on, Show } from 'solid-js'
import { Icon } from '../../ui/icons'
import { FileIcon } from '../fileIcons'
import type { TreeNode } from './tree'

export function FileTree<T>(props: {
  root: TreeNode<T>
  label: JSX.Element
  /** Folders closed by the user (absolute paths). */
  closed: () => Set<string>
  setClosed: (s: Set<string>) => void
  /** Explorer color of a file: modified, added, untracked, conflict, deleted. */
  stateOf: (item: T) => string
  /** Former path of a renamed file. */
  fromOf?: (item: T) => string | undefined
  /** End of a row (the check box). */
  end?: (node: TreeNode<T>) => JSX.Element
  toggle?: (node: TreeNode<T>) => void
  open: (item: T) => void
  openFile?: (item: T) => void
  menu?: (e: MouseEvent, node: TreeNode<T>) => void
  testid?: string
}) {
  const [selected, setSelected] = createSignal<string | null>(null)
  let box!: HTMLDivElement
  const isOpen = (n: TreeNode<T>) => !props.closed().has(n.path)
  const setOpen = (n: TreeNode<T>, open: boolean) => {
    const s = new Set(props.closed())
    open ? s.delete(n.path) : s.add(n.path)
    props.setClosed(s)
  }
  const rows = () => [...box.querySelectorAll<HTMLElement>('.tree-row')]
  // Rows are rebuilt with the status: the selected row keeps the focus it had.
  createEffect(
    on(
      () => props.root,
      () =>
        requestAnimationFrame(() => {
          if (document.activeElement !== document.body || !selected()) return
          rows().find((r) => r.dataset.path === selected())?.focus()
        }),
      { defer: true },
    ),
  )

  function Row(p: { node: TreeNode<T>; depth: number; root?: boolean }) {
    const n = p.node
    const dir = !n.item
    const color = () => (n.item ? props.stateOf(n.item) : '')
    const from = () => (n.item && props.fromOf?.(n.item)) || ''
    return (
      <>
        <div
          class="tree-row"
          role="treeitem"
          tabIndex={-1}
          data-path={n.path}
          aria-expanded={dir ? isOpen(n) : undefined}
          classList={{ selected: selected() === n.path, 'tree-top': !!p.root }}
          style={{ 'padding-left': `${p.depth * 14 + 6}px` }}
          onFocus={() => setSelected(n.path)}
          onClick={(e) => {
            setSelected(n.path)
            e.currentTarget.focus()
            if ((e.target as Element).closest('.git-check')) return
            if (dir) setOpen(n, !isOpen(n))
            else props.open(n.item!)
          }}
          onDblClick={(e) => n.item && !(e.target as Element).closest('.git-check') && props.openFile?.(n.item)}
          onContextMenu={(e) => {
            setSelected(n.path)
            props.menu?.(e, n)
          }}
          onKeyDown={(e) => {
            const list = rows()
            const i = list.indexOf(e.currentTarget)
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') list[i + (e.key === 'ArrowDown' ? 1 : -1)]?.focus()
            else if (e.key === 'ArrowRight' && dir && !isOpen(n)) setOpen(n, true)
            else if (e.key === 'ArrowLeft' && dir && isOpen(n) && !p.root) setOpen(n, false)
            else if (e.key === 'Enter') dir ? setOpen(n, !isOpen(n)) : props.open(n.item!)
            else if (e.key === ' ' && props.toggle) props.toggle(n)
            else return
            e.preventDefault()
          }}
        >
          <span class="tree-twist" classList={{ open: isOpen(n), none: !dir || p.root }}>
            <Show when={dir && !p.root}>
              <Icon name="chevron" size={12} />
            </Show>
          </span>
          <Show when={p.root} fallback={<FileIcon name={n.name} dir={dir} open={isOpen(n)} />}>
            {props.label}
          </Show>
          <Show when={!p.root}>
            <span class={`tree-name git-${color()}`}>{n.name}</span>
            <Show when={dir}>
              <span class="git-count">{n.items.length}</span>
            </Show>
            <Show when={from()}>
              <span class="git-from">← {from()}</span>
            </Show>
          </Show>
          <span class="grow" />
          {props.end?.(n)}
        </div>
        <Show when={dir && (p.root || isOpen(n))}>
          <For each={n.children}>{(c) => <Row node={c} depth={p.depth + 1} />}</For>
        </Show>
      </>
    )
  }

  return (
    <div class="tree git-tree" role="tree" ref={box} data-testid={props.testid}>
      <Show when={props.root} keyed>
        {(root) => <Row node={root} depth={0} root />}
      </Show>
    </div>
  )
}
