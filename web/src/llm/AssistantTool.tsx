// Tool "Assistant IA": chat with a model of a llama.cpp or Ollama server, which can read and
// change the project files and ask the language servers.
import { createEffect, createSignal, For, on, onCleanup, onMount, Show, type JSX } from 'solid-js'
import { Modal } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { openFile, relPath } from '../state/project'
import { request } from '../pod/rpc'
import {
  applyConfig,
  approval,
  chat,
  chatList,
  config,
  currentModel,
  deleteChat,
  live,
  loadConfig,
  loadModels,
  models,
  modelsError,
  modelsLoading,
  openChat,
  prefs,
  refreshChats,
  resetChat,
  savePrefs,
  select,
  serverKind,
  setPrefs,
  type Attachment,
  type ChatMessage,
  type DiffLine,
  type Part,
  type ServerView,
  type ToolCall,
} from './state'
import { retry, send, stop } from './agent'
import { prepare } from './attachments'
import { onMarkdownClick, renderMarkdown, renderMermaid } from './markdown'
import { absPath } from './tools'

function Markdown(props: { text: string; final: boolean }) {
  let el!: HTMLDivElement
  let frame = 0
  createEffect(() => {
    const text = props.text
    const final = props.final
    cancelAnimationFrame(frame)
    // While streaming, one render per frame at most.
    frame = requestAnimationFrame(() => {
      el.innerHTML = renderMarkdown(text)
      if (final) renderMermaid(el).catch(() => {})
    })
  })
  onCleanup(() => cancelAnimationFrame(frame))
  return <div class="md" ref={el} onClick={onMarkdownClick} />
}

function formatSize(n: number) {
  if (n < 1024) return `${n} o`
  if (n < 1 << 20) return `${(n / 1024).toFixed(0)} Ko`
  if (n < 1 << 30) return `${(n / (1 << 20)).toFixed(1)} Mo`
  return `${(n / (1 << 30)).toFixed(1)} Go`
}

function DiffBlock(props: { lines: DiffLine[] }) {
  return (
    <pre class="ai-diff">
      <For each={props.lines}>
        {(l) => (
          <div class={l.t === '+' ? 'add' : l.t === '-' ? 'del' : l.t === '…' ? 'gap' : ''}>
            {l.t === '…' ? `⋯ ${l.text}` : `${l.t} ${l.text}`}
          </div>
        )}
      </For>
    </pre>
  )
}

/** Main argument of a tool call, for its one-line label. */
function callLabel(call: ToolCall | undefined, name: string) {
  let a: any = {}
  try {
    a = JSON.parse(call?.function.arguments || '{}')
  } catch {
    /* shown raw */
  }
  const target = a.path !== undefined ? relPath(absPath(a.path)) || '.' : a.query ?? a.pattern ?? ''
  const extra = a.symbol ? ` · ${a.symbol}${a.line ? ` (l. ${a.line})` : ''}` : a.start_line ? ` · l. ${a.start_line}${a.end_line ? `-${a.end_line}` : ''}` : ''
  return { name, target: String(target), extra }
}

const toolIcons: Record<string, string> = {
  list_dir: 'folder', find_files: 'search', read_file: 'file', search_text: 'search', edit_file: 'edit', write_file: 'edit',
}

function ToolRow(props: { msg: ChatMessage; call?: ToolCall }) {
  const [open, setOpen] = createSignal(false)
  const label = () => callLabel(props.call, props.msg.name ?? '')
  const pending = () => !props.msg.status
  const canOpen = () => {
    const a = props.call ? safeArgs(props.call) : {}
    return typeof a.path === 'string' && props.msg.status === 'ok' && props.msg.name !== 'list_dir'
  }
  return (
    <div class={`ai-tool ${props.msg.status ?? 'running'}`}>
      <button class="ai-tool-head" onClick={() => setOpen(!open())} title="Afficher le résultat">
        <Icon name={toolIcons[props.msg.name ?? ''] ?? 'puzzle'} size={13} />
        <span class="ai-tool-name">{label().name}</span>
        <span class="ai-tool-target ellipsis">
          {label().target}
          {label().extra}
        </span>
        <span class="grow" />
        <span class="ai-tool-sum ellipsis">{pending() ? <span class="spinner" /> : props.msg.summary}</span>
      </button>
      <Show when={props.msg.diff?.length && (open() || props.msg.status === 'ok')}>
        <DiffBlock lines={props.msg.diff!} />
      </Show>
      <Show when={open()}>
        <Show when={canOpen()}>
          <button class="link small" onClick={() => openFile(absPath(safeArgs(props.call!).path))}>
            Ouvrir le fichier
          </button>
        </Show>
        <pre class="ai-tool-out">{typeof props.msg.content === 'string' ? props.msg.content : ''}</pre>
      </Show>
    </div>
  )
}

