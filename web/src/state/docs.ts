// The documentation embedded in the pod, shown in `doc` tabs (ui/DocView.tsx): paths are those
// of the repo ("docs/guide.md"), a target is a path with an optional #heading.
import { activateTab, leaves, mutate, openTab, session, type TabState } from './project'
import topics from '../help/topics.json'

export const docsIndex = 'docs/README.md'

/** Places in the interface that have a page: topic → "path#heading" (checked by scripts/check-links.mjs). */
export type HelpTopic = keyof typeof topics

export function splitTarget(target: string): { path: string; anchor?: string } {
  const [path, anchor] = target.split('#')
  return { path, anchor: anchor || undefined }
}

// Pages already left, by tab: what Back returns to.
const history = new Map<string, string[]>()

export function canGoBack(tab: string) {
  return (history.get(tab)?.length ?? 0) > 0
}

function current(tab: TabState) {
  return tab.doc + (tab.anchor ? '#' + tab.anchor : '')
}

/** Shows a page in a doc tab, the one already open before a new one. */
export function openDoc(target: string) {
  const { path, anchor } = splitTarget(target)
  const open = Object.values(session.tabs).find((x) => x.kind === 'doc')
  if (!open) {
    openTab({ kind: 'doc', doc: path, anchor })
    return
  }
  goDoc(open.id, target)
  const leaf = leaves().find((l) => l.tabs.includes(open.id))
  if (leaf) activateTab(leaf.id, open.id)
}

/** Follows a link inside a doc tab. */
export function goDoc(tab: string, target: string) {
  const t = session.tabs[tab]
  if (!t) return
  const { path, anchor } = splitTarget(target)
  if (t.doc !== path || t.anchor !== anchor) history.set(tab, [...(history.get(tab) ?? []), current(t)])
  mutate((s) => {
    s.tabs[tab].doc = path
    s.tabs[tab].anchor = anchor
    s.tabs[tab].title = undefined
  })
}

export function backDoc(tab: string) {
  const list = history.get(tab) ?? []
  const prev = list.pop()
  if (prev === undefined) return
  const { path, anchor } = splitTarget(prev)
  mutate((s) => {
    s.tabs[tab].doc = path
    s.tabs[tab].anchor = anchor
    s.tabs[tab].title = undefined
  })
  history.set(tab, [...list])
}

export function docTarget(topic: HelpTopic) {
  return topics[topic]
}

