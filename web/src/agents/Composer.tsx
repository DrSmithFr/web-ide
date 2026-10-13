// Message box of an agent session: the editor of the assistant (MessageBox) with @paths, the
// /commands the agent announces, files, images, doodles and dictation, the settings of the
// agent in its bar. A prompt sent during a turn is queued by the pod. Drafts are kept per
// session while the page lives.
import { createSignal, For, onCleanup, Show } from 'solid-js'
import { createStore, produce } from 'solid-js/store'
import { Icon } from '../ui/icons'
import { contextMenu } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { registerAction, shortcutOf } from '../keys/bindings'
import { t, tn } from '../i18n'
import type { EditorView } from '../editor/view'
import { insertAtCaret, MessageBox, type CompletionItem } from '../llm/MessageBox'
import { prepare, type Prepared } from '../llm/attachments'
import { prepareDoodle } from '../llm/doodle/export'
import { cloneDoc, newDoc, type DoodleDoc } from '../llm/doodle/model'
import { doodleSession, openDoodle } from '../llm/doodle/session'
import { canCapture, captureScreen, type Picture } from '../llm/doodle/background'
import { cancelRecording, canRecord, speech, startRecording, stopRecording, transcribe } from '../llm/transcribe'
import { AttachmentChip, formatTokens, Popover, Switch } from '../llm/parts'
import type { Caps } from '../llm/state'
import { phone } from '../state/mobile'
import { cancelAgent, configAgent, followAgent, promptAgent, sessions, threads, unqueueAgent, type AgentPart, type ConfigOption } from './state'

interface Box {
  draft: string
  pending: Prepared[]
}
const [boxes, setBoxes] = createStore<Record<string, Box>>({})
const box = (id: string): Box => boxes[id] ?? { draft: '', pending: [] }
const setBox = (id: string, f: (b: Box) => void) => setBoxes(produce((all) => f((all[id] ??= { draft: '', pending: [] }))))

/** The message box over the whole session (the thread hidden), as in the assistant. */
export const [agentFull, setAgentFull] = createSignal(false)

/** What the agent reads, in the terms of the attachments of the assistant. */
const capsOf = (id: string): Caps => ({ vision: !!sessions().find((s) => s.id === id)?.caps?.image, video: false, audio: false, tools: true, thinking: false, known: true })

/** The parts of the prepared attachments, as the pod takes them. */
function partsOf(list: Prepared[]): AgentPart[] {
  const out: AgentPart[] = []
  for (const p of list)
    for (const x of p.parts) {
      if (x.type === 'image_url') {
        const m = /^data:([^;]+);base64,(.*)$/.exec(x.image_url.url)
        if (m) out.push({ kind: 'image', name: p.attachment.name, mime: m[1], data: m[2] })
      } else if (x.type === 'text') out.push({ kind: 'text', name: p.attachment.name, text: x.text })
    }
  return out
}

