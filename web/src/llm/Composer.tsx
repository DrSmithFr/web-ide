// Message box of the assistant: text that grows with its content, attachments, dictation,
// model picker, options menu, context gauge, send / stop. The draft survives panel switches.
import { createEffect, createMemo, createResource, createSignal, For, on, onCleanup, onMount, Show, type JSX } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { fuzzy } from '../ui/overlay'
import { request } from '../pod/rpc'
import { relPath } from '../state/project'
import { loadPromptContext, promptContext } from './prompt'
import {
  chat,
  config,
  contextSize,
  contextUsed,
  currentModel,
  live,
  loadModels,
  models,
  modelsLoading,
  prefs,
  savePrefs,
  select,
  serverKind,
  resetChat,
  setPrefs,
  type Attachment,
  type Model,
  type Part,
} from './state'
import { compactNow, send, stop, unqueue } from './agent'
import { prepare } from './attachments'
import { cancelRecording, canRecord, modelById, speech, startRecording, stopRecording, transcribe } from './transcribe'
import { AttachmentChip, formatSize, formatTokens, Popover, Switch } from './parts'

const [draft, setDraft] = createSignal('')
const [pending, setPending] = createSignal<{ parts: Part[]; attachment: Attachment }[]>([])
const [preparing, setPreparing] = createSignal(0)
let textareaRef: HTMLTextAreaElement | undefined

