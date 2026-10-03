// Small pieces shared by the views of the assistant.
import { createEffect, createSignal, For, onCleanup, Show, type JSX } from 'solid-js'
import { relPath, root } from '../state/project'
import { onMarkdownClick, renderMarkdown, renderMermaid } from './markdown'
import { absPath } from './tools'
import type { Attachment, DiffLine, ToolCall } from './state'

/** Markdown rendered at most once per frame while it streams; diagrams drawn as they close. */
export function Markdown(props: { text: string; final: boolean }) {
  let el!: HTMLDivElement
  let frame = 0
  createEffect(() => {
    const text = props.text
    const final = props.final
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => {
      el.innerHTML = renderMarkdown(text)
      renderMermaid(el, final).catch(() => {})
    })
  })
  onCleanup(() => cancelAnimationFrame(frame))
  return <div class="md" ref={el} onClick={onMarkdownClick} />
}

export function formatSize(n: number) {
  if (n < 1024) return `${n} o`
  if (n < 1 << 20) return `${(n / 1024).toFixed(0)} Ko`
  if (n < 1 << 30) return `${(n / (1 << 20)).toFixed(1)} Mo`
  return `${(n / (1 << 30)).toFixed(1)} Go`
}

export function formatDuration(ms: number) {
  const s = ms / 1000
  if (s < 10) return `${s.toFixed(1)} s`
  if (s < 60) return `${Math.round(s)} s`
  return `${Math.floor(s / 60)} min ${String(Math.round(s % 60)).padStart(2, '0')} s`
}

export function formatTokens(n: number) {
  return n >= 1000 ? `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k` : String(n)
}