export function AgentComposer(props: { id: string }) {
  let fileInput!: HTMLInputElement
  const [view, setView] = createSignal<EditorView>()
  const [preparing, setPreparing] = createSignal(0)
  const info = () => sessions().find((s) => s.id === props.id)
  const busy = () => info()?.state === 'busy' || info()?.state === 'asking'
  const draft = () => box(props.id).draft
  const pending = () => box(props.id).pending
  const setDraft = (text: string) => setBox(props.id, (b) => (b.draft = text))

  const addFiles = async (files: Iterable<File>) => {
    const id = props.id
    for (const f of files) {
      setPreparing((n) => n + 1)
      try {
        const p = await prepare(f, capsOf(id))
        if (p.warning) toast(`${f.name}: ${p.warning}`, 'warn')
        setBox(id, (b) => b.pending.push(p))
      } catch (e) {
        toast(`${f.name}: ${(e as Error).message}`, 'error')
      } finally {
        setPreparing((n) => n - 1)
      }
    }
  }

  // Doodles: drawn in the host of the project window (the assistant may be closed).
  const attachDoodle = async (doc: DoodleDoc, name: string, replace?: Prepared) => {
    const id = props.id
    const p = await prepareDoodle(doc, name, capsOf(id))
    setBox(id, (b) => {
      const i = replace ? b.pending.indexOf(replace) : -1
      if (i >= 0) b.pending[i] = p
      else b.pending.push(p)
    })
  }
  const doodle = (background?: Picture) => {
    if (doodleSession()) return
    const name = t('Doodle {n}', { n: pending().filter((p) => p.attachment.kind === 'doodle').length + 1 })
    openDoodle({ doc: newDoc(background), name, external: true, onAttach: (doc) => attachDoodle(doc, name) })
  }
  const editDoodle = (p: Prepared) => openDoodle({ doc: cloneDoc(p.attachment.doodle!), name: p.attachment.name, external: true, onAttach: (doc) => attachDoodle(doc, p.attachment.name, p) })
  const screenshot = async () => {
    try {
      doodle(await captureScreen())
    } catch (e) {
      if ((e as Error).name !== 'NotAllowedError' && (e as Error).name !== 'AbortError') errorToast(e)
    }
  }

  const dictate = async () => {
    try {
      if (speech.phase === 'recording') {
        const blob = await stopRecording()
        const v = view()
        if (blob && v) insertAtCaret(v, await transcribe(blob))
      } else if (speech.phase === 'idle') await startRecording()
    } catch (e) {
      toast(t('Dictation: {message}', { message: (e as Error).message }), 'error')
    }
  }
  onCleanup(cancelRecording)

  // The commands the agent announces (its own commands and skills).
  const slashItems = (): CompletionItem[] => (info()?.commands ?? []).map((c) => ({ kind: 'command', label: `/${c.name}`, detail: c.input?.hint ? `${c.description} · ${c.input.hint}` : c.description, insert: `/${c.name} ` }))

  // ↑ / ↓ in an empty box (or one showing a prompt of the history) go through the prompts sent.
  let back = -1
  const history = () => (threads[props.id]?.entries ?? []).filter((e) => e.kind === 'user').map((e) => e.text ?? '')
  const browse = (dir: 1 | -1): boolean => {
    const list = history()
    if (draft() && (back < 0 || draft() !== list[list.length - 1 - back])) return false
    const next = back + dir
    if (next < -1 || next >= list.length) return true
    back = next
    setDraft(back < 0 ? '' : list[list.length - 1 - back])
    return true
  }

  const canSend = () => (!!draft().trim() || pending().length > 0) && !preparing() && info()?.state !== 'error'
  const submit = async () => {
    if (!canSend()) return
    const id = props.id
    const text = draft().trim()
    const atts = pending()
    try {
      await promptAgent(id, text, partsOf(atts))
      setBox(id, (b) => ((b.draft = ''), (b.pending = [])))
      back = -1
    } catch (e) {
      errorToast(e)
    }
  }
  const onKey = (e: KeyboardEvent): boolean => {
    if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) return browse(e.key === 'ArrowUp' ? 1 : -1)
    if (e.key === 'Tab' && e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && modeOption()) {
      const o = modeOption()!
      const list = flatOptions(o)
      const next = list[(list.findIndex((x) => x.value === o.currentValue) + 1) % list.length]
      if (next) void configAgent(props.id, o.id, next.value).catch(errorToast)
      return true
    }
    if (e.key === 'Escape' && agentFull() && !view()?.hasCarets()) {
      setAgentFull(false)
      return true
    }
    if (e.key === 'Escape' && busy()) {
      void cancelAgent(props.id).catch(errorToast)
      return true
    }
    if (e.key === ' ' && e.ctrlKey && !e.shiftKey && !e.altKey) {
      void dictate()
      return true
    }
    return false
  }
  const config = () => (info()?.config ?? []).filter((c) => c.type === 'select' && flatOptions(c).length > 1)
  const modeOption = () => config().find((c) => c.category === 'mode')
  const modelOption = () => config().find((c) => c.category === 'model')
  const otherOptions = () => config().filter((c) => c !== modeOption() && c !== modelOption())
  const off = registerAction('assistant.fullComposer', () => {
    if (!view()?.hasFocus()) return false
    setAgentFull(!agentFull())
  })
  onCleanup(off)

  const placeholder = () => (busy() ? t('Queued message…') : t('Ask the agent… (Ctrl+Enter sends)'))

  return (
    <div class={`ai-composer-wrap agent-composer mode-${modeTone(modeOption()?.currentValue)}`} classList={{ full: agentFull(), busy: busy() }} data-testid="agent-composer">
      <div class="ai-led" />
      <div class="ai-composer">
        <Show when={info()?.queue?.length}>
          <div class="ai-queue" data-testid="agent-queue">
            <div class="ai-queue-title">
              <Icon name="history" size={12} /> {t('Queued: sent at the end of the turn')}
            </div>
            <For each={info()!.queue}>
              {(q, i) => (
                <div class="ai-queue-item">
                  <span class="ellipsis">{q.text}</span>
                  <Show when={q.attachments}>
                    <span class="muted small">+{tn(q.attachments!, '{n} attachment', '{n} attachments')}</span>
                  </Show>
                  <span class="grow" />
                  <button
                    class="ai-act"
                    title={t('Back to the message box')}
                    onClick={() =>
                      void unqueueAgent(props.id, i())
                        .then((r) => (setDraft(r.text), view()?.focus()))
                        .catch(errorToast)
                    }
                  >
                    <Icon name="edit" size={12} />
                  </button>
                  <button class="ai-act" title={t('Remove from the queue')} onClick={() => void unqueueAgent(props.id, i()).catch(errorToast)}>
                    <Icon name="close" size={12} />
                  </button>
                </div>
              )}
            </For>
          </div>
        </Show>
        <Show when={pending().length || preparing()}>
          <div class="ai-atts" data-testid="agent-atts">
            <For each={pending()}>
              {(p, i) => (
                <AttachmentChip
                  a={p.attachment}
                  onOpen={p.attachment.doodle ? () => editDoodle(p) : undefined}
                  onRemove={() => setBox(props.id, (b) => b.pending.splice(i(), 1))}
                />
              )}
            </For>
            <Show when={preparing()}>
              <span class="ai-att muted">
                <span class="spinner" /> {t('preparing…')}
              </span>
            </Show>
          </div>
        </Show>
        <MessageBox
          draft={draft}
          setDraft={setDraft}
          placeholder={placeholder}
          slashItems={slashItems}
          onSubmit={() => void submit()}
          onKey={onKey}
          onFiles={(f) => void addFiles(f)}
          onView={(v) => (setView(v), !phone() && queueMicrotask(() => v.focus()))}
          testid="agent-input"
        />
        <button
          class="ai-act ai-full-btn"
          title={agentFull() ? t('Leave the full screen (Esc)') : `${t('Full screen')} (${shortcutOf('assistant.fullComposer')})`}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => (setAgentFull(!agentFull()), view()?.focus())}
          data-testid="agent-full"
        >
          <Icon name={agentFull() ? 'minimize' : 'maximize'} size={13} />
        </button>
        <div class="ai-composer-bar">
          <Show when={modeOption()}>{(o) => <ModePill id={props.id} option={o()} />}</Show>
          <button
            class="ai-icon ai-mic"
            classList={{ rec: speech.phase === 'recording' }}
            title={canRecord() ? (speech.phase === 'recording' ? t('End the dictation (Ctrl+Space)') : t('Dictate: local transcription, the sound stays on this machine (Ctrl+Space)')) : t('Microphone unavailable (https or localhost required)')}
            disabled={!canRecord() || speech.phase === 'loading' || speech.phase === 'transcribing'}
            onClick={() => void dictate()}
          >
            <Icon name="mic" size={16} />
          </button>
          <button
            class="ai-icon"
            title={t('Attach a file or a doodle')}
            aria-haspopup="menu"
            data-testid="agent-attach"
            onClick={(e) => {
              const r = e.currentTarget.getBoundingClientRect()
              contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.top - 4 }), [
                { label: t('File…'), hint: t('image, video, audio, PDF, text'), action: () => fileInput.click() },
                { label: t('Doodle…'), hint: shortcutOf('assistant.doodle'), action: () => doodle() },
                ...(canCapture() ? [{ label: t('Screenshot…'), hint: t('to annotate'), action: () => void screenshot() }] : []),
              ])
            }}
          >
            <Icon name="paperclip" size={16} />
          </button>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              void addFiles([...(e.currentTarget.files ?? [])])
              e.currentTarget.value = ''
            }}
          />
          <AgentOptions id={props.id} options={otherOptions()} follow={!!info()?.follow} />
          <span class="grow" />
          <Show when={info()?.usage}>{(u) => <Ring used={u().used} size={u().size} />}</Show>
          <Show when={modelOption()}>{(o) => <ModelPill id={props.id} option={o()} />}</Show>
          <Show when={busy() && canSend()}>
            <button class="ai-send queue" onClick={() => void submit()} aria-label={t('Queue')} title={t('Queue: sent at the end of the turn')} data-testid="agent-enqueue">
              <Icon name="arrowUp" size={16} />
            </button>
          </Show>
          <Show
            when={busy()}
            fallback={
              <button class="ai-send" disabled={!canSend()} onClick={() => void submit()} aria-label={t('Send')} title={t('Send (Ctrl+S or Ctrl+Enter)')} data-testid="agent-send">
                <Icon name="arrowUp" size={16} />
              </button>
            }
          >
            <button class="ai-send stop" onClick={() => void cancelAgent(props.id).catch(errorToast)} aria-label={t('Stop')} title={t('Stop (Esc)')} data-testid="agent-stop">
              <span class="ai-stop-square" />
            </button>
          </Show>
        </div>
      </div>
    </div>
  )
}

