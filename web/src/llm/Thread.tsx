// Messages of the conversation: welcome screen, user messages, answers with their reasoning
// and tool steps, the answer being written with its live counters, edit confirmation.
import { createMemo, createSignal, For, onCleanup, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { activeTab, openFile, project, relPath } from '../state/project'
import { approval, chat, config, live, liveSpeed, savePrefs, setPrefs, type ChatMessage, type Part, type ToolCall } from './state'
import { retry } from './agent'
import { AttachmentChip, callLabel, DiffBlock, formatDuration, formatTokens, Markdown, safeArgs, toolIcons, toolVerbs } from './parts'
import { absPath } from './tools'
import { startEdit } from './Composer'

const textOf = (m: ChatMessage) =>
  m.display !== undefined
    ? m.display
    : typeof m.content === 'string' ? m.content : (m.content ?? []).filter((p): p is Extract<Part, { type: 'text' }> => p.type === 'text').slice(0, 1).map((p) => p.text).join('')

function copy(text: string) {
  navigator.clipboard?.writeText(text).then(
    () => toast('Copié', 'ok'),
    () => toast('Copie impossible', 'error'),
  )
}

/** A clock that ticks while something is running. */
function useNow(active: () => boolean, ms = 250) {
  const [now, setNow] = createSignal(Date.now())
  const t = setInterval(() => active() && setNow(Date.now()), ms)
  onCleanup(() => clearInterval(t))
  return now
}

// ---------- welcome ----------

function Welcome(props: { onSuggest: (t: string) => void; onSettings: () => void }) {
  const active = () => (activeTab()?.kind === 'file' ? relPath(activeTab()!.path!) : '')
  const suggestions = () => [
    { icon: 'outline', text: 'Explique-moi l’architecture de ce projet' },
    active() ? { icon: 'file', text: `Relis ${active()} et propose des améliorations` } : { icon: 'search', text: 'Où est gérée la configuration du projet ?' },
    { icon: 'conflict', text: 'Trouve et corrige les erreurs signalées par les serveurs de langage' },
    active() ? { icon: 'check', text: `Écris des tests pour ${active()}` } : { icon: 'terminal', text: 'Lance les tests et explique les échecs' },
  ]
  return (
    <div class="ai-empty ai-welcome">
      <div class="ai-welcome-icon">
        <Icon name="sparkle" size={26} />
      </div>
      <h2>Que fait-on sur {project()?.name ?? 'ce projet'} ?</h2>
      <p class="muted">
        L’assistant lit et modifie les fichiers, cherche dans le code, interroge les serveurs de langage et lance des commandes.
      </p>
      <Show
        when={config.servers.length}
        fallback={
          <button class="btn primary" onClick={props.onSettings}>
            Ajouter un serveur de modèles
          </button>
        }
      >
        <div class="ai-suggestions">
          <For each={suggestions()}>
            {(s) => (
              <button class="ai-suggestion" onClick={() => props.onSuggest(s.text)}>
                <Icon name={s.icon} size={14} />
                <span>{s.text}</span>
              </button>
            )}
          </For>
        </div>
      </Show>
      <p class="ai-hint">
        Glisser-déposer ou coller des images, vidéos, PDF · <kbd>Ctrl</kbd>+<kbd>Espace</kbd> pour dicter · <kbd>Maj</kbd>+<kbd>Entrée</kbd> pour aller à la ligne
      </p>
    </div>
  )
}

// ---------- user ----------

function UserMessage(props: { msg: ChatMessage; index: number }) {
  return (
    <div class="ai-msg user">
      <div class="ai-user-bubble">
        <Show when={props.msg.attachments?.length}>
          <div class="ai-atts">
            <For each={props.msg.attachments}>{(a) => <AttachmentChip a={a} />}</For>
          </div>
        </Show>
        <Show when={textOf(props.msg)}>
          <div class="ai-user-text">
            {/* @paths and the /command of the start shown as chips */}
            <For each={textOf(props.msg).split(/(^\/\S+|@[^\s@]+)/)}>
              {(part) => (part.startsWith('@') || /^\/\S+$/.test(part) ? <span class="ai-mention">{part}</span> : part)}
            </For>
          </div>
        </Show>
      </div>
      <div class="ai-actions">
        <button class="ai-act" title="Copier" onClick={() => copy(textOf(props.msg))}>
          <Icon name="copy" size={13} />
        </button>
        <Show when={!live.busy && !props.msg.compacted}>
          <button class="ai-act" title="Modifier et renvoyer" onClick={() => startEdit(props.index, textOf(props.msg))}>
            <Icon name="edit" size={13} />
          </button>
        </Show>
      </div>
    </div>
  )
}

// ---------- answers ----------

function Reasoning(props: { text: string; live?: boolean; ms?: number }) {
  const now = useNow(() => !!props.live, 500)
  const label = () => {
    if (props.live) return `Réflexion${live.thinkStart ? ` · ${formatDuration(now() - live.thinkStart)}` : ''}…`
    return props.ms ? `Réflexion · ${formatDuration(props.ms)}` : 'Réflexion'
  }
  return (
    <details class="ai-reasoning" classList={{ live: !!props.live }} open={props.live}>
      <summary>
        <Icon name="chevron" size={11} />
        <span class={props.live ? 'ai-shimmer' : ''}>{label()}</span>
      </summary>
      <div class="ai-reasoning-text">{props.text}</div>
    </details>
  )
}

function ToolRow(props: { msg: ChatMessage; call?: ToolCall }) {
  const [open, setOpen] = createSignal(false)
  const label = () => callLabel(props.call, props.msg.name ?? '')
  const pending = () => !props.msg.status
  const path = () => {
    const a = safeArgs(props.call)
    return typeof a.path === 'string' && props.msg.status === 'ok' && props.msg.name !== 'list_dir' ? absPath(a.path) : ''
  }
  return (
    <div class={`ai-tool ${props.msg.status ?? 'running'}`}>
      <button class="ai-tool-head" onClick={() => setOpen(!open())} title={`${props.msg.name} : afficher le résultat`}>
        <span class="ai-tool-icon">
          <Show when={!pending()} fallback={<span class="spinner" />}>
            <Icon name={toolIcons[props.msg.name ?? ''] ?? 'puzzle'} size={13} />
          </Show>
        </span>
        <span class="ai-tool-name">{toolVerbs[label().name] ?? label().name}</span>
        <span class="ai-tool-target ellipsis">
          {label().target}
          {label().extra}
        </span>
        <span class="grow" />
        <span class="ai-tool-sum ellipsis">{pending() ? '' : props.msg.summary}</span>
        <span class="ai-chev" classList={{ open: open() }}>
          <Icon name="chevron" size={11} />
        </span>
      </button>
      <Show when={props.msg.diff?.length && (open() || props.msg.status === 'ok')}>
        <DiffBlock lines={props.msg.diff!} />
      </Show>
      <Show when={open()}>
        <div class="ai-tool-detail">
          <Show when={path()}>
            <button class="link small" onClick={() => openFile(path())}>
              Ouvrir {relPath(path())}
            </button>
          </Show>
          <Show
            when={props.msg.name === 'bash'}
            fallback={
              <>
                <Show when={props.call}>
                  <div class="ai-tool-args mono">
                    {props.msg.name}({props.call!.function.arguments.length > 300 ? props.call!.function.arguments.slice(0, 300) + '…' : props.call!.function.arguments})
                  </div>
                </Show>
                <pre class="ai-tool-out">{typeof props.msg.content === 'string' ? props.msg.content : ''}</pre>
              </>
            }
          >
            {/* A terminal-like block: the command, then its output. */}
            <pre class="ai-term">
              <span class="ai-term-cmd">$ {safeArgs(props.call).command}</span>
              {'\n' + (typeof props.msg.content === 'string' ? props.msg.content : '')}
            </pre>
          </Show>
        </div>
      </Show>
    </div>
  )
}

function ToolSteps(props: { items: { msg: ChatMessage; call?: ToolCall }[] }) {
  const running = () => props.items.some((i) => !i.msg.status)
  const failed = () => props.items.filter((i) => i.msg.status === 'error').length
  const names = () => [...new Set(props.items.map((i) => toolVerbs[i.msg.name ?? ''] ?? i.msg.name))].slice(0, 3).join(', ')
  return (
    <Show when={props.items.length > 2} fallback={<div class="ai-steps-flat">{<For each={props.items}>{(i) => <ToolRow msg={i.msg} call={i.call} />}</For>}</div>}>
      <details class="ai-steps" open={running() || undefined}>
        <summary>
          <Show when={running()} fallback={<Icon name="check" size={12} />}>
            <span class="spinner" />
          </Show>
          <span>
            {props.items.length} actions <span class="muted">· {names()}</span>
          </span>
          <Show when={failed()}>
            <span class="badge danger">{failed()} en échec</span>
          </Show>
          <span class="grow" />
          <span class="ai-chev ai-steps-chev">
            <Icon name="chevron" size={11} />
          </span>
        </summary>
        <For each={props.items}>{(i) => <ToolRow msg={i.msg} call={i.call} />}</For>
      </details>
    </Show>
  )
}

function Stats(props: { msg: ChatMessage }) {
  const u = () => props.msg.usage
  const parts = () => {
    const out: string[] = []
    if (props.msg.model) out.push(props.msg.model)
    if (u()?.perSecond) out.push(`${u()!.perSecond!.toFixed(1)} jetons/s`)
    const ms = props.msg.elapsedMs ?? u()?.durationMs
    if (ms) out.push(formatDuration(ms))
    if (u()) out.push(`${formatTokens(u()!.prompt)} → ${formatTokens(u()!.completion)} jetons${u()!.cached ? ` (${formatTokens(u()!.cached!)} en cache)` : ''}`)
    return out
  }
  return (
    <Show when={parts().length}>
      <span class="ai-usage">{parts().join(' · ')}</span>
    </Show>
  )
}

function AssistantMessage(props: { msg: ChatMessage; index: number; lastOfTurn: boolean; lastTurn: boolean; turnStart: number }) {
  // Tool results follow the message that asked for them.
  const results = () => {
    const out: { msg: ChatMessage; call?: ToolCall }[] = []
    for (let i = props.index + 1; i < chat.messages.length && chat.messages[i].role === 'tool'; i++) {
      const m = chat.messages[i]
      out.push({ msg: m, call: props.msg.tool_calls?.find((c) => c.id === m.tool_call_id) })
    }
    return out
  }
  const turnText = () =>
    chat.messages
      .slice(props.turnStart, props.index + 1)
      .filter((m) => m.role === 'assistant' && typeof m.content === 'string' && m.content)
      .map((m) => m.content as string)
      .join('\n\n')
  return (
    <div class="ai-msg assistant" classList={{ 'turn-end': props.lastOfTurn }}>
      <Show when={props.msg.reasoning_content}>
        <Reasoning text={props.msg.reasoning_content!} ms={props.msg.thinkMs} />
      </Show>
      <Show when={typeof props.msg.content === 'string' && props.msg.content}>
        <Markdown text={props.msg.content as string} final />
      </Show>
      <Show when={results().length}>
        <ToolSteps items={results()} />
      </Show>
      <Show when={props.msg.error}>
        <div class="ai-error">
          <Icon name="conflict" size={13} /> {props.msg.error}
        </div>
      </Show>
      <Show when={props.lastOfTurn && !(live.busy && props.lastTurn)}>
        <div class="ai-actions">
          <Show when={turnText()}>
            <button class="ai-act" title="Copier la réponse" onClick={() => copy(turnText())}>
              <Icon name="copy" size={13} />
            </button>
          </Show>
          <Show when={props.lastTurn && !props.msg.compacted}>
            <button class="ai-act" title="Régénérer la réponse" onClick={() => retry().catch(errorToast)}>
              <Icon name="refresh" size={13} />
            </button>
          </Show>
          <Stats msg={props.msg} />
        </div>
      </Show>
    </div>
  )
}

function SummaryCard(props: { msg: ChatMessage }) {
  return (
    <details class="ai-summary" data-testid="ai-summary">
      <summary>
        <Icon name="history" size={13} />
        <span>
          Conversation compactée : {props.msg.summarized} messages résumés{props.msg.model ? ` par ${props.msg.model}` : ''}
        </span>
      </summary>
      <Markdown text={typeof props.msg.content === 'string' ? props.msg.content : ''} final />
    </details>
  )
}

// ---------- live ----------

function LiveStats() {
  const now = useNow(() => live.busy)
  const text = () => {
    const t = now()
    const parts: string[] = []
    if (live.compacting) return 'Compaction de la conversation…'
    if (!live.firstAt) {
      if (live.promptTotal) {
        const pct = Math.round((live.promptDone / live.promptTotal) * 100)
        parts.push(`Lecture du prompt · ${pct} % (${formatTokens(live.promptDone)} / ${formatTokens(live.promptTotal)})`)
      } else parts.push('En attente du modèle')
    } else {
      parts.push(live.tool ? `Prépare l’appel à ${live.tool}` : live.content ? 'Écrit' : 'Réfléchit')
      const speed = liveSpeed(t)
      if (speed) parts.push(`${speed.toFixed(1)} jetons/s`)
      if (live.tokens) parts.push(`${live.tokens} jetons`)
    }
    if (live.startedAt) parts.push(formatDuration(t - live.startedAt))
    return parts.join(' · ')
  }
  return (
    <div class="ai-live-stats" data-testid="ai-live-stats">
      <span class="ai-dots">
        <i />
        <i />
        <i />
      </span>
      <span>{text()}</span>
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
      <div class="ai-approval-foot">
        <label class="check small">
          <input type="checkbox" onChange={(e) => (setPrefs('autoApply', e.currentTarget.checked), savePrefs())} />
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

// ---------- thread ----------

export function Thread(props: { onSuggest: (t: string) => void; onSettings: () => void }) {
  const [showCompacted, setShowCompacted] = createSignal(false)
  // For each assistant message: is it the last of its turn, where does the turn start.
  const turns = createMemo(() => {
    const msgs = chat.messages
    const info: { lastOfTurn: boolean; turnStart: number }[] = []
    let start = 0
    for (let i = 0; i < msgs.length; i++) {
      if (msgs[i].role === 'user') start = i + 1
      let j = i + 1
      while (j < msgs.length && msgs[j].role === 'tool') j++
      info.push({ lastOfTurn: msgs[i].role === 'assistant' && (j >= msgs.length || msgs[j].role === 'user'), turnStart: start })
    }
    return info
  })
  const lastUser = createMemo(() => {
    for (let i = chat.messages.length - 1; i >= 0; i--) if (chat.messages[i].role === 'user') return i
    return -1
  })
  const compactedCount = () => chat.messages.filter((m) => m.compacted).length
  const lastFailed = () => {
    const m = chat.messages[chat.messages.length - 1]
    return !live.busy && m && (m.role === 'user' || (m.role === 'assistant' && !!m.error && !m.content))
  }
  return (
    <div class="ai-thread">
      <Show when={!chat.messages.length && !live.busy}>
        <Welcome onSuggest={props.onSuggest} onSettings={props.onSettings} />
      </Show>
      <Show when={compactedCount()}>
        <button class="ai-compacted-toggle" onClick={() => setShowCompacted(!showCompacted())}>
          <Icon name="history" size={12} /> {showCompacted() ? 'Masquer' : 'Afficher'} les {compactedCount()} messages compactés
        </button>
      </Show>
      <For each={chat.messages}>
        {(m, i) => (
          <Show when={m.role !== 'tool' && (!m.compacted || showCompacted())}>
            <div class="ai-row" classList={{ 'ai-old': !!m.compacted }}>
              <Show
                when={m.kind === 'summary'}
                fallback={
                  <Show
                    when={m.role === 'user'}
                    fallback={<AssistantMessage msg={m} index={i()} lastOfTurn={turns()[i()]?.lastOfTurn ?? false} lastTurn={i() > lastUser()} turnStart={turns()[i()]?.turnStart ?? 0} />}
                  >
                    <UserMessage msg={m} index={i()} />
                  </Show>
                }
              >
                <SummaryCard msg={m} />
              </Show>
            </div>
          </Show>
        )}
      </For>
      <Show when={live.busy && !approval()}>
        <div class="ai-msg assistant live">
          <Show when={live.reasoning}>
            <Reasoning text={live.reasoning} live={!live.content && !live.tool} ms={live.thinkEnd ? live.thinkEnd - live.thinkStart : undefined} />
          </Show>
          <Show when={live.content}>
            <Markdown text={live.content} final={false} />
          </Show>
          <LiveStats />
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
  )
}
