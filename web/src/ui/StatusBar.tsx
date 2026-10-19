// Status bar at the bottom of the window: the desks on the left (where asked: the window-level bar
// of a project window), then on the right the items of what has the focus, given by the plugins
// (plugins/types.ts StatusItem): a file tab (Code: caret, format, language), a tab of a plugin
// view (Kanban: a ticket's status, priority and size) or a tool, in a zone or as a tab. When
// nothing in focus gives items, the bar keeps its row.
import { createMemo, Index, Show } from 'solid-js'
import { activeTab, session } from '../state/project'
import { focusPart } from '../state/focus'
import { shownIn } from '../state/zones'
import { mobileView, phone } from '../state/mobile'
import { pluginStatus } from '../plugins/registry'
import type { StatusItem } from '../plugins/types'
import { Icon } from './icons'
import { contextMenu, type MenuItem } from './overlay'
import { DeskSwitcher } from './DeskSwitcher'

function menuAt(e: MouseEvent, items: MenuItem[]) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.top - 4 }), items)
}

/** The items of the focus: the active tab of the focused pane, or the tool shown in the focused zone. */
function focusedItems(): StatusItem[] {
  const part = focusPart()
  // On a phone, one view shows at a time: the bar is the editor's.
  if (phone() ? mobileView() !== 'editor' : part !== 'editor') {
    const tool = phone() ? null : shownIn(session, part as Exclude<typeof part, 'editor'>)
    return tool ? pluginStatus({ tool }) : []
  }
  const tab = activeTab()
  return tab ? pluginStatus({ tab }) : []
}

export function StatusBar(props: { desks?: boolean }) {
  const items = createMemo(focusedItems)
  return (
    <div class="status-bar" data-testid="status-bar">
      {/* The desks of the project, on the left of the bar (ui/DeskSwitcher.tsx). */}
      <Show when={props.desks}>
        <DeskSwitcher />
      </Show>
      <span class="grow" />
      <Index each={items()}>
        {(it) => (
          <Show
            when={it().action || it().menu}
            fallback={
              <span class="sb-item" data-testid={it().testid} data-status={it().id} title={it().title}>
                {it().text}
              </span>
            }
          >
            <button class="sb-item" data-testid={it().testid} data-status={it().id} title={it().title} onClick={(e) => (it().menu ? menuAt(e, it().menu!()) : it().action!())}>
              <Show when={it().icon}>
                <Icon name={it().icon!} size={12} />{' '}
              </Show>
              {it().text}
            </button>
          </Show>
        )}
      </Index>
    </div>
  )
}
