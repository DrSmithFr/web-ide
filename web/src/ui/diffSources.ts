// Diff sources (docs/design/plugins.md, *Phase 2*): the diff view is the core's, its two sides
// come from the source of the tab (tab.source; git when unset, as the desks saved before #118):
// the Git plugin gives the index, HEAD and commits; others may come (a ticket, a proposed change).
import { createMemo, createRoot, createSignal, type JSX } from 'solid-js'
import type { TabState } from '../state/project'

export interface DiffSource {
  id: string
  /**
   * Both sides of the tab. work: the text of the buffer of its file when it is open, for a live
   * side (null: read the file from the disk if the side is the working file).
   */
  load(tab: TabState, work: string | null): Promise<{ old: string; neu: string }>
  /** Whether the right side is the working file, which follows the buffer while it is edited. */
  live(tab: TabState): boolean
  /** Bumped when the sides may have changed (reloaded). */
  revision?: () => number
  /** What is compared, shown in the toolbar. */
  label(tab: TabState): string
  /** Buttons of the toolbar (stage, unstage). */
  Actions?: (props: { tab: TabState }) => JSX.Element
}

const [sources, setSources] = createSignal<DiffSource[]>([])
/** The diff sources of the enabled plugins (set by the plugin registry). */
export const setDiffSources = setSources

const byId = createRoot(() => createMemo(() => new Map(sources().map((s) => [s.id, s]))))

/** The source of a diff tab, undefined while its plugin is off. */
export const diffSource = (tab: TabState) => byId().get(tab.source ?? 'git')
