// History side bar of the assistant: the active conversations first, then the others grouped
// by date; search, rename and delete. A sub-agent is listed under its parent while it works;
// once ended, it goes to the history of the day it ended.
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

const endedStatus = new Set(['done', 'blocked', 'stopped', 'error'])

export function Sidebar(props: { onPicked: () => void; onNew: () => void }) {
  const [q, setQ] = createSignal('')
  onMount(refreshChats)
  const ids = createMemo(() => new Set(chatList().map((c) => c.id)))
  // A sub-agent still working is listed under its parent.
  const underParent = (c: ChatInfo) => !!c.parent && ids().has(c.parent) && !endedStatus.has(c.status ?? '')
  const children = (id: string) => chatList().filter((c) => c.parent === id && underParent(c))
  // Running, waiting for the model server, for the user or for its parent, or with a sub-agent at work.
  const busy = (c: ChatInfo) => !!runStates()[c.id] || c.status === 'waiting_parent'
  const active = (c: ChatInfo) => busy(c) || children(c.id).some(busy)
  const groups = createMemo(() => {
    const query = q().trim().toLowerCase()
    const out: { name: string; items: ChatInfo[] }[] = []
    const add = (name: string, c: ChatInfo) => {
      let g = out.find((x) => x.name === name)
      if (!g) out.push((g = { name, items: [] }))
      g.items.push(c)
    }
    for (const c of chatList()) {
      if (query) {
        if ((c.title || '').toLowerCase().includes(query)) add(groupOf(c.updated), c)
        continue
      }
      if (underParent(c)) continue
      add(active(c) ? t('Active') : groupOf(c.updated), c)
    }
    // The active ones first, the others by date (the list comes most recent first).
    return out.sort((a, b) => (b.name === t('Active') ? 1 : 0) - (a.name === t('Active') ? 1 : 0))
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
  const item = (c: ChatInfo, nested: boolean) => (
    <div class="ai-chat-item" classList={{ active: c.id === chat.id, nested }} data-testid={nested ? 'ai-chat-child' : undefined}>
      <button class="ai-chat-open" onClick={() => open(c.id)} title={c.model ? `${c.title} · ${c.model}` : c.title}>
        <Show when={runStates()[c.id]} fallback={<Show when={c.parent}>{<span class={`ai-agent-dot ${c.status ?? ''}`} title={t(agentLabels[c.status ?? ''] ?? '')} />}</Show>}>
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
              <div class="ai-side-group" data-testid={g.name === t('Active') ? 'ai-side-active' : undefined}>
                <div class="ai-side-group-name">{g.name}</div>
                <For each={g.items}>
                  {(c) => (
                    <>
                      {item(c, false)}
                      <Show when={!q().trim()}>
                        <For each={children(c.id)}>{(k) => item(k, true)}</For>
                      </Show>
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