/** Converts files (picked, pasted or dropped) into attachments of the next message. */
export async function addFiles(files: Iterable<File>) {
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

export function focusComposer() {
  queueMicrotask(() => textareaRef?.focus())
}

/** Puts a suggestion in the box (not sent). */
export function suggest(text: string) {
  setDraft(text)
  focusComposer()
}


// ---------- commands and mentions ----------

export const commands: { name: string; args?: string; hint: string }[] = [
  { name: 'compact', args: '[consignes]', hint: 'Résume les anciens messages, avec des consignes facultatives' },
  { name: 'clear', hint: 'Nouvelle conversation' },
  { name: 'model', args: '[nom]', hint: 'Change de modèle' },
  { name: 'help', hint: 'Liste des commandes et des skills' },
]

interface CompletionItem {
  kind: 'command' | 'skill' | 'file' | 'dir'
  label: string
  detail?: string
  insert: string
}

let pathsCache: { at: number; list: string[] } | null = null

/** Files and folders of the project, relative to its root (cached 15 s). */
async function projectPaths(): Promise<string[]> {
  if (pathsCache && Date.now() - pathsCache.at < 15_000) return pathsCache.list
  const files = (await request<string[]>('search.files').catch(() => [])).map((f) => relPath(f))
  const dirs = new Set<string>()
  for (const f of files) {
    const parts = f.split('/')
    for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/') + '/')
  }
  const list = [...dirs, ...files]
  pathsCache = { at: Date.now(), list }
  return list
}

/** Runs a /command; returns false when the text is not a command to run here. */
export async function runCommand(text: string, onSettings: () => void): Promise<boolean> {
  const m = /^\/(\S+)\s*([\s\S]*)$/.exec(text)
  if (!m) return false
  const [, name, args] = m
  if (live.busy && ['clear', 'new', 'compact'].includes(name)) {
    toast(`/${name} : disponible une fois la réponse terminée`, 'warn')
    return true
  }
  switch (name) {
    case 'clear':
    case 'new':
      resetChat()
      return true
    case 'compact':
      await compactNow(args.trim())
      return true
    case 'help':
      setHelp(true)
      return true
    case 'model': {
      const q = args.trim().toLowerCase()
      if (!q) {
        toast(`Modèles : ${models().map((x) => x.id).join(', ') || 'aucun'}`, 'info', undefined, 8000)
        return true
      }
      const found = models().find((x) => x.id.toLowerCase() === q) ?? models().find((x) => x.id.toLowerCase().includes(q))
      if (!found) {
        toast(`Aucun modèle « ${args.trim()} »`, 'warn')
        return true
      }
      await select(config.server, found.id)
      toast(`Modèle : ${found.id}`, 'ok')
      return true
    }
  }
  const skill = promptContext()?.skills.find((x) => x.name === name)
  if (skill) {
    if (!config.server || !config.model) {
      onSettings()
      return true
    }
    const ask = `Utilise le skill « ${skill.name} » : charge ses instructions avec load_skill puis applique-les.${args.trim() ? `\n\n${args.trim()}` : ''}`
    await send(ask, [], [], text.trim())
    return true
  }
  toast(`Commande inconnue : /${name} (voir /help)`, 'warn')
  return true
}

const [help, setHelp] = createSignal(false)

function HelpCard() {
  return (
    <div class="ai-help" data-testid="ai-help">
      <div class="ai-help-head">
        <strong>Commandes</strong>
        <span class="grow" />
        <button class="icon-btn small" title="Fermer" onClick={() => setHelp(false)}>
          <Icon name="close" size={12} />
        </button>
      </div>
      <For each={commands}>
        {(c) => (
          <div class="ai-help-row">
            <code>
              /{c.name}
              {c.args ? ` ${c.args}` : ''}
            </code>
            <span>{c.hint}</span>
          </div>
        )}
      </For>
      <Show when={promptContext()?.skills.length}>
        <strong class="ai-help-sub">Skills</strong>
        <For each={promptContext()!.skills}>
          {(sk) => (
            <div class="ai-help-row">
              <code>/{sk.name} [demande]</code>
              <span>{sk.description}</span>
            </div>
          )}
        </For>
      </Show>
      <div class="ai-help-foot muted">@chemin désigne un fichier ou un dossier du projet (autocomplétion en tapant @).</div>
    </div>
  )
}

function capsText(m: Model) {
  const c: string[] = []
  if (m.caps.vision) c.push('image')
  if (m.caps.video) c.push('vidéo')
  if (m.caps.audio) c.push('audio')
  if (m.caps.tools) c.push('outils')
  if (m.caps.thinking) c.push('réflexion')
  return c
}

function ModelPicker(props: { onSettings: () => void }) {
  const [filter, setFilter] = createSignal('')
  const shown = () => {
    const q = filter().toLowerCase()
    return models().filter((m) => !q || m.id.toLowerCase().includes(q))
  }
  const label = () => config.model || (config.servers.length ? 'Choisir un modèle' : 'Aucun serveur')
  return (
    <Popover
      align="right"
      class="ai-model-pop"
      trigger={(toggle, open) => (
        <button class="ai-pill" classList={{ open }} onClick={toggle} title="Modèle" data-testid="model-pill">
          <Show when={currentModel()?.state === 'loaded'}>
            <span class="ai-loaded-dot" />
          </Show>
          <span class="ellipsis">{label()}</span>
          <span class="ai-chev open-up">
            <Icon name="chevron" size={10} />
          </span>
        </button>
      )}
    >
      {(close) => (
        <>
          <div class="ai-pop-head">
            <select
              class="small grow"
              value={config.server}
              title="Serveur"
              onChange={(e) => {
                const v = e.currentTarget.value
                if (v === '+') {
                  close()
                  props.onSettings()
                  return
                }
                select(v, '').catch(errorToast)
              }}
            >
              <Show when={!config.server}>
                <option value="">— serveur —</option>
              </Show>
              <For each={config.servers}>{(s) => <option value={s.id}>{s.name}</option>}</For>
              <option value="+">Gérer les serveurs…</option>
            </select>
            <button class="icon-btn small" title={`Recharger la liste${serverKind() ? ` (${serverKind() === 'ollama' ? 'Ollama' : 'llama.cpp'})` : ''}`} onClick={() => loadModels()}>
              <Icon name="refresh" size={13} />
            </button>
          </div>
          <Show when={models().length > 8}>
            <input class="ai-pop-filter" placeholder="Filtrer les modèles…" value={filter()} onInput={(e) => setFilter(e.currentTarget.value)} />
          </Show>
          <div class="ai-model-list">
            <Show when={shown().length} fallback={<div class="muted small pad">{modelsLoading() ? 'Chargement…' : 'Aucun modèle'}</div>}>
              <For each={shown()}>
                {(m) => (
                  <button
                    class="ai-model-item"
                    classList={{ active: m.id === config.model }}
                    onClick={() => {
                      select(config.server, m.id).catch(errorToast)
                      close()
                    }}
                  >
                    <span class="ai-model-check">{m.id === config.model ? '✓' : ''}</span>
                    <span class="grow">
                      <span class="ai-model-name">
                        {m.id}
                        <Show when={m.state === 'loaded'}>
                          <span class="ai-loaded-dot" title="Chargé" />
                        </Show>
                      </span>
                      <span class="ai-model-meta">
                        {[m.details, m.context ? `${formatTokens(m.context)} de contexte` : '', m.size ? formatSize(m.size) : ''].filter(Boolean).join(' · ')}
                      </span>
                      <span class="ai-model-caps">
                        <For each={capsText(m)}>{(c) => <span class="badge">{c}</span>}</For>
                        <Show when={!m.caps.known}>
                          <span class="badge" title="Capacités connues une fois le modèle chargé">?</span>
                        </Show>
                      </span>
                    </span>
                  </button>
                )}
              </For>
            </Show>
          </div>
        </>
      )}
    </Popover>
  )
}

function ContextMenu() {
  const ctx = () => contextSize()
  const used = () => contextUsed()
  const ratio = () => (ctx() ? Math.min(1, used() / ctx()) : 0)
  const level = () => (ratio() > prefs.compactAt / 100 ? 'danger' : ratio() > (prefs.compactAt / 100) * 0.85 ? 'warn' : '')
  const active = () => chat.messages.filter((m) => !m.compacted).length
  const compacted = () => chat.messages.filter((m) => m.compacted).length
  const r = 7
  const c = 2 * Math.PI * r
  return (
    <Popover
      align="right"
      class="ai-ctx-pop"
      trigger={(toggle, open) => (
        <button
          class={`ai-ring ${level()}`}
          classList={{ on: open }}
          onClick={toggle}
          title={ctx() ? `Contexte : ${used().toLocaleString()} / ${ctx().toLocaleString()} jetons (${Math.round(ratio() * 100)} %)` : 'Contexte : taille inconnue'}
          data-testid="ai-gauge"
        >
          <svg width="18" height="18" viewBox="0 0 18 18">
            <circle cx="9" cy="9" r={r} class="ai-ring-bg" />
            <circle cx="9" cy="9" r={r} class="ai-ring-fg" stroke-dasharray={`${c * ratio()} ${c}`} transform="rotate(-90 9 9)" />
          </svg>
        </button>
      )}
    >
      {(close) => (
        <div class="ai-ctx" data-testid="ai-context-menu">
          <div class="ai-ctx-title">Contexte</div>
          <Show when={ctx()} fallback={<p class="muted small">Taille de contexte inconnue : elle est connue une fois le modèle chargé. Environ {formatTokens(used())} jetons utilisés.</p>}>
            <div class="ai-ctx-num">
              <strong>{formatTokens(used())}</strong> / {formatTokens(ctx())} jetons <span class="muted">· {Math.round(ratio() * 100)} %</span>
            </div>
            <div class={`ai-ctx-bar ${level()}`}>
              <span style={{ width: `${ratio() * 100}%` }} />
              <i style={{ left: `${prefs.compactAt}%` }} title={`Seuil de compaction : ${prefs.compactAt} %`} />
            </div>
          </Show>
          <div class="ai-ctx-rows small">
            <span>Messages envoyés au modèle</span>
            <span>{active()}</span>
            <Show when={compacted()}>
              <span>Messages compactés</span>
              <span>{compacted()}</span>
            </Show>
          </div>
          <div class="ai-pop-sep" />
          <Switch label="Compaction automatique" hint={`Au-delà de ${prefs.compactAt} % du contexte`} checked={prefs.autoCompact} onChange={(v) => (setPrefs('autoCompact', v), savePrefs())} />
          <label class="ai-ctx-range small">
            <span>Seuil</span>
            <input type="range" min="40" max="95" step="5" value={prefs.compactAt} onInput={(e) => (setPrefs('compactAt', Number(e.currentTarget.value)), savePrefs())} />
            <span>{prefs.compactAt} %</span>
          </label>
          <button
            class="btn small primary ai-ctx-compact"
            disabled={live.busy || chat.messages.length < 2}
            onClick={() => {
              close()
              compactNow().catch(errorToast)
            }}
          >
            <Icon name="history" size={13} /> Compacter maintenant
          </button>
          <p class="muted small ai-ctx-tip">Ou tapez /compact suivi de consignes pour le résumé.</p>
        </div>
      )}
    </Popover>
  )
}

function Options() {
  const set = (k: 'tools' | 'autoApply' | 'think', v: boolean) => {
    setPrefs(k, v)
    savePrefs()
  }
  return (
    <Popover
      class="ai-options-pop"
      trigger={(toggle, open) => (
        <button class="ai-icon" classList={{ on: open }} onClick={toggle} title="Options de l’assistant" data-testid="ai-options">
          <Icon name="sliders" size={16} />
        </button>
      )}
    >
      {() => (
        <>
          <Switch label="Outils" hint="Fichiers, recherche, serveurs de langage, consoles" checked={prefs.tools} onChange={(v) => set('tools', v)} testid="opt-tools" />
          <Switch label="Appliquer sans demander" hint="Modifications de fichiers sans confirmation" checked={prefs.autoApply} onChange={(v) => set('autoApply', v)} testid="opt-auto" />
          <Show when={currentModel()?.caps.thinking}>
            <Switch label="Réflexion" hint="Le modèle réfléchit avant de répondre" checked={prefs.think} onChange={(v) => set('think', v)} testid="opt-think" />
          </Show>
        </>
      )}
    </Popover>
  )
}

export function Composer(props: { onSettings: () => void; onSent: () => void }) {
  let fileInput!: HTMLInputElement

  const grow = () => {
    const el = textareaRef
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, Math.round(innerHeight * 0.4))}px`
  }
  createEffect(() => {
    draft()
    queueMicrotask(grow)
  })

  const insertText = (t: string) => {
    if (!t) return
    const el = textareaRef
    const v = draft()
    const start = el?.selectionStart ?? v.length
    const end = el?.selectionEnd ?? v.length
    const before = v.slice(0, start)
    const sep = before && !/\s$/.test(before) ? ' ' : ''
    setDraft(before + sep + t + v.slice(end))
    queueMicrotask(() => {
      el?.focus()
      const pos = (before + sep + t).length
      el?.setSelectionRange(pos, pos)
    })
  }

  const dictate = async () => {
    try {
      if (speech.phase === 'recording') {
        const blob = await stopRecording()
        if (blob) insertText(await transcribe(blob))
      } else if (speech.phase === 'idle') await startRecording()
    } catch (e) {
      toast(`Dictée : ${(e as Error).message}`, 'error')
    }
  }
  onCleanup(cancelRecording)

  const [now, setNow] = createSignal(Date.now())
  const tick = setInterval(() => speech.phase === 'recording' && setNow(Date.now()), 250)
  onCleanup(() => clearInterval(tick))
  const speechLabel = () => {
    switch (speech.phase) {
      case 'recording': {
        const s = Math.max(0, Math.floor((now() - speech.startedAt) / 1000))
        return `Enregistrement ${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')} · cliquer sur le micro pour terminer`
      }
      case 'loading':
        return speech.total
          ? `Téléchargement du modèle ${modelById(prefs.whisperModel).repo.split('/')[1]} : ${Math.round((speech.loaded / speech.total) * 100)} % (${formatSize(speech.loaded)} / ${formatSize(speech.total)})`
          : 'Chargement du modèle de transcription…'
      case 'transcribing':
        return 'Transcription locale…'
    }
    return ''
  }

  // Completion of /commands (at the start) and @paths (anywhere), from the caret.
  const [caret, setCaret] = createSignal(0)
  const [selIndex, setSelIndex] = createSignal(0)
  const [dismissed, setDismissed] = createSignal('')
  onMount(() => {
    if (!promptContext()) loadPromptContext().catch(() => {})
  })
  const trackCaret = () => setCaret(textareaRef?.selectionStart ?? draft().length)
  const query = createMemo(() => {
    const v = draft()
    const before = v.slice(0, caret())
    let m = /^\/(\S*)$/.exec(before)
    if (m) return { mode: 'slash' as const, q: m[1], start: 0, end: caret() }
    m = /(^|\s)@([^\s@]*)$/.exec(before)
    if (m) return { mode: 'mention' as const, q: m[2], start: caret() - m[2].length - 1, end: caret() }
    return null
  })
  const [paths] = createResource(() => (query()?.mode === 'mention' ? true : null), projectPaths)
  const items = createMemo<CompletionItem[]>(() => {
    const qy = query()
    if (!qy) return []
    if (qy.mode === 'slash') {
      const all: CompletionItem[] = [
        ...commands.map((c) => ({ kind: 'command' as const, label: `/${c.name}`, detail: c.hint, insert: `/${c.name} ` })),
        ...(promptContext()?.skills ?? []).map((sk) => ({ kind: 'skill' as const, label: `/${sk.name}`, detail: sk.description, insert: `/${sk.name} ` })),
      ]
      return all
        .map((it) => ({ it, score: fuzzy(qy.q, it.label.slice(1)) }))
        .filter((x) => x.score > 0)
        .sort((a, b) => b.score - a.score)
        .map((x) => x.it)
    }
    return (paths() ?? [])
      .map((p) => ({ p, score: fuzzy(qy.q, p) - (p.endsWith('/') ? 0.5 : 0) }))
      .filter((x) => x.score > 0)
      .sort((a, b) => b.score - a.score || a.p.length - b.p.length)
      .slice(0, 40)
      .map(({ p }) => ({ kind: p.endsWith('/') ? ('dir' as const) : ('file' as const), label: p, insert: p.endsWith('/') ? `@${p}` : `@${p} ` }))
  })
  const queryKey = () => (query() ? `${query()!.mode}:${query()!.start}` : '')
  const completionOpen = () => !!query() && items().length > 0 && dismissed() !== queryKey()
  createEffect(on(items, () => setSelIndex(0)))
  const accept = (it: CompletionItem) => {
    const qy = query()
    if (!qy) return
    const v = draft()
    const next = v.slice(0, qy.start) + it.insert + v.slice(qy.end)
    const pos = qy.start + it.insert.length
    setDraft(next)
    queueMicrotask(() => {
      textareaRef?.focus()
      textareaRef?.setSelectionRange(pos, pos)
      setCaret(pos)
    })
  }

  const canSend = () => (!!draft().trim() || pending().length > 0) && !preparing()

  const submit = async () => {
    const text = draft().trim()
    const atts = pending()
    if (!canSend()) return
    if (live.watching) {
      toast('Réponse en cours dans une autre fenêtre : attendre sa fin pour écrire ici', 'info')
      return
    }
    if (text.startsWith('/') && !atts.length) {
      setDraft('')
      setHelp(false)
      try {
        if (await runCommand(text, props.onSettings)) {
          props.onSent()
          return
        }
      } catch (e) {
        errorToast(e)
        return
      }
    }
    if (!config.server || !config.model) {
      props.onSettings()
      return
    }
    setHelp(false)
    setDraft('')
    setPending([])
    props.onSent()
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
    if (completionOpen()) {
      const n = items().length
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        setSelIndex((i) => (i + (e.key === 'ArrowDown' ? 1 : n - 1)) % n)
        return
      }
      const it = items()[selIndex()]
      const qy = query()!
      // Enter on a token already complete (e.g. "/clear") sends the message.
      const complete = it.insert.trimEnd() === draft().slice(qy.start, qy.end)
      if ((e.key === 'Enter' && !e.shiftKey && !complete) || e.key === 'Tab') {
        e.preventDefault()
        accept(it)
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setDismissed(queryKey())
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      submit()
    } else if (e.key === 'Escape') {
      if (live.busy) {
        e.preventDefault()
        stop()
      }
    } else if (e.key === ' ' && e.ctrlKey && !e.shiftKey && !e.altKey) {
      e.preventDefault()
      dictate()
    }
  }

  const onPaste = (e: ClipboardEvent) => {
    const files = [...(e.clipboardData?.files ?? [])]
    if (files.length) {
      e.preventDefault()
      addFiles(files)
    }
  }

  return (
    <div class="ai-composer-wrap">
      <div class="ai-composer">
        <Show when={chat.queue?.length}>
          <div class="ai-queue" data-testid="ai-queue">
            <div class="ai-queue-title">
              <Icon name="history" size={12} /> En file d’attente : {live.busy ? 'envoyé à la prochaine étape de la réponse' : 'envoyé avec le prochain message'}
            </div>
            <For each={chat.queue}>
              {(q) => (
                <div class="ai-queue-item">
                  <span class="ellipsis">{q.display ?? q.text}</span>
                  <Show when={q.attachments?.length}>
                    <span class="muted small">+{q.attachments!.length} pièce(s) jointe(s)</span>
                  </Show>
                  <span class="grow" />
                  <button
                    class="ai-act"
                    title="Reprendre dans la zone de saisie"
                    onClick={() => {
                      unqueue(q.id)
                      setDraft(q.display ?? q.text)
                      focusComposer()
                    }}
                  >
                    <Icon name="edit" size={12} />
                  </button>
                  <button class="ai-act" title="Retirer de la file" onClick={() => unqueue(q.id)}>
                    <Icon name="close" size={12} />
                  </button>
                </div>
              )}
            </For>
          </div>
        </Show>
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
        <Show when={speech.phase !== 'idle'}>
          <div class="ai-speech small" classList={{ rec: speech.phase === 'recording' }} data-testid="ai-speech">
            {speech.phase === 'recording' ? <span class="rec-dot" /> : <span class="spinner" />}
            <span class="ellipsis">{speechLabel()}</span>
          </div>
        </Show>
        <Show when={help()}>
          <HelpCard />
        </Show>
        <Show when={completionOpen()}>
          <div class="ai-complete" role="listbox" data-testid="ai-complete">
            <For each={items()}>
              {(it, i) => (
                <div
                  class="ai-complete-item"
                  classList={{ active: i() === selIndex() }}
                  role="option"
                  onMouseDown={(e) => {
                    e.preventDefault()
                    accept(it)
                  }}
                  onMouseEnter={() => setSelIndex(i())}
                  ref={(el) => createEffect(() => i() === selIndex() && el.scrollIntoView({ block: 'nearest' }))}
                >
                  <Icon name={it.kind === 'dir' ? 'folder' : it.kind === 'file' ? 'file' : it.kind === 'skill' ? 'puzzle' : 'chevron'} size={13} />
                  <span class="ai-complete-label">{it.label}</span>
                  <Show when={it.detail}>
                    <span class="ai-complete-detail ellipsis">{it.detail}</span>
                  </Show>
                </div>
              )}
            </For>
          </div>
        </Show>
        <textarea
          ref={(el) => (textareaRef = el)}
          rows="1"
          placeholder={live.watching ? 'Réponse en cours dans une autre fenêtre…' : live.busy ? 'Écrire la suite : le message attendra la prochaine étape…' : config.model ? `Message à ${config.model}…` : 'Choisir un modèle pour commencer…'}
          value={draft()}
          onInput={(e) => {
            setDraft(e.currentTarget.value)
            trackCaret()
          }}
          onKeyUp={(e) => (!completionOpen() || !['ArrowDown', 'ArrowUp', 'Enter', 'Tab', 'Escape'].includes(e.key)) && trackCaret()}
          onClick={trackCaret}
          onKeyDown={onKey}
          onPaste={onPaste}
        />
        <div class="ai-composer-bar">
          <button class="ai-icon" title="Joindre des fichiers (image, vidéo, audio, PDF, texte)" onClick={() => fileInput.click()}>
            <Icon name="paperclip" size={16} />
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
            class="ai-icon ai-mic"
            classList={{ rec: speech.phase === 'recording' }}
            title={canRecord() ? (speech.phase === 'recording' ? 'Terminer la dictée (Ctrl+Espace)' : 'Dicter : transcription locale, le son reste sur cette machine (Ctrl+Espace)') : 'Micro indisponible (https ou localhost requis)'}
            disabled={!canRecord() || speech.phase === 'loading' || speech.phase === 'transcribing'}
            onClick={dictate}
          >
            <Icon name="mic" size={16} />
          </button>
          <Options />
          <span class="grow" />
          <Show when={chat.messages.length}>
            <ContextMenu />
          </Show>
          <ModelPicker onSettings={props.onSettings} />
          <Show when={live.busy && !live.watching && canSend()}>
            <button class="ai-send queue" onClick={submit} aria-label="Mettre en file d’attente" title="Mettre en file d’attente (Entrée) : envoyé à la prochaine étape" data-testid="enqueue">
              <Icon name="arrowUp" size={16} />
            </button>
          </Show>
          <Show
            when={live.busy}
            fallback={
              <button class="ai-send" disabled={!canSend()} onClick={submit} aria-label="Envoyer" title="Envoyer (Entrée)" data-testid="send">
                <Icon name="arrowUp" size={16} />
              </button>
            }
          >
            <button class="ai-send stop" onClick={stop} aria-label="Arrêter" title="Arrêter (Échap)" data-testid="stop">
              <span class="ai-stop-square" />
            </button>
          </Show>
        </div>
      </div>
    </div>
  )
}