export function DiffBlock(props: { lines: DiffLine[] }) {
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

export function AttachmentChip(props: { a: Attachment; onRemove?: () => void }) {
  const icons: Record<string, string> = { image: '🖼', video: '🎞', audio: '🔊', pdf: '📄', text: '📃' }
  return (
    <span class="ai-att" title={`${props.a.name} · ${formatSize(props.a.size)}${props.a.note ? ` · ${props.a.note}` : ''}`}>
      <Show when={props.a.thumb} fallback={<span class="ai-att-icon">{icons[props.a.kind]}</span>}>
        <img src={props.a.thumb} alt="" />
      </Show>
      <span class="ai-att-name">
        <span class="ellipsis">{props.a.name}</span>
        <span class="ai-att-meta">{props.a.note ?? formatSize(props.a.size)}</span>
      </span>
      <Show when={props.onRemove}>
        <button class="ai-att-x" title="Retirer" onClick={props.onRemove}>
          ✕
        </button>
      </Show>
    </span>
  )
}

export function safeArgs(call: ToolCall | undefined): any {
  try {
    return JSON.parse(call?.function.arguments || '{}')
  } catch {
    return {}
  }
}

/** Main argument of a tool call, for its one-line label. */
export function callLabel(call: ToolCall | undefined, name: string) {
  const a = safeArgs(call)
  const abs = a.path !== undefined ? absPath(a.path) : ''
  const target =
    a.path !== undefined
      ? abs === root() ? '.' : relPath(abs)
      : a.query ?? a.pattern ?? a.command ?? a.name ?? a.panel ?? a.console_id ?? (name.startsWith('kanban_') ? (a.id ? `#${a.id}` : a.title ?? a.status ?? a.hash ?? '') : '')
  const extra = a.symbol ? ` · ${a.symbol}${a.line ? ` (l. ${a.line})` : ''}` : a.start_line ? ` · l. ${a.start_line}${a.end_line ? `-${a.end_line}` : ''}` : a.line ? ` · l. ${a.line}${a.end_line ? `-${a.end_line}` : ''}` : ''
  return { name, target: String(target), extra }
}

/** Readable verb of each tool, for the steps of an answer. */
export const toolVerbs: Record<string, string> = {
  list_dir: 'Liste',
  find_files: 'Cherche les fichiers',
  read_file: 'Lit',
  search_text: 'Recherche',
  edit_file: 'Modifie',
  write_file: 'Écrit',
  lsp_symbols: 'Structure de',
  lsp_workspace_symbols: 'Cherche le symbole',
  lsp_definition: 'Définition dans',
  lsp_references: 'Références dans',
  lsp_hover: 'Documentation dans',
  lsp_diagnostics: 'Diagnostics',
  load_skill: 'Charge le skill',
  read_skill_file: 'Lit le skill',
  open_file: 'Ouvre',
  focus: 'Affiche',
  run_command: 'Lance dans une console',
  bash: 'Exécute',
  list_consoles: 'Liste les consoles',
  read_console: 'Lit la console',
  console_input: 'Tape dans la console',
  kanban_list: 'Liste les tickets',
  kanban_get: 'Lit le ticket',
  kanban_create: 'Crée le ticket',
  kanban_update: 'Modifie le ticket',
  kanban_add_note: 'Ajoute une note',
  kanban_set_plan: 'Écrit le plan',
  kanban_goal: 'Goal',
  kanban_move: 'Change l’état du ticket',
  kanban_link_commit: 'Lie le commit',
  ask_user: 'Pose des questions',
}

export const toolIcons: Record<string, string> = {
  list_dir: 'folder', find_files: 'search', read_file: 'file', search_text: 'search', edit_file: 'edit', write_file: 'edit',
  open_file: 'external', focus: 'locate', run_command: 'terminal', bash: 'terminal', list_consoles: 'terminal', read_console: 'terminal', console_input: 'terminal',
  load_skill: 'puzzle', read_skill_file: 'puzzle', lsp_symbols: 'outline', lsp_workspace_symbols: 'outline', lsp_definition: 'outline',
  lsp_references: 'outline', lsp_hover: 'info', lsp_diagnostics: 'conflict',
  kanban_list: 'kanban', kanban_get: 'kanban', kanban_create: 'kanban', kanban_update: 'kanban', kanban_add_note: 'kanban', kanban_set_plan: 'kanban',
  kanban_goal: 'check', kanban_move: 'kanban', kanban_link_commit: 'branch', ask_user: 'info',
}

/** Menu opened above (or below) its trigger, closed by a click outside or Escape. */
export function Popover(props: { trigger: (open: () => void, isOpen: boolean) => JSX.Element; children: (close: () => void) => JSX.Element; class?: string; align?: 'left' | 'right' }) {
  const [open, setOpen] = createSignal(false)
  let box!: HTMLDivElement
  const close = () => setOpen(false)
  const onDown = (e: MouseEvent) => {
    if (!box.contains(e.target as Node)) close()
  }
  const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
  createEffect(() => {
    if (open()) {
      document.addEventListener('mousedown', onDown)
      document.addEventListener('keydown', onKey)
    } else {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  })
  onCleanup(() => {
    document.removeEventListener('mousedown', onDown)
    document.removeEventListener('keydown', onKey)
  })
  return (
    <div class="ai-pop-anchor" ref={box}>
      {props.trigger(() => setOpen(!open()), open())}
      <Show when={open()}>
        <div class={`ai-pop ${props.align === 'right' ? 'right' : ''} ${props.class ?? ''}`} role="menu">
          {props.children(close)}
        </div>
      </Show>
    </div>
  )
}

/** Small on/off switch with a label, for the menus. */
export function Switch(props: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void; testid?: string }) {
  return (
    <label class="ai-switch-row" data-testid={props.testid}>
      <span class="grow">
        <span>{props.label}</span>
        <Show when={props.hint}>
          <span class="ai-switch-hint">{props.hint}</span>
        </Show>
      </span>
      <input type="checkbox" class="ai-switch" checked={props.checked} onChange={(e) => props.onChange(e.currentTarget.checked)} />
    </label>
  )
}
