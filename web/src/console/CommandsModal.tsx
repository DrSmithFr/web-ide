// Modal of the saved commands: the list on the left (filter, new, duplicate, order), the
// command chosen on the right, its bash in an editor. Edits stay in the modal until Save.
import { createMemo, createSignal, For, onCleanup, onMount, Show } from 'solid-js'
import { Modal } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { Doc } from '../editor/doc'
import { EditorView } from '../editor/view'
import { settings } from '../state/settings'
import { t } from '../i18n'
import { commands, fileError, flagIcon, flags, loadCommands, openApp, runCommand, runningConsole, saveCommands, servesApp, type Command } from './commands'

const [request, setRequest] = createSignal<{ select?: string; draft?: Partial<Command> } | null>(null)

/** Opens the modal on a command, or on a new one filled with draft. */
export function openCommands(o: { select?: string; draft?: Partial<Command> } = {}) {
  setRequest(o)
}

export function CommandsHost() {
  return (
    <Show when={request()} keyed>
      {(r) => <CommandsModal select={r.select} draft={r.draft} onClose={() => setRequest(null)} />}
    </Show>
  )
}

let seq = 0
const newKey = () => `new-${++seq}`
const blank = (): Command => ({ id: newKey(), name: '', description: '', command: '', flag: 'other', cwd: '' })