type Choice = { value: string; name: string; description?: string }
const flatOptions = (o: ConfigOption): Choice[] => (o.options ?? []).flatMap((x: any) => (x.options ? x.options : [x]))
const nameOf = (o: ConfigOption) => flatOptions(o).find((x) => x.value === o.currentValue)?.name ?? o.currentValue

/** The tone of a mode, for its color: plan, automatic (edits accepted), bypass or manual. */
function modeTone(value = ''): 'plan' | 'auto' | 'bypass' | 'manual' {
  const v = value.toLowerCase()
  if (v.includes('plan')) return 'plan'
  if (/bypass|yolo|dontask|full/.test(v)) return 'bypass'
  if (/accept|auto/.test(v)) return 'auto'
  return 'manual'
}
const toneIcons = { plan: 'outline', auto: 'check', bypass: 'warning', manual: 'edit' } as const

/** The choices of an option in a popover, the current one checked. */
function ChoiceList(props: { id: string; option: ConfigOption; close: () => void }) {
  return (
    <div class="ai-model-list" role="listbox" data-testid={`agent-config-${props.option.id}-list`}>
      <For each={flatOptions(props.option)}>
        {(c) => (
          <button
            class="ai-model-item"
            classList={{ active: c.value === props.option.currentValue }}
            role="option"
            aria-selected={c.value === props.option.currentValue}
            data-value={c.value}
            onClick={() => (props.close(), void configAgent(props.id, props.option.id, c.value).catch(errorToast))}
          >
            <span class="ai-model-check">{c.value === props.option.currentValue ? '✓' : ''}</span>
            <span class="grow">
              <span class="ai-model-name">{c.name}</span>
              <Show when={c.description}>
                <span class="ai-model-meta">{c.description}</span>
              </Show>
            </span>
          </button>
        )}
      </For>
    </div>
  )
}