function safeArgs(call: ToolCall): any {
  try {
    return JSON.parse(call.function.arguments || '{}')
  } catch {
    return {}
  }
}

function UserMessage(props: { msg: ChatMessage }) {
  const text = () => (typeof props.msg.content === 'string' ? props.msg.content : (props.msg.content ?? []).filter((p): p is Extract<Part, { type: 'text' }> => p.type === 'text').slice(0, 1).map((p) => p.text).join(''))
  return (
    <div class="ai-msg user">
      <Show when={props.msg.attachments?.length}>
        <div class="ai-atts">
          <For each={props.msg.attachments}>{(a) => <AttachmentChip a={a} />}</For>
        </div>
      </Show>
      <Show when={text()}>
        <div class="ai-user-text">{text()}</div>
      </Show>
    </div>
  )
}

function AttachmentChip(props: { a: Attachment; onRemove?: () => void }) {
  const icons: Record<string, string> = { image: '🖼', video: '🎞', audio: '🔊', pdf: '📄', text: '📃' }
  return (
    <span class="ai-att" title={`${props.a.name} · ${formatSize(props.a.size)}${props.a.note ? ` · ${props.a.note}` : ''}`}>
      <Show when={props.a.thumb} fallback={<span class="ai-att-icon">{icons[props.a.kind]}</span>}>
        <img src={props.a.thumb} alt="" />
      </Show>
      <span class="ellipsis">{props.a.name}</span>
      <Show when={props.onRemove}>
        <button class="chip-x" title="Retirer" onClick={props.onRemove}>
          ✕
        </button>
      </Show>
    </span>
  )
}

function Reasoning(props: { text: string; live?: boolean }) {
  return (
    <details class="ai-reasoning" open={props.live}>
      <summary>{props.live ? 'Réflexion…' : 'Réflexion'}</summary>
      <div class="ai-reasoning-text">{props.text}</div>
    </details>
  )
}

function AssistantMessage(props: { msg: ChatMessage; index: number }) {
  // Tool results follow the message that asked for them.
  const results = () => {
    const out: ChatMessage[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) out.push(chat.messages[i])
    return out
  }
  const callOf = (m: ChatMessage) => props.msg.tool_calls?.find((c) => c.id === m.tool_call_id)
  const usage = () => props.msg.usage
  return (
    <div class="ai-msg assistant">
      <Show when={props.msg.reasoning_content}>
        <Reasoning text={props.msg.reasoning_content!} />
      </Show>
      <Show when={typeof props.msg.content === 'string' && props.msg.content}>
        <Markdown text={props.msg.content as string} final />
      </Show>
      <For each={results()}>{(m) => <ToolRow msg={m} call={callOf(m)} />}</For>
      <Show when={props.msg.error}>
        <div class="ai-error">{props.msg.error}</div>
      </Show>
      <Show when={usage()}>
        <div class="ai-usage">
          {usage()!.prompt.toLocaleString()} → {usage()!.completion.toLocaleString()} jetons
          {usage()!.cached ? ` · ${usage()!.cached!.toLocaleString()} en cache` : ''}
          {usage()!.perSecond ? ` · ${usage()!.perSecond!.toFixed(1)} jetons/s` : ''}
          {usage()!.durationMs ? ` · ${(usage()!.durationMs! / 1000).toFixed(1)} s` : ''}
        </div>
      </Show>
    </div>
  )
}