function CommandsModal(props: { select?: string; draft?: Partial<Command>; onClose: () => void }) {
  const [list, setList] = createSignal<Command[]>([])
  const [selected, setSelected] = createSignal<string | null>(null)
  const [filter, setFilter] = createSignal('')
  const [dirty, setDirty] = createSignal(false)
  const [busy, setBusy] = createSignal(false)
  const [error, setError] = createSignal('')
  let dragged: number | null = null

  onMount(async () => {
    await loadCommands()
    const l = commands().map((c) => ({ ...c }))
    if (props.draft) {
      const c = { ...blank(), ...props.draft, id: newKey() }
      l.push(c)
      setDirty(true)
      setSelected(c.id)
    } else setSelected(props.select ?? l[0]?.id ?? null)
    setList(l)
  })

  const current = createMemo(() => list().find((c) => c.id === selected()) ?? null)
  const shown = () => {
    const q = filter().trim().toLowerCase()
    return list().filter((c) => !q || c.name.toLowerCase().includes(q) || c.flag.includes(q) || (c.description ?? '').toLowerCase().includes(q))
  }
  const edit = (patch: Partial<Command>) => {
    setList((l) => l.map((c) => (c.id === selected() ? { ...c, ...patch } : c)))
    setDirty(true)
  }
  const add = (c: Command) => {
    setList((l) => [...l, c])
    setSelected(c.id)
    setDirty(true)
    queueMicrotask(() => document.querySelector<HTMLInputElement>('[data-testid=cmd-name]')?.focus())
  }
  const move = (from: number, to: number) => {
    if (to < 0 || to >= list().length || from === to) return
    setList((l) => {
      const n = [...l]
      const [c] = n.splice(from, 1)
      n.splice(to, 0, c)
      return n
    })
    setDirty(true)
  }
  const remove = () => {
    const c = current()
    if (!c || !confirm(t('Delete the command “{name}”?', { name: c.name || t('New command') }))) return
    const i = list().indexOf(c)
    setList((l) => l.filter((x) => x !== c))
    setSelected(list()[Math.min(i, list().length - 1)]?.id ?? null)
    setDirty(true)
  }
  const close = () => {
    if (dirty() && !confirm(t('The commands have unsaved changes. Close anyway?'))) return
    props.onClose()
  }
  /** Saves; returns the saved command matching the one selected. */
  const save = async () => {
    setBusy(true)
    setError('')
    try {
      const index = list().findIndex((c) => c.id === selected())
      const saved = await saveCommands(list().map((c) => ({ ...c, id: c.id.startsWith('new-') ? '' : c.id, port: c.port || undefined })))
      setList(saved.map((c) => ({ ...c })))
      setSelected(saved[index]?.id ?? saved[0]?.id ?? null)
      setDirty(false)
      return saved[index]
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const run = async () => {
    const c = dirty() ? await save() : current()
    if (!c) return
    props.onClose()
    await runCommand(c)
  }
  const openTheApp = async () => {
    const c = dirty() ? await save() : current()
    if (!c) return
    props.onClose()
    await openApp(c)
  }
  const onListKey = (e: KeyboardEvent) => {
    const l = shown()
    const i = l.findIndex((c) => c.id === selected())
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    e.preventDefault()
    const dir = e.key === 'ArrowDown' ? 1 : -1
    if (e.altKey && !filter()) return move(i, i + dir)
    const next = l[Math.max(0, Math.min(l.length - 1, i + dir))]
    if (next) setSelected(next.id)
  }

  return (
    <Modal
      title={t('Project commands')}
      onClose={close}
      class="modal-wide cmd-modal"
      footer={
        <>
          <Show when={error()}>
            <span class="danger small" data-testid="cmd-error">
              {error()}
            </span>
          </Show>
          <span style={{ flex: 1 }} />
          <button class="btn" onClick={close}>
            {t('Cancel')}
          </button>
          <button class="btn primary" disabled={busy() || !dirty()} onClick={() => void save()} data-testid="cmd-save">
            {t('Save')}
          </button>
        </>
      }
    >
      <div class="cmd-layout">
        <div class="cmd-side">
          <div class="cmd-side-bar">
            <input class="cmd-filter" placeholder={t('Filter…')} value={filter()} onInput={(e) => setFilter(e.currentTarget.value)} />
            <button class="icon-btn small" title={t('New command')} onClick={() => add(blank())} data-testid="cmd-new">
              <Icon name="plus" />
            </button>
            <button class="icon-btn small" title={t('Duplicate')} disabled={!current()} onClick={() => add({ ...current()!, id: newKey(), name: t('{name} (copy)', { name: current()!.name }) })} data-testid="cmd-duplicate">
              <Icon name="copy" />
            </button>
          </div>
          <Show when={fileError()}>
            <p class="danger small">{fileError()}</p>
          </Show>
          <div class="cmd-list" role="listbox" tabindex="0" onKeyDown={onListKey} aria-label={t('Commands')}>
            <For each={shown()} fallback={<p class="muted small cmd-none">{t('No command yet.')}</p>}>
              {(c) => (
                <div
                  class="cmd-item"
                  role="option"
                  aria-selected={c.id === selected()}
                  classList={{ selected: c.id === selected() }}
                  title={c.description || undefined}
                  draggable={!filter()}
                  onDragStart={() => (dragged = list().indexOf(c))}
                  onDragOver={(e) => dragged !== null && e.preventDefault()}
                  onDrop={(e) => {
                    e.preventDefault()
                    if (dragged !== null) move(dragged, list().indexOf(c))
                    dragged = null
                  }}
                  onClick={() => setSelected(c.id)}
                  data-testid="cmd-item"
                >
                  <Icon name={flagIcon[c.flag]} size={13} />
                  <span class="cmd-item-name">{c.name || t('New command')}</span>
                  <Show when={!c.id.startsWith('new-') && runningConsole(c.id)}>
                    <span class="cmd-running" title={t('Running')} />
                  </Show>
                  <span class="muted small">{c.flag}</span>
                </div>
              )}
            </For>
          </div>
          <p class="muted small">{t('Alt+↑ / Alt+↓ or drag: order of the ▶ menu.')}</p>
        </div>
        <Show when={current() && selected()} keyed fallback={<div class="cmd-detail muted">{t('Create a command with +.')}</div>}>
          {(_id: string) => {
            // Keyed by the id: the editor is not rebuilt at each keystroke.
            const c = () => current()!
            return (
              <div class="cmd-detail form">
                <div class="cmd-row">
                  <label class="field" style={{ flex: 2 }}>
                    <span>{t('Name')}</span>
                    <input value={c().name} onInput={(e) => edit({ name: e.currentTarget.value })} data-testid="cmd-name" />
                  </label>
                  <label class="field" style={{ flex: 1 }}>
                    <span>{t('Flag')}</span>
                    <select value={c().flag} onChange={(e) => edit({ flag: e.currentTarget.value as Command['flag'] })} data-testid="cmd-flag">
                      <For each={flags}>{(f) => <option value={f}>{f}</option>}</For>
                    </select>
                  </label>
                </div>
                <label class="field">
                  <span>{t('Description (optional)')}</span>
                  <input value={c().description ?? ''} onInput={(e) => edit({ description: e.currentTarget.value })} data-testid="cmd-description" />
                </label>
                <div class="cmd-row">
                  <label class="field" style={{ flex: 2 }}>
                    <span>{t('Folder (relative to the root, optional)')}</span>
                    <input value={c().cwd ?? ''} placeholder="web/" onInput={(e) => edit({ cwd: e.currentTarget.value })} data-testid="cmd-cwd" />
                  </label>
                  <Show when={c().flag === 'serve'}>
                    <label class="field" style={{ flex: 1 }}>
                      <span>{t('Port (optional)')}</span>
                      <input type="number" min="1" max="65535" value={c().port ?? ''} onInput={(e) => edit({ port: Number(e.currentTarget.value) || undefined })} data-testid="cmd-port" />
                    </label>
                  </Show>
                </div>
                <div class="field">
                  <span>{t('Command (bash)')}</span>
                  <BashEditor value={c().command} onInput={(command) => edit({ command })} />
                </div>
                <div class="cmd-actions">
                  <button class="btn danger small" onClick={remove} data-testid="cmd-delete">
                    <Icon name="trash" size={13} /> {t('Delete')}
                  </button>
                  <span style={{ flex: 1 }} />
                  <Show when={servesApp(c())}>
                    <button class="btn" disabled={busy() || !c().name.trim() || !c().command.trim()} onClick={() => void openTheApp()} title={t('Runs the command unless it runs already, waits for its port, then opens the app on a temporary URL')} data-testid="cmd-open-app">
                      <Icon name="external" size={13} /> {t('Start and open the app')}
                    </button>
                  </Show>
                  <button class="btn" disabled={busy() || !c().name.trim() || !c().command.trim()} onClick={() => void run()} data-testid="cmd-run">
                    <Icon name="play" size={13} /> {dirty() ? t('Save and run') : t('Run')}
                  </button>
                </div>
              </div>
            )
          }}
        </Show>
      </div>
    </Modal>
  )
}

/** Multiline bash in the editor of the IDE (shell highlighting). */
function BashEditor(props: { value: string; onInput: (v: string) => void }) {
  let host!: HTMLDivElement
  const doc = new Doc('', props.value, { lang: 'shell' })
  onCleanup(doc.onChange(() => props.onInput(doc.text)))
  onMount(() => {
    const v = new EditorView(doc, { tabSize: settings.editor.tabSize, insertSpaces: true, highlightLine: false, free: true, placeholder: 'npm ci\nnpm run dev' })
    v.mount(host)
    onCleanup(() => v.destroy())
  })
  return <div class="cmd-editor" ref={host} data-testid="cmd-command" />
}