/** The mode of the agent, as the mode button of the assistant: its color and its choices (Shift+Tab: the next one). */
function ModePill(props: { id: string; option: ConfigOption }) {
  const tone = () => modeTone(props.option.currentValue)
  return (
    <Popover
      class="ai-options-pop"
      trigger={(toggle) => (
        <button class="ai-mode" title={`${props.option.name} (Shift+Tab)`} onClick={toggle} data-testid={`agent-config-${props.option.id}`} data-value={props.option.currentValue}>
          <Icon name={toneIcons[tone()]} size={13} />
          <span class="ai-mode-label">{nameOf(props.option)}</span>
        </button>
      )}
    >
      {(close) => <ChoiceList id={props.id} option={props.option} close={close} />}
    </Popover>
  )
}

/** The model of the agent, as the model button of the assistant. */
function ModelPill(props: { id: string; option: ConfigOption }) {
  return (
    <Popover
      align="right"
      class="ai-model-pop"
      trigger={(toggle, open) => (
        <button class="ai-pill" classList={{ open }} onClick={toggle} title={props.option.name} data-testid={`agent-config-${props.option.id}`} data-value={props.option.currentValue}>
          <span class="ai-loaded-dot" />
          <span class="ellipsis">{nameOf(props.option)}</span>
          <span class="ai-chev open-up">
            <Icon name="chevron" size={10} />
          </span>
        </button>
      )}
    >
      {(close) => <ChoiceList id={props.id} option={props.option} close={close} />}
    </Popover>
  )
}

