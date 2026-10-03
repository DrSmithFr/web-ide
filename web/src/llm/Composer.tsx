// Message box of the assistant: text that grows with its content, attachments, dictation,
// model picker, options menu, context gauge, send / stop. The draft survives panel switches.
import { createEffect, createSignal, For, onCleanup, Show, type JSX } from 'solid-js'
import { produce } from 'solid-js/store'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
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
  setChat,
  setPrefs,
  type Attachment,
  type Model,
  type Part,
} from './state'
import { compactNow, send, stop } from './agent'
import { prepare } from './attachments'
import { cancelRecording, canRecord, modelById, speech, startRecording, stopRecording, transcribe } from './transcribe'
import { AttachmentChip, formatSize, formatTokens, Popover, Switch } from './parts'

const [draft, setDraft] = createSignal('')
const [editFrom, setEditFrom] = createSignal<number | null>(null)
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

/** Edits a message already sent: the conversation restarts from it when the box is sent. */
export function startEdit(index: number, text: string) {
  setEditFrom(index)
  setDraft(text)
  focusComposer()
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

function ContextRing() {
  const ctx = () => contextSize()
  const used = () => contextUsed()
  const ratio = () => (ctx() ? Math.min(1, used() / ctx()) : 0)
  const r = 7
  const c = 2 * Math.PI * r
  return (
    <span
      class="ai-ring"
      classList={{ warn: ratio() > (prefs.compactAt / 100) * 0.85, danger: ratio() > prefs.compactAt / 100 }}
      title={
        ctx()
          ? `Contexte utilisé (estimation) : ${used().toLocaleString()} / ${ctx().toLocaleString()} jetons (${Math.round(ratio() * 100)} %). Compaction automatique à ${prefs.compactAt} %.`
          : 'Taille de contexte du modèle inconnue (connue une fois le modèle chargé)'
      }
      data-testid="ai-gauge"
    >
      <svg width="18" height="18" viewBox="0 0 18 18">
        <circle cx="9" cy="9" r={r} class="ai-ring-bg" />
        <circle cx="9" cy="9" r={r} class="ai-ring-fg" stroke-dasharray={`${c * ratio()} ${c}`} transform="rotate(-90 9 9)" />
      </svg>
    </span>
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
      {(close) => (
        <>
          <Switch label="Outils" hint="Fichiers, recherche, serveurs de langage, consoles" checked={prefs.tools} onChange={(v) => set('tools', v)} testid="opt-tools" />
          <Switch label="Appliquer sans demander" hint="Modifications de fichiers sans confirmation" checked={prefs.autoApply} onChange={(v) => set('autoApply', v)} testid="opt-auto" />
          <Show when={currentModel()?.caps.thinking}>
            <Switch label="Réflexion" hint="Le modèle réfléchit avant de répondre" checked={prefs.think} onChange={(v) => set('think', v)} testid="opt-think" />
          </Show>
          <div class="ai-pop-sep" />
          <button
            class="ai-menu-item"
            disabled={live.busy || chat.messages.length < 2}
            onClick={() => {
              close()
              compactNow().catch(errorToast)
            }}
          >
            <Icon name="history" size={14} /> Compacter la conversation
          </button>
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

  const canSend = () => (!!draft().trim() || pending().length > 0) && !preparing()

  const submit = async () => {
    const text = draft().trim()
    const atts = pending()
    if (!canSend() || live.busy) return
    if (!config.server || !config.model) {
      props.onSettings()
      return
    }
    const from = editFrom()
    if (from !== null) {
      setChat(produce((c) => c.messages.splice(from)))
      setEditFrom(null)
    }
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
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      submit()
    } else if (e.key === 'Escape') {
      if (live.busy) {
        e.preventDefault()
        stop()
      } else if (editFrom() !== null) {
        e.preventDefault()
        setEditFrom(null)
        setDraft('')
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
      <div class="ai-composer" classList={{ editing: editFrom() !== null }}>
        <Show when={editFrom() !== null}>
          <div class="ai-editing">
            <Icon name="edit" size={12} /> Modification d’un message : la conversation reprendra à partir de lui.
            <span class="grow" />
            <button class="link small" onClick={() => (setEditFrom(null), setDraft(''))}>
              Annuler
            </button>
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
        <textarea
          ref={(el) => (textareaRef = el)}
          rows="1"
          placeholder={config.model ? `Message à ${config.model}…` : 'Choisir un modèle pour commencer…'}
          value={draft()}
          onInput={(e) => setDraft(e.currentTarget.value)}
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
            <ContextRing />
          </Show>
          <ModelPicker onSettings={props.onSettings} />
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
