// History side bar of the assistant: conversations of the project grouped by date, search,
// rename and delete.
import { createMemo, createSignal, For, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { openChat, runStates } from './agent'
import { chat, chatList, deleteChat, refreshChats, renameChat, resetChat, type ChatInfo } from './state'
import { t } from '../i18n'

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

export function Sidebar(props: { onPicked: () => void; onNew: () => void }) {
  const [q, setQ] = createSignal('')
  onMount(refreshChats)
  const groups = createMemo(() => {
    const query = q().trim().toLowerCase()
    const out: { name: string; items: ChatInfo[] }[] = []
    for (const c of chatList()) {
      if (query && !(c.title || '').toLowerCase().includes(query)) continue
      const g = groupOf(c.updated)
      let last = out[out.length - 1]
      if (!last || last.name !== g) out.push((last = { name: g, items: [] }))
      last.items.push(c)
    }
    return out
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
              <div class="ai-side-group">
                <div class="ai-side-group-name">{g.name}</div>
                <For each={g.items}>
                  {(c) => (
                    <div class="ai-chat-item" classList={{ active: c.id === chat.id }}>
                      <button class="ai-chat-open" onClick={() => open(c.id)} title={c.model ? `${c.title} · ${c.model}` : c.title}>
                        <Show when={runStates()[c.id]}>
                          {(st) => <span class={`ai-run-dot ${st()}`} title={t(runLabels[st()])} data-testid="ai-run-dot" />}
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
