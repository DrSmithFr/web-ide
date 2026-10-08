// History side bar of the assistant: the last Orchestrator conversation with its active
// children, then the other active conversations as trees, then the rest by day; search,
// rename and delete. A child is active until it reports or is stopped by hand; an ended child
// goes to the history of the day it ended.
import { createMemo, createSignal, For, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { openChat, runStates } from './agent'
import { chat, chatList, deleteChat, refreshChats, renameChat, resetChat, type ChatInfo } from './state'
import { t } from '../i18n'
import { agentLabels } from './SubAgents'

function groupOf(ms: number): string {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  const day = 86_400_000
  if (ms >= d.getTime()) return t('Today')
  if (ms >= d.getTime() - day) return t('Yesterday')
  if (ms >= d.getTime() - 7 * day) return t('Last 7 days')
  if (ms >= d.getTime() - 30 * day) return t('Last 30 days')
  return t('Older')
}

/** What a conversation running in the pod is doing. */
const runLabels: Record<string, string> = {
  running: 'Running',
  queued: 'Waiting for the model server',
  waiting_user: 'Waiting for you',
  compacting: 'Compacting',
}

// A child has ended once it reported (done, blocked) or was stopped; an error leaves it active.
const endedStatus = new Set(['done', 'blocked', 'stopped'])

interface Group {
  name: string
  testid?: string
  /** Each item with the active children shown under it. */
  items: { c: ChatInfo; kids: { c: ChatInfo; depth: number }[] }[]
}

/** State of a conversation that does not run: a question waiting (orange), an error (red), else the status of a sub-agent. */
function StateDot(props: { c: ChatInfo }) {
  const state = () => (props.c.waiting ? 'waiting' : props.c.failed || props.c.status === 'error' ? 'failed' : props.c.parent ? props.c.status ?? '' : '')
  const labels: Record<string, string> = { waiting: 'Waiting for you', failed: 'Error' }
  return (
    <Show when={state()}>
      <span class={`ai-agent-dot ${state()}`} title={t(labels[state()] ?? agentLabels[state()] ?? '')} data-testid={`ai-dot-${state()}`} />
    </Show>
  )
}

export function Sidebar(props: { onPicked: () => void; onNew: () => void }) {
  const [q, setQ] = createSignal('')
  onMount(refreshChats)
  const groups = createMemo((): Group[] => {
    const list = chatList()
    const query = q().trim().toLowerCase()
    const byDay = (items: ChatInfo[]) => {
      const out: Group[] = []
      for (const c of items) {
        const name = groupOf(c.updated)
        let g = out[out.length - 1]
        if (!g || g.name !== name) out.push((g = { name, items: [] }))
        g.items.push({ c, kids: [] })
      }
      return out
    }
    if (query) return byDay(list.filter((c) => (c.title || '').toLowerCase().includes(query)))
    const ids = new Set(list.map((c) => c.id))
    // A working child is shown under its parent; an orphan stands on its own.
    const working = (c: ChatInfo) => !!c.parent && ids.has(c.parent) && !endedStatus.has(c.status ?? '')
    const kidsOf = (id: string) => list.filter((c) => c.parent === id && working(c))
    const tree = (id: string, depth = 1): { c: ChatInfo; depth: number }[] => kidsOf(id).flatMap((k) => [{ c: k, depth }, ...tree(k.id, depth + 1)])
    const running = (c: ChatInfo) => !!runStates()[c.id] || (!!c.parent && !ids.has(c.parent) && !endedStatus.has(c.status ?? '') && !!c.status)
    const tops = list.filter((c) => !working(c))
    const shown = new Set<string>()
    const take = (c: ChatInfo) => {
      const kids = tree(c.id)
      for (const x of [c, ...kids.map((k) => k.c)]) shown.add(x.id)
      return { c, kids }
    }
    const out: Group[] = []
    const orchestrator = tops.find((c) => c.mode === 'orchestrator')
    if (orchestrator) out.push({ name: t('Orchestrator'), testid: 'ai-side-orchestrator', items: [take(orchestrator)] })
    const active = tops.filter((c) => !shown.has(c.id) && (running(c) || kidsOf(c.id).length > 0))
    if (active.length) out.push({ name: t('Active'), testid: 'ai-side-active', items: active.map(take) })
    return [...out, ...byDay(tops.filter((c) => !shown.has(c.id)))]
  })
  const open = async (id: string) => {
    try {
      await openChat(id)
      props.onPicked()
    } catch (e) {
      errorToast(e)
    }
  }
  const rename = async (c: ChatInfo) => {
    const title = await prompt({ title: t('Rename the conversation'), value: c.title })
    if (title?.trim()) await renameChat(c.id, title.trim()).catch(errorToast)
  }
  const remove = async (c: ChatInfo) => {
    if (!confirm(t('Delete the conversation “{title}”?', { title: c.title || t('Untitled') }))) return
    await deleteChat(c.id).catch(errorToast)
  }
  const item = (c: ChatInfo, depth = 0) => (
    <div class="ai-chat-item" classList={{ active: c.id === chat.id, nested: depth > 0 }} style={depth > 1 ? { 'padding-left': `${depth * 14}px` } : undefined} data-testid={depth ? 'ai-chat-child' : undefined}>
      <button class="ai-chat-open" onClick={() => open(c.id)} title={c.model ? `${c.title} · ${c.model}` : c.title}>
        <Show when={runStates()[c.id]} fallback={<StateDot c={c} />}>
          {(st) => <span class={`ai-run-dot ${st()}`} title={t(runLabels[st()])} data-testid="ai-run-dot" />}
        </Show>
        <Show when={c.mode === 'orchestrator'}>
          <Icon name="locate" size={12} class="ai-side-mode" />
        </Show>
        <span class="ellipsis">{c.title || t('Untitled')}</span>
      </button>
      <button class="ai-chat-act" title={t('Rename')} onClick={() => rename(c)}>
        <Icon name="edit" size={12} />
      </button>
      <button class="ai-chat-act" title={t('Delete')} onClick={() => remove(c)}>
        <Icon name="close" size={12} />
      </button>
    </div>
  )
  return (
    <aside class="ai-sidebar" data-testid="ai-sidebar">
      <div class="ai-side-search">
        <Icon name="search" size={13} />
        <input placeholder={t('Search')} value={q()} onInput={(e) => setQ(e.currentTarget.value)} />
      </div>
      <button
        class="ai-new-chat"
        onClick={() => {
          resetChat()
          props.onNew()
        }}
      >
        <Icon name="plus" size={14} /> {t('New conversation')}
      </button>
      <div class="ai-side-list">
        <Show when={groups().length} fallback={<p class="muted small ai-side-empty">{q() ? t('No conversation found.') : t('No conversation for this project.')}</p>}>
          <For each={groups()}>
            {(g) => (
              <div class="ai-side-group" data-testid={g.testid}>
                <div class="ai-side-group-name">{g.name}</div>
                <For each={g.items}>
                  {(it) => (
                    <>
                      {item(it.c)}
                      <For each={it.kids}>{(k) => item(k.c, k.depth)}</For>
                    </>
                  )}
                </For>
              </div>
            )}
          </For>
        </Show>
      </div>
    </aside>
  )
}