/** The follow mode and the other settings of the agent (effort…), behind the options button. */
function AgentOptions(props: { id: string; options: ConfigOption[]; follow: boolean }) {
  return (
    <Popover
      class="ai-options-pop"
      trigger={(toggle, open) => (
        <button class="ai-icon" classList={{ on: open }} onClick={toggle} title={t('Agent options')} data-testid="agent-options">
          <Icon name="sliders" size={16} />
        </button>
      )}
    >
      {() => (
        <>
          <Switch label={t('Follow the edits')} hint={t('Each file the agent edits opens in the editor')} checked={props.follow} onChange={(v) => void followAgent(props.id, v).catch(errorToast)} testid="agent-follow" />
          <For each={props.options}>
            {(o) => (
              <div class="ai-effort-row">
                <span>{o.name}</span>
                <div class="segmented" role="radiogroup" data-testid={`agent-config-${o.id}`}>
                  <For each={flatOptions(o)}>
                    {(c) => (
                      <button type="button" role="radio" aria-checked={c.value === o.currentValue} classList={{ on: c.value === o.currentValue }} title={c.description} onClick={() => void configAgent(props.id, o.id, c.value).catch(errorToast)}>
                        {c.name}
                      </button>
                    )}
                  </For>
                </div>
              </div>
            )}
          </For>
        </>
      )}
    </Popover>
  )
}

/** The context the agent uses, as the ring of the assistant. */
function Ring(props: { used: number; size: number }) {
  const ratio = () => (props.size ? Math.min(1, props.used / props.size) : 0)
  const level = () => (ratio() > 0.9 ? 'danger' : ratio() > 0.75 ? 'warn' : '')
  const r = 7
  const c = 2 * Math.PI * r
  return (
    <span
      class={`ai-ring agent-ring ${level()}`}
      title={t('Context: {used} / {size} tokens ({n} %)', { used: formatTokens(props.used), size: formatTokens(props.size), n: Math.round(ratio() * 100) })}
      data-testid="agent-usage"
    >
      <svg width="18" height="18" viewBox="0 0 18 18">
        <circle cx="9" cy="9" r={r} class="ai-ring-bg" />
        <circle cx="9" cy="9" r={r} class="ai-ring-fg" stroke-dasharray={`${c * ratio()} ${c}`} transform="rotate(-90 9 9)" />
      </svg>
    </span>
  )
}
