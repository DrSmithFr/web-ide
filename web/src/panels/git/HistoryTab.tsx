// History tab: commits of the current branch with their graph, as
// `git log --graph --pretty='%h -%d %s (%an %ar - %ad)'`, and the selected commit at the bottom.
import { createEffect, createMemo, createSignal, For, on, Show } from 'solid-js'
import { request } from '../../pod/rpc'
import { mutate, session } from '../../state/project'
import { gitRevision } from '../../state/git'
import { contextMenu } from '../../ui/overlay'
import { errorToast } from '../../ui/toast'
import { fmtAgo, fmtDate, t } from '../../i18n'
import { commitMenuItems, type LogCommit } from './actions'
import { layoutGraph } from './graph'
import { GraphCell } from './GraphCell'
import { CommitDetail } from './CommitDetail'
import { Resizer } from './Resizer'

const PAGE = 100
const MAX_LANES = 16

// Kept while the tool is hidden.
const [selected, setSelected] = createSignal<string | null>(null)
const [query, setQuery] = createSignal('')

/** Refs of a commit (`%D`): the checked out branch, tags, other branches. */
function refsOf(d?: string) {
  return (d ?? '')
    .split(', ')
    .filter(Boolean)
    .map((r) => (r.startsWith('HEAD -> ') ? { name: r.slice(8), kind: 'head' } : r.startsWith('tag: ') ? { name: r.slice(5), kind: 'tag' } : { name: r, kind: r === 'HEAD' ? 'head' : 'branch' }))
}

export function HistoryTab() {
  const [commits, setCommits] = createSignal<LogCommit[]>([])
  const [done, setDone] = createSignal(false)
  const [loading, setLoading] = createSignal(false)
  let list!: HTMLDivElement
  let generation = 0

  const load = async (more: boolean) => {
    const gen = more ? generation : ++generation
    const skip = more ? commits().length : 0
    const n = more ? PAGE : Math.min(500, Math.max(PAGE, commits().length))
    setLoading(true)
    try {
      const page = await request<LogCommit[]>('git.log', { skip, n, query: query() })
      if (gen !== generation) return
      setCommits(more ? [...commits(), ...page] : page)
      setDone(page.length < n)
    } catch (e) {
      if (gen === generation) errorToast(e)
    } finally {
      if (gen === generation) setLoading(false)
    }
  }
  // Reloaded on every change of the repository, and when the search changes (after a pause).
  let timer: number | undefined
  createEffect(on(gitRevision, () => load(false)))
  createEffect(
    on(
      query,
      () => {
        clearTimeout(timer)
        timer = window.setTimeout(() => load(false), 250)
      },
      { defer: true },
    ),
  )

  // A search breaks the chain of parents: no graph then.
  const graph = createMemo(() => (query().trim() ? null : layoutGraph(commits())))
  const lanes = () => Math.min(MAX_LANES, Math.max(1, ...(graph() ?? []).map((r) => r.width)))

  const scrolled = () => {
    if (!done() && !loading() && list.scrollTop + list.clientHeight > list.scrollHeight - 200) load(true)
  }
  const move = (dir: 1 | -1) => {
    const all = commits()
    const i = all.findIndex((c) => c.hash === selected())
    const next = all[Math.max(0, Math.min(all.length - 1, i + dir))]
    if (!next) return
    setSelected(next.hash)
    list.querySelector(`[data-hash="${next.hash}"]`)?.scrollIntoView({ block: 'nearest' })
  }

  return (
    <div class="git-split">
      <div class="git-search">
        <input class="input small" type="search" placeholder={t('Search the history (message, author, hash)')} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
      </div>
      <div
        class="panel-body git-log"
        ref={list}
        tabIndex={0}
        onScroll={scrolled}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') move(e.key === 'ArrowDown' ? 1 : -1)
          else if (e.key === 'Escape') setSelected(null)
          else return
          e.preventDefault()
        }}
      >
        <For each={commits()} fallback={<p class="muted small pad">{loading() ? t('Loading…') : query() ? t('No commit found.') : t('No commit.')}</p>}>
          {(c, i) => (
            <div
              class="git-log-row"
              data-hash={c.hash}
              classList={{ selected: selected() === c.hash }}
              title={`${c.hash}\n${c.author} <${c.email}>\n${fmtDate(c.when * 1000)}`}
              onClick={() => setSelected(c.hash)}
              onContextMenu={(e) => {
                setSelected(c.hash)
                contextMenu(e, commitMenuItems(c))
              }}
            >
              <Show when={graph()?.[i()]}>{(row) => <GraphCell row={row()} lanes={lanes()} merge={c.parents.length > 1} />}</Show>
              <span class="mono git-hash">{c.short}</span>
              <For each={refsOf(c.refs)}>{(r) => <span class={`git-ref ref-${r.kind}`}>{r.name}</span>}</For>
              <span class="git-subject">{c.subject}</span>
              <span class="git-meta">
                ({c.author} {fmtAgo(c.when * 1000)} - {fmtDate(c.when * 1000)})
              </span>
            </div>
          )}
        </For>
        <Show when={loading() && commits().length}>
          <p class="muted small pad">{t('Loading…')}</p>
        </Show>
      </div>
      <Show when={selected()}>
        {(hash) => (
          <>
            <Resizer height={session.git.detail} set={(h) => mutate((s) => (s.git.detail = h))} />
            <div class="git-bottom" style={{ height: `${session.git.detail}px` }}>
              <CommitDetail hash={hash()} select={setSelected} close={() => setSelected(null)} />
            </div>
          </>
        )}
      </Show>
    </div>
  )
}
