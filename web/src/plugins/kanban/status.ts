// The items of the status bar for a ticket view in focus: its status, priority and size, each
// changed through its menu (the priority and the size only before the development, as in the view).
import type { TabState } from '../api'
import type { StatusItem } from '../types'
import { moveTo } from './actions'
import { ensureBoard, getTicket, priorityLabels, sizeNames, statusLabels, statusOrder, summary, updateTicket, type Priority, type Size, type Status } from './state'
import { errorToast, t } from './plugin'

const apply = (p: Promise<unknown>) => p.then(() => undefined, errorToast)

export function viewStatus(tab: TabState): StatusItem[] {
  const id = (tab.state as { ticket?: number } | undefined)?.ticket
  ensureBoard()
  const tk = id ? summary(id) : undefined
  if (!tk) return []
  const editable = tk.status === 'new' || tk.status === 'todo'
  const fixed = editable ? undefined : t('Fixed once the development started')
  return [
    {
      id: 'ticket-status',
      testid: 'status-ticket-status',
      text: statusLabels[tk.status],
      title: t('Status'),
      menu: () =>
        statusOrder.map((s: Status) => ({
          label: statusLabels[s],
          checked: s === tk.status,
          disabled: s === tk.status,
          action: () => void getTicket(tk.id).then((full) => moveTo(full, s, apply), errorToast),
        })),
    },
    {
      id: 'ticket-priority',
      testid: 'status-ticket-priority',
      text: t('{priority} priority', { priority: priorityLabels[tk.priority] }),
      title: fixed ?? t('Priority'),
      menu: () =>
        (Object.keys(priorityLabels) as Priority[]).map((p) => ({
          label: t('{priority} priority', { priority: priorityLabels[p] }),
          checked: p === tk.priority,
          disabled: !editable,
          action: () => void apply(updateTicket(tk.id, { priority: p })),
        })),
    },
    {
      id: 'ticket-size',
      testid: 'status-ticket-size',
      text: tk.size ? t('Size {size}', { size: sizeNames[tk.size] }) : t('Size not estimated'),
      title: fixed ?? t('Estimated size, written with the plan'),
      menu: () => [
        { label: t('Size not estimated'), checked: !tk.size, disabled: !editable, action: () => void apply(updateTicket(tk.id, { size: '' })) },
        ...(Object.keys(sizeNames) as Size[]).map((s) => ({
          label: t('Size {size}', { size: sizeNames[s] }),
          checked: s === tk.size,
          disabled: !editable,
          action: () => void apply(updateTicket(tk.id, { size: s })),
        })),
      ],
    },
  ]
}
