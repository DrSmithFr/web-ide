// Tool "Assistant IA": chat with a model of a llama.cpp or Ollama server, which can read and
// change the project files, ask the language servers and run commands. Layout: history side
// bar (always shown in a wide detached window, else folding), conversation, message box.
import { createEffect, createSignal, on, onCleanup, onMount, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast } from '../ui/toast'
import { route } from '../app/router'
import { approval, chat, live, loadConfig, modelsError, prefs, resetChat, restoreActive, savePrefs, setPrefs } from './state'
import { SettingsModal } from './AssistantSettings'
import { Thread } from './Thread'
import { addFiles, Composer, focusComposer, suggest } from './Composer'
import { Sidebar } from './Sidebar'
import './assistant.css'

/** Width from which the side bar sits next to the conversation instead of over it. */
const WIDE = 720

export function AssistantTool() {
  const detached = route().name === 'tool'
  const [width, setWidth] = createSignal(0)
  const [settings, setSettings] = createSignal(false)
  const [dragging, setDragging] = createSignal(false)
  const wide = () => width() >= WIDE
  // In a wide detached window the history is always there.
  const pinned = () => detached && wide()
  const sidebarShown = () => pinned() || prefs.sidebarOpen
  const setSidebar = (v: boolean) => {
    setPrefs('sidebarOpen', v)
    savePrefs()
  }
  let rootEl!: HTMLDivElement
  let list!: HTMLDivElement
  let stick = true

  onMount(() => {
    const ro = new ResizeObserver(() => setWidth(rootEl.clientWidth))
    ro.observe(rootEl)
    onCleanup(() => ro.disconnect())
    setWidth(rootEl.clientWidth)
    loadConfig().catch(errorToast)
    restoreActive().catch(() => {})
    focusComposer()
  })

  // Follow the answer while the view is at the bottom.
  const onScroll = () => {
    stick = list.scrollHeight - list.scrollTop - list.clientHeight < 60
  }
  const toBottom = () => requestAnimationFrame(() => list && (list.scrollTop = list.scrollHeight))
  createEffect(
    on(
      () => [chat.messages.length, live.content, live.reasoning, live.tool, approval()],
      () => stick && toBottom(),
    ),
  )
  createEffect(
    on(
      () => chat.id,
      () => {
        stick = true
        toBottom()
      },
    ),
  )

  const newChat = () => {
    if (live.busy) return
    resetChat()
    focusComposer()
  }

  return (
    <div class="panel ai-panel" ref={rootEl} classList={{ wide: wide(), detached }}>
      <div class="panel-head ai-head">
        <Show when={!pinned()}>
          <button class="icon-btn" classList={{ on: prefs.sidebarOpen }} title="Conversations du projet" onClick={() => setSidebar(!prefs.sidebarOpen)}>
            <Icon name="sidebar" size={15} />
          </button>
        </Show>
        <span class="ai-title ellipsis" title={chat.title}>
          {chat.title || 'Nouvelle conversation'}
        </span>
        <span class="grow" />
        <button class="icon-btn" title="Nouvelle conversation" disabled={live.busy} onClick={newChat}>
          <Icon name="plus" size={15} />
        </button>
        <button class="icon-btn" title="Réglages (serveurs, prompt, compaction, transcription)" onClick={() => setSettings(true)}>
          <Icon name="gear" size={15} />
        </button>
      </div>
      <Show when={modelsError()}>
        <div class="ai-banner">
          <Icon name="conflict" size={13} /> {modelsError()}
        </div>
      </Show>
      <div class="ai-body">
        <Show when={sidebarShown()}>
          <Show when={!wide()}>
            <div class="ai-scrim" onClick={() => setSidebar(false)} />
          </Show>
          <div class="ai-side-wrap" classList={{ overlay: !wide() }}>
            <Sidebar onPicked={() => !wide() && setSidebar(false)} onNew={() => (!wide() && setSidebar(false), focusComposer())} />
          </div>
        </Show>
        <div class="ai-main">
          <div
            class="ai-messages"
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
            <Thread onSuggest={suggest} onSettings={() => setSettings(true)} />
          </div>
          <Composer
            onSettings={() => setSettings(true)}
            onSent={() => {
              stick = true
              toBottom()
            }}
          />
        </div>
      </div>
      <Show when={settings()}>
        <SettingsModal onClose={() => setSettings(false)} />
      </Show>
    </div>
  )
}