function ApprovalCard() {
  const a = () => approval()!
  return (
    <div class="ai-approval" data-testid="ai-approval">
      <div class="ai-approval-head">
        <Icon name="edit" size={14} />
        <strong>{a().created ? 'Créer' : 'Modifier'}</strong>
        <span class="mono ellipsis">{relPath(a().path)}</span>
      </div>
      <DiffBlock lines={a().diff} />
      <div class="form-actions">
        <label class="check small">
          <input
            type="checkbox"
            onChange={(e) => {
              setPrefs('autoApply', e.currentTarget.checked)
              savePrefs()
            }}
          />
          Ne plus demander
        </label>
        <span class="grow" />
        <button class="btn" onClick={() => a().resolve(false)}>
          Refuser
        </button>
        <button class="btn primary" onClick={() => a().resolve(true)}>
          Appliquer
        </button>
      </div>
    </div>
  )
}

function ServersModal(props: { onClose: () => void }) {
  const blank = { id: '', name: '', kind: 'auto' as ServerView['kind'], url: '', apiKey: '', context: 0, hasKey: false, clearKey: false }
  const [form, setForm] = createSignal({ ...blank })
  const [busy, setBusy] = createSignal(false)
  const edit = (s: ServerView) => setForm({ ...blank, ...s, apiKey: '', context: s.context ?? 0 })
  const field = (k: keyof ReturnType<typeof form>) => (e: Event) => setForm({ ...form(), [k]: (e.currentTarget as HTMLInputElement).value })
  const save = async (e: Event) => {
    e.preventDefault()
    setBusy(true)
    try {
      const f = form()
      const view = await request('llm.server.save', { id: f.id, name: f.name.trim(), kind: f.kind, url: f.url, apiKey: f.apiKey, context: Number(f.context) || 0, clearKey: f.clearKey })
      applyConfig(view)
      const saved = f.id ? view.servers.find((s: ServerView) => s.id === f.id) : view.servers[view.servers.length - 1]
      setForm({ ...blank })
      if (saved && !config.server) await select(saved.id, '')
      else if (saved?.id === config.server) await loadModels()
      toast('Serveur enregistré', 'ok')
    } catch (err) {
      errorToast(err)
    } finally {
      setBusy(false)
    }
  }
  const remove = async (s: ServerView) => {
    if (!confirm(`Supprimer le serveur « ${s.name} » ?`)) return
    try {
      applyConfig(await request('llm.server.delete', { id: s.id }))
      if (config.server === s.id) await select('', '')
    } catch (err) {
      errorToast(err)
    }
  }
  return (
    <Modal title="Serveurs de modèles" onClose={props.onClose} class="ai-servers">
      <div class="form">
        <For each={config.servers} fallback={<p class="muted">Aucun serveur. Ajoutez llama.cpp (llama-server) ou Ollama ci-dessous.</p>}>
          {(s) => (
            <div class="ai-server-row">
              <div class="grow">
                <strong>{s.name}</strong>
                <div class="muted small mono">
                  {s.url} · {s.kind === 'auto' ? 'détection auto' : s.kind === 'ollama' ? 'Ollama' : 'llama.cpp / OpenAI'}
                  {s.hasKey ? ' · clé API' : ''}
                </div>
              </div>
              <button class="btn small" onClick={() => edit(s)}>
                Modifier
              </button>
              <button class="btn small danger" onClick={() => remove(s)}>
                Supprimer
              </button>
            </div>
          )}
        </For>
        <form class="fieldset" onSubmit={save}>
          <legend>{form().id ? 'Modifier le serveur' : 'Ajouter un serveur'}</legend>
          <div class="field-row">
            <label class="field grow">
              <span>Adresse (IP:port ou URL)</span>
              <input value={form().url} onInput={field('url')} placeholder="127.0.0.1:8080" required name="url" />
            </label>
            <label class="field">
              <span>Type</span>
              <select value={form().kind} onChange={field('kind')} name="kind">
                <option value="auto">Détection auto</option>
                <option value="llamacpp">llama.cpp / OpenAI</option>
                <option value="ollama">Ollama</option>
              </select>
            </label>
          </div>
          <div class="field-row">
            <label class="field grow">
              <span>Nom (facultatif)</span>
              <input value={form().name} onInput={field('name')} name="name" />
            </label>
            <label class="field grow">
              <span>Clé API (facultative)</span>
              <input type="password" value={form().apiKey} onInput={field('apiKey')} placeholder={form().hasKey ? 'inchangée' : ''} autocomplete="off" name="apiKey" />
            </label>
          </div>
          <Show when={form().kind !== 'llamacpp'}>
            <label class="field">
              <span>Contexte demandé à Ollama (num_ctx, 0 = défaut du modèle)</span>
              <input type="number" min="0" step="1024" value={form().context} onInput={field('context')} class="w-next" name="context" />
            </label>
          </Show>
          <Show when={form().hasKey}>
            <label class="check small">
              <input type="checkbox" checked={form().clearKey} onChange={(e) => setForm({ ...form(), clearKey: e.currentTarget.checked })} />
              Supprimer la clé enregistrée
            </label>
          </Show>
          <div class="form-actions">
            <Show when={form().id}>
              <button type="button" class="btn" onClick={() => setForm({ ...blank })}>
                Annuler
              </button>
            </Show>
            <button class="btn primary" disabled={busy()}>
              {form().id ? 'Enregistrer' : 'Ajouter'}
            </button>
          </div>
        </form>
      </div>
    </Modal>
  )
}

