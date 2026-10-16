// Desks of the project in the menu bar (docs/desks.md): 1 2 3 +. A desk is a link: a click
// switches the window, a middle click opens it in a new tab. A tab dropped on a desk moves there.
import { createSignal, For, Show } from 'solid-js'
import { podPath } from '../app/base'
import { deskPath, navigate } from '../app/router'
import { home, moveTabToDesk, switchDesk } from '../state/project'
import { addDesk, desk, deskLabel, desks, removeDesk, renameDesk, type Desk } from '../state/desks'
import { changeMode, tileMode, nextSplit, resizing, toggleSplit } from '../state/tiles'
import { tileLauncher } from '../ui/tileLauncher'
import { Icon } from '../ui/icons'
import { draggedTab } from '../ui/EditorArea'
import { shortcutOf } from '../keys/bindings'
import { contextMenu, prompt } from '../ui/overlay'
import { toast } from '../ui/toast'
import { t } from '../i18n'

export function DeskSwitcher() {
  const [over, setOver] = createSignal(0)
  const path = (n: number) => deskPath(home()!.id, n)
  const go = (e: MouseEvent, n: number) => {
    if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey) return // the browser opens a new tab
    e.preventDefault()
    navigate(path(n))
  }
  const menu = (e: MouseEvent, d: Desk) =>
    contextMenu(e, [
      { label: t('Open in a new tab'), action: () => window.open(podPath(path(d.n)), '_blank') },
      {
        label: t('Rename…'),
        action: async () => {
          const v = await prompt({ title: t('Rename the desk'), label: t('Name of desk {n}', { n: d.n }), value: d.name ?? '' })
          if (v !== null) renameDesk(d.n, v)
        },
      },
      { separator: true, label: '' },
      { label: t('Full layout'), title: t('Rails and tool zones'), checked: d.mode === 'full', action: () => changeMode(d.n, 'full') },
      { label: t('Grid of tiles'), title: t('Tiles only, like i3'), checked: d.mode === 'grid', action: () => changeMode(d.n, 'grid') },
      { label: t('Scrolling columns'), title: t('Columns of tiles side by side, like niri'), checked: d.mode === 'scroll', action: () => changeMode(d.n, 'scroll') },
      { separator: true, label: '' },
      { label: t('Remove the desk'), disabled: d.n === 1, title: d.n === 1 ? t('Desk 1 stays') : undefined, danger: true, action: () => remove(d) },
    ])
  const remove = async (d: Desk) => {
    if (!confirm(t('Remove the desk {name}? Its tabs close, the files stay open on the other desks.', { name: deskLabel(d) }))) return
    // Leave it first: its last changes must not write it back once removed.
    if (d.n === desk()) {
      await switchDesk(1)
      navigate(path(1), true)
    }
    removeDesk(d.n)
  }
  const add = async () => {
    try {
      navigate(path(await addDesk()))
    } catch (e) {
      toast((e as Error).message, 'warn')
    }
  }
  const drop = (e: DragEvent, n: number) => {
    const d = draggedTab()
    setOver(0)
    if (!d || n === desk()) return
    e.preventDefault()
    void moveTabToDesk(d.pane, d.tab, n).catch((err) => toast((err as Error).message, 'warn'))
  }
  return (
    <nav class="mb-desks" aria-label={t('Desks')} data-testid="desks">
      <For each={desks.desks}>
        {(d) => (
          <a
            class="mb-desk"
            classList={{ active: d.n === desk(), over: over() === d.n }}
            href={podPath(path(d.n))}
            aria-current={d.n === desk() ? 'page' : undefined}
            title={[d.name ? t('Desk {n}: {name}', { n: d.n, name: d.name }) : t('Desk {n}', { n: d.n }), shortcutOf(`desk.go.${d.n}`)].filter(Boolean).join(' · ')}
            data-testid={`desk-${d.n}`}
            onClick={(e) => go(e, d.n)}
            onContextMenu={(e) => menu(e, d)}
            onDragOver={(e) => {
              if (!draggedTab() || d.n === desk()) return
              e.preventDefault()
              setOver(d.n)
            }}
            onDragLeave={() => setOver(0)}
            onDrop={(e) => drop(e, d.n)}
          >
            {deskLabel(d)}
          </a>
        )}
      </For>
      <button class="mb-desk add" title={t('New desk')} onClick={add} data-testid="desk-add">
        +
      </button>
      <Show when={tileMode()}>
        <button
          class="mb-desk tile-add"
          title={[t('Open in a new tile…'), shortcutOf('tile.launcher')].filter(Boolean).join(' · ')}
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect()
            void tileLauncher({ left: r.left, top: r.bottom + 2 })
          }}
          data-testid="tile-launcher"
        >
          <Icon name="split" size={13} />
        </button>
        <button
          class="mb-desk tile-split"
          title={[nextSplit() === 'row' ? t('New tiles open on the right') : t('New tiles open below'), shortcutOf('tile.toggleSplit')].filter(Boolean).join(' · ')}
          onClick={toggleSplit}
          data-testid="tile-split"
          data-split={nextSplit()}
        >
          {nextSplit() === 'row' ? '◨' : '⬓'}
        </button>
        <Show when={resizing()}>
          <span class="mb-resizing" data-testid="tile-resizing">
            {t('Resize: arrows, Shift for more, Esc to end')}
          </span>
        </Show>
      </Show>
    </nav>
  )
}
