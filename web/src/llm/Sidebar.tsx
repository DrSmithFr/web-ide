// History side bar of the assistant: conversations of the project grouped by date, search,
// rename and delete.
import { createMemo, createSignal, For, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { prompt } from '../ui/overlay'
import { errorToast } from '../ui/toast'
import { chat, chatList, deleteChat, live, openChat, refreshChats, renameChat, resetChat, type ChatInfo } from './state'

function groupOf(t: number): string {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  const day = 86_400_000
  if (t >= d.getTime()) return 'Aujourd’hui'
  if (t >= d.getTime() - day) return 'Hier'
  if (t >= d.getTime() - 7 * day) return '7 derniers jours'
  if (t >= d.getTime() - 30 * day) return '30 derniers jours'
  return 'Plus ancien'
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
    if (live.busy) return
    try {
      await openChat(id)
      props.onPicked()
    } catch (e) {
      errorToast(e)
    }
  }
  const rename = async (c: ChatInfo) => {
    const title = await prompt({ title: 'Renommer la conversation', value: c.title })
    if (title?.trim()) await renameChat(c.id, title.trim()).catch(errorToast)
  }
  const remove = async (c: ChatInfo) => {
    if (!confirm(`Supprimer la conversation « ${c.title || 'Sans titre'} » ?`)) return
    await deleteChat(c.id).catch(errorToast)
  }
  return (
    <aside class="ai-sidebar" data-testid="ai-sidebar">
      <div class="ai-side-search">
        <Icon name="search" size={13} />
        <input placeholder="Rechercher" value={q()} onInput={(e) => setQ(e.currentTarget.value)} />
      </div>
      <button
        class="ai-new-chat"
        disabled={live.busy}
        onClick={() => {
          resetChat()
          props.onNew()
        }}
      >
        <Icon name="plus" size={14} /> Nouvelle conversation
      </button>
      <div class="ai-side-list">
        <Show when={groups().length} fallback={<p class="muted small ai-side-empty">{q() ? 'Aucune conversation trouvée.' : 'Aucune conversation pour ce projet.'}</p>}>
          <For each={groups()}>
            {(g) => (
              <div class="ai-side-group">
                <div class="ai-side-group-name">{g.name}</div>
                <For each={g.items}>
                  {(c) => (
                    <div class="ai-chat-item" classList={{ active: c.id === chat.id }}>
                      <button class="ai-chat-open" onClick={() => open(c.id)} title={c.model ? `${c.title} · ${c.model}` : c.title}>
                        <span class="ellipsis">{c.title || 'Sans titre'}</span>
                      </button>
                      <button class="ai-chat-act" title="Renommer" onClick={() => rename(c)}>
                        <Icon name="edit" size={12} />
                      </button>
                      <button class="ai-chat-act" title="Supprimer" onClick={() => remove(c)}>
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