function History(props: { onClose: () => void }) {
  onMount(refreshChats)
  return (
    <div class="ai-history">
      <For each={chatList()} fallback={<p class="muted pad">Aucune conversation enregistrée pour ce projet.</p>}>
        {(c) => (
          <div class="ai-history-row" classList={{ active: c.id === chat.id }}>
            <button
              class="ai-history-open"
              onClick={async () => {
                try {
                  await openChat(c.id)
                  props.onClose()
                } catch (e) {
                  errorToast(e)
                }
              }}
            >
              <span class="ellipsis">{c.title || 'Sans titre'}</span>
              <span class="muted small nowrap">{new Date(c.updated).toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' })}</span>
            </button>
            <button class="icon-btn small" title="Supprimer" onClick={() => deleteChat(c.id).catch(errorToast)}>
              <Icon name="close" size={12} />
            </button>
          </div>
        )}
      </For>
    </div>
  )
}

function CapsBadges() {
  const m = () => currentModel()
  return (
    <Show when={m()}>
      <span class="ai-caps">
        <Show when={m()!.caps.vision}>
          <span class="badge" title="Lit les images">image</span>
        </Show>
        <Show when={m()!.caps.video}>
          <span class="badge" title="Lit la vidéo">vidéo</span>
        </Show>
        <Show when={m()!.caps.audio}>
          <span class="badge" title="Écoute l’audio">audio</span>
        </Show>
        <Show when={m()!.caps.tools}>
          <span class="badge" title="Appels d’outils">outils</span>
        </Show>
        <Show when={!m()!.caps.known}>
          <span class="badge" title="Modèle non chargé : capacités inconnues avant le premier message">?</span>
        </Show>
      </span>
    </Show>
  )
}

export function AssistantTool() {
  const [view, setView] = createSignal<'chat' | 'history'>('chat')
  const [servers, setServers] = createSignal(false)
  const [input, setInput] = createSignal('')
  const [pending, setPending] = createSignal<{ parts: Part[]; attachment: Attachment }[]>([])
  const [preparing, setPreparing] = createSignal(0)
  const [dragging, setDragging] = createSignal(false)
  let list!: HTMLDivElement
  let textarea!: HTMLTextAreaElement
  let fileInput!: HTMLInputElement
  let stick = true

  onMount(() => {
    loadConfig().catch(errorToast)
    refreshChats()
    queueMicrotask(() => textarea?.focus())
  })

  // Follow the answer while the view is at the bottom.
  const onScroll = () => {
    stick = list.scrollHeight - list.scrollTop - list.clientHeight < 40
  }
  createEffect(
    on(
      () => [chat.messages.length, live.content, live.reasoning, live.tool, approval()],
      () => {
        if (stick) requestAnimationFrame(() => list && (list.scrollTop = list.scrollHeight))
      },
    ),
  )
  createEffect(on(() => chat.id, () => (stick = true)))

  const addFiles = async (files: Iterable<File>) => {
    for (const f of files) {
      setPreparing((n) => n + 1)
      try {
        const p = await prepare(f, currentModel()?.caps)
        if (p.warning) toast(`${f.name} : ${p.warning}`, 'warn')
        setPending([...pending(), p])
      } catch (e) {
        toast(`${f.name} : ${(e as Error).message}`, 'error')
      } finally {
        setPreparing((n) => n - 1)
      }
    }
  }

  const submit = async () => {
    const text = input().trim()
    const atts = pending()
    if ((!text && !atts.length) || live.busy || preparing()) return
    if (!config.server) {
      setServers(true)
      return
    }
    setInput('')
    setPending([])
    stick = true
    try {
      await send(
        text,
        atts.flatMap((a) => a.parts),
        atts.map((a) => a.attachment),
      )
    } catch (e) {
      errorToast(e)
    }
  }

  const onKey: JSX.EventHandler<HTMLTextAreaElement, KeyboardEvent> = (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      submit()
    } else if (e.key === 'Escape' && live.busy) {
      e.preventDefault()
      stop()
    }
  }

  const onPaste = (e: ClipboardEvent) => {
    const files = [...(e.clipboardData?.files ?? [])]
    if (files.length) {
      e.preventDefault()
      addFiles(files)
    }
  }

  const lastFailed = () => {
    const m = chat.messages[chat.messages.length - 1]
    return !live.busy && m && (m.role === 'user' || !!m.error)
  }

  return (
    <div class="panel ai-panel">
      <div class="panel-head">
        <span class="panel-title">Assistant IA</span>
        <span class="grow" />
        <button class="icon-btn" title="Nouvelle conversation" disabled={live.busy} onClick={() => (resetChat(), setView('chat'), textarea?.focus())}>
          <Icon name="plus" size={14} />
        </button>
        <button class="icon-btn" classList={{ on: view() === 'history' }} title="Conversations du projet" onClick={() => setView(view() === 'history' ? 'chat' : 'history')}>
          <Icon name="history" size={14} />
        </button>
        <button class="icon-btn" title="Serveurs de modèles" onClick={() => setServers(true)}>
          <Icon name="gear" size={14} />
        </button>
      </div>
      <div class="ai-modelbar">
        <select
          class="small"
          title="Serveur"
          value={config.server}
          onChange={(e) => {
            if (e.currentTarget.value === '+') {
              e.currentTarget.value = config.server
              setServers(true)
              return
            }
            select(e.currentTarget.value, '').catch(errorToast)
          }}
        >
          <Show when={!config.server}>
            <option value="">— serveur —</option>
          </Show>
          <For each={config.servers}>{(s) => <option value={s.id}>{s.name}</option>}</For>
          <option value="+">Ajouter un serveur…</option>
        </select>
        <select class="small grow" title="Modèle" value={config.model} disabled={!models().length} onChange={(e) => select(config.server, e.currentTarget.value).catch(errorToast)}>
          <Show when={!models().length}>
            <option value="">{modelsLoading() ? 'chargement…' : 'aucun modèle'}</option>
          </Show>
          <For each={models()}>
            {(m) => (
              <option value={m.id}>
                {m.state === 'loaded' ? '● ' : ''}
                {m.id}
                {m.details ? ` · ${m.details}` : ''}
              </option>
            )}
          </For>
        </select>
        <button class="icon-btn small" title={`Recharger la liste des modèles${serverKind() ? ` (${serverKind() === 'ollama' ? 'Ollama' : 'llama.cpp'})` : ''}`} disabled={!config.server} onClick={() => loadModels()}>
          <Icon name="refresh" size={13} />
        </button>
      </div>
      <Show when={modelsError()}>
        <div class="ai-banner danger small">{modelsError()}</div>
      </Show>
      <Show when={view() === 'history'}>
        <div class="panel-body">
          <History onClose={() => setView('chat')} />
        </div>
      </Show>
      <Show when={view() === 'chat'}>
        <div
          class="panel-body ai-messages"
          classList={{ dragging: dragging() }}
          ref={list}
          onScroll={onScroll}
          onDragOver={(e) => {
            if (e.dataTransfer?.types.includes('Files')) {
              e.preventDefault()
              setDragging(true)
            }
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            setDragging(false)
            if (e.dataTransfer?.files.length) {
              e.preventDefault()
              addFiles(e.dataTransfer.files)
            }
          }}
        >
          <Show when={!chat.messages.length && !live.busy}>
            <div class="ai-empty muted">
              <p>
                Posez une question sur le projet. L’assistant peut lire et modifier les fichiers, chercher dans le code et interroger les serveurs de langage.
              </p>
              <p class="small">Images, vidéos, audio et PDF : bouton trombone, glisser-déposer ou coller. Entrée envoie, Maj+Entrée va à la ligne.</p>
              <Show when={!config.servers.length}>
                <button class="btn primary" onClick={() => setServers(true)}>
                  Ajouter un serveur de modèles
                </button>
              </Show>
            </div>
          </Show>
          <For each={chat.messages}>
            {(m, i) => (
              <Show when={m.role !== 'tool'}>
                <Show when={m.role === 'user'} fallback={<AssistantMessage msg={m} index={i()} />}>
                  <UserMessage msg={m} />
                </Show>
              </Show>
            )}
          </For>
          <Show when={live.busy && !approval()}>
            <div class="ai-msg assistant live">
              <Show when={live.reasoning}>
                <Reasoning text={live.reasoning} live={!live.content} />
              </Show>
              <Show when={live.content}>
                <Markdown text={live.content} final={false} />
              </Show>
              <div class="ai-live muted small">
                <span class="spinner" />
                {live.tool ? `Prépare l’appel à ${live.tool}…` : live.content ? 'Écrit…' : live.reasoning ? 'Réfléchit…' : 'En attente du modèle…'}
              </div>
            </div>
          </Show>
          <Show when={approval()}>
            <ApprovalCard />
          </Show>
          <Show when={lastFailed()}>
            <div class="ai-retry">
              <button class="btn small" onClick={() => retry().catch(errorToast)}>
                <Icon name="refresh" size={12} /> Relancer
              </button>
            </div>
          </Show>
        </div>
        <div class="ai-composer">
          <Show when={pending().length || preparing()}>
            <div class="ai-atts">
              <For each={pending()}>{(p, i) => <AttachmentChip a={p.attachment} onRemove={() => setPending(pending().filter((_, j) => j !== i()))} />}</For>
              <Show when={preparing()}>
                <span class="ai-att muted">
                  <span class="spinner" /> préparation…
                </span>
              </Show>
            </div>
          </Show>
          <textarea
            ref={textarea}
            rows="3"
            placeholder={config.model ? `Message à ${config.model}…` : 'Choisir un modèle…'}
            value={input()}
            onInput={(e) => setInput(e.currentTarget.value)}
            onKeyDown={onKey}
            onPaste={onPaste}
          />
          <div class="ai-composer-bar">
            <button class="icon-btn" title="Joindre des fichiers (image, vidéo, audio, PDF, texte)" onClick={() => fileInput.click()}>
              📎
            </button>
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                addFiles([...(e.currentTarget.files ?? [])])
                e.currentTarget.value = ''
              }}
            />
            <button
              class="toggle"
              classList={{ on: prefs.tools }}
              title="Autoriser l’assistant à utiliser les outils (fichiers, recherche, serveurs de langage)"
              onClick={() => (setPrefs('tools', !prefs.tools), savePrefs())}
            >
              outils
            </button>
            <button class="toggle" classList={{ on: prefs.autoApply }} title="Appliquer les modifications de fichiers sans demander" onClick={() => (setPrefs('autoApply', !prefs.autoApply), savePrefs())}>
              auto
            </button>
            <Show when={currentModel()?.caps.thinking}>
              <button class="toggle" classList={{ on: prefs.think }} title="Laisser le modèle réfléchir avant de répondre" onClick={() => (setPrefs('think', !prefs.think), savePrefs())}>
                réflexion
              </button>
            </Show>
            <CapsBadges />
            <span class="grow" />
            <Show
              when={live.busy}
              fallback={
                <button class="btn small primary" disabled={(!input().trim() && !pending().length) || !!preparing()} onClick={submit}>
                  Envoyer
                </button>
              }
            >
              <button class="btn small" onClick={stop} title="Arrêter (Échap)">
                <Icon name="stop" size={12} /> Arrêter
              </button>
            </Show>
          </div>
        </div>
      </Show>
      <Show when={servers()}>
        <ServersModal onClose={() => setServers(false)} />
      </Show>
    </div>
  )
}
