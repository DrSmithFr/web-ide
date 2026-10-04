// Generic overlays: modal, prompt, keyboard-driven pick list, context menu.
import { createEffect, createSignal, For, type JSX, onCleanup, onMount, Show } from 'solid-js'
import { Portal } from 'solid-js/web'
import { t } from '../i18n'

export function Modal(props: { title: string; onClose: () => void; children: JSX.Element; class?: string; footer?: JSX.Element }) {
  const key = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      props.onClose()
    }
  }
  return (
    <Portal>
      <div class="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && props.onClose()} onKeyDown={key}>
        <div class={`modal ${props.class ?? ''}`} role="dialog" aria-modal="true" aria-label={props.title}>
          <header class="modal-head">
            <h2>{props.title}</h2>
            <button class="icon-btn" title={t('Close (Esc)')} onClick={props.onClose}>
              ✕
            </button>
          </header>
          <div class="modal-body">{props.children}</div>
          <Show when={props.footer}>
            <footer class="modal-foot">{props.footer}</footer>
          </Show>
        </div>
      </div>
    </Portal>
  )
}

// ---------- prompt ----------

interface PromptReq {
  title: string
  label?: string
  value?: string
  password?: boolean
  placeholder?: string
  resolve: (v: string | null) => void
}
const [promptReq, setPromptReq] = createSignal<PromptReq | null>(null)

export function prompt(o: Omit<PromptReq, 'resolve'>): Promise<string | null> {
  return new Promise((resolve) => setPromptReq({ ...o, resolve }))
}

function PromptHost() {
  let input!: HTMLInputElement
  const close = (v: string | null) => {
    promptReq()?.resolve(v)
    setPromptReq(null)
  }
  createEffect(() => {
    if (promptReq()) queueMicrotask(() => input?.select())
  })
  return (
    <Show when={promptReq()}>
      {(r) => (
        <Modal title={r().title} onClose={() => close(null)} class="modal-small">
          <form
            class="form"
            onSubmit={(e) => {
              e.preventDefault()
              close(input.value)
            }}
          >
            <label class="field">
              <Show when={r().label}>
                <span>{r().label}</span>
              </Show>
              <input ref={input} type={r().password ? 'password' : 'text'} value={r().value ?? ''} placeholder={r().placeholder} autofocus />
            </label>
            <div class="form-actions">
              <button type="button" class="btn" onClick={() => close(null)}>
                {t('Cancel')}
              </button>
              <button type="submit" class="btn primary">
                {t('OK')}
              </button>
            </div>
          </form>
        </Modal>
      )}
    </Show>
  )
}

// ---------- pick list ----------

export interface PickItem<T = unknown> {
  label: string
  detail?: string
  hint?: string
  value: T
  icon?: string
}

interface PickReq {
  placeholder: string
  items?: PickItem[]
  provider?: (q: string, signal: AbortSignal) => Promise<PickItem[]> | PickItem[]
  initial?: number
  noFilter?: boolean
  anchor?: { left: number; top: number }
  /** Labels are code (monospace); details are paths (monospace). */
  codeLabel?: boolean
  pathDetail?: boolean
  resolve: (v: any) => void
  onPreview?: (v: any) => void
}
const [pickReq, setPickReq] = createSignal<PickReq | null>(null)

export function pick<T>(o: {
  placeholder: string
  items?: PickItem<T>[]
  provider?: (q: string, signal: AbortSignal) => Promise<PickItem<T>[]> | PickItem<T>[]
  initial?: number
  noFilter?: boolean
  anchor?: { left: number; top: number }
  codeLabel?: boolean
  pathDetail?: boolean
}): Promise<T | null> {
  return new Promise((resolve) => setPickReq({ ...(o as any), resolve }))
}

/** Fuzzy score: every query character in order, bonus for consecutive and word starts. */
export function fuzzy(q: string, text: string): number {
  if (!q) return 1
  const t = text.toLowerCase()
  const query = q.toLowerCase()
  let score = 0
  let ti = 0
  let prev = -2
  for (const ch of query) {
    const i = t.indexOf(ch, ti)
    if (i < 0) return 0
    score += i === prev + 1 ? 5 : 1
    if (i === 0 || /[/_\-. A-Z]/.test(text[i - 1] ?? '') || text[i] !== t[i]) score += 3
    prev = i
    ti = i + 1
  }
  return score - t.length * 0.01
}

function PickHost() {
  let input!: HTMLInputElement
  let list!: HTMLDivElement
  const [query, setQuery] = createSignal('')
  const [items, setItems] = createSignal<PickItem[]>([])
  const [index, setIndex] = createSignal(0)
  const [busy, setBusy] = createSignal(false)
  let ctrl: AbortController | null = null

  const close = (v: unknown) => {
    ctrl?.abort()
    pickReq()?.resolve(v)
    setPickReq(null)
  }

  createEffect(() => {
    const r = pickReq()
    if (!r) return
    setQuery('')
    setIndex(r.initial ?? 0)
    queueMicrotask(() => input?.focus())
  })

  createEffect(() => {
    const r = pickReq()
    if (!r) return
    const q = query()
    if (r.provider) {
      ctrl?.abort()
      ctrl = new AbortController()
      const c = ctrl
      setBusy(true)
      Promise.resolve(r.provider(q, c.signal))
        .then((res) => {
          if (!c.signal.aborted) {
            setItems(res)
            setIndex((i) => Math.min(i, Math.max(0, res.length - 1)))
          }
        })
        .catch(() => {})
        .finally(() => !c.signal.aborted && setBusy(false))
    } else {
      const all = r.items ?? []
      if (!q || r.noFilter) setItems(all)
      else
        setItems(
          all
            .map((it) => ({ it, s: Math.max(fuzzy(q, it.label), fuzzy(q, it.detail ?? '') * 0.8) }))
            .filter((x) => x.s > 0)
            .sort((a, b) => b.s - a.s)
            .map((x) => x.it),
        )
      if (q) setIndex(0)
    }
  })

  createEffect(() => {
    const i = index()
    list?.children[i]?.scrollIntoView({ block: 'nearest' })
  })

  const key = (e: KeyboardEvent) => {
    const n = items().length
    if (e.key === 'ArrowDown') setIndex((i) => (n ? (i + 1) % n : 0))
    else if (e.key === 'ArrowUp') setIndex((i) => (n ? (i - 1 + n) % n : 0))
    else if (e.key === 'PageDown') setIndex((i) => Math.min(n - 1, i + 10))
    else if (e.key === 'PageUp') setIndex((i) => Math.max(0, i - 10))
    else if (e.key === 'Enter') {
      const it = items()[index()]
      if (it) close(it.value)
    } else if (e.key === 'Escape') close(null)
    else return
    e.preventDefault()
    e.stopPropagation()
  }

  return (
    <Show when={pickReq()}>
      {(r) => (
        <Portal>
          <div class="pick-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close(null)}>
            <div
              class="pick"
              classList={{ anchored: !!r().anchor }}
              style={r().anchor ? { left: `${Math.min(r().anchor!.left, innerWidth - 520)}px`, top: `${Math.min(r().anchor!.top, innerHeight - 340)}px` } : undefined}
              onKeyDown={key}
            >
              <input ref={input} class="pick-input" placeholder={r().placeholder} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
              <div class="pick-list" ref={list} role="listbox">
                <For each={items()} fallback={<div class="pick-empty">{busy() ? t('Searching…') : t('No result')}</div>}>
                  {(it, i) => (
                    <div
                      class="pick-item"
                      role="option"
                      aria-selected={i() === index()}
                      classList={{ selected: i() === index() }}
                      onMouseEnter={() => setIndex(i())}
                      onMouseDown={(e) => {
                        e.preventDefault()
                        close(it.value)
                      }}
                    >
                      <Show when={it.icon}>
                        <span class="pick-icon">{it.icon}</span>
                      </Show>
                      <span class="pick-label" classList={{ mono: r().codeLabel }}>{it.label}</span>
                      <Show when={it.detail}>
                        <span class="pick-detail" classList={{ mono: r().pathDetail }}>{it.detail}</span>
                      </Show>
                      <Show when={it.hint}>
                        <kbd class="pick-hint">{it.hint}</kbd>
                      </Show>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </div>
        </Portal>
      )}
    </Show>
  )
}

// ---------- context menu ----------

export interface MenuItem {
  label: string
  action?: () => void
  disabled?: boolean
  hint?: string
  danger?: boolean
  separator?: boolean
  /** A setting switched by the item: a check box shows its state. */
  checked?: boolean
}

interface MenuState {
  x: number
  y: number
  items: MenuItem[]
  /** Left / Right arrows: the menu bar opens the menu beside. */
  onSide?: (dir: -1 | 1) => void
  /** Focused before the menu opened, focused again when it closes. */
  prev: HTMLElement | null
}

const [menu, setMenu] = createSignal<MenuState | null>(null)

/** Focused element outside the menus (a menu opened from another one keeps the first one). */
export function focusBeforeMenus(): HTMLElement | null {
  const a = document.activeElement as HTMLElement | null
  return a?.closest('.ctx-menu') ? (menu()?.prev ?? null) : a
}

export function contextMenu(e: MouseEvent, items: MenuItem[], opts: { onSide?: (dir: -1 | 1) => void } = {}) {
  e.preventDefault()
  e.stopPropagation()
  setMenu({ x: e.clientX, y: e.clientY, items, onSide: opts.onSide, prev: focusBeforeMenus() })
}

function MenuHost() {
  let el!: HTMLDivElement
  const close = () => {
    const prev = menu()?.prev
    setMenu(null)
    if (prev && prev !== document.body && prev.isConnected) prev.focus()
  }
  // The keyboard moves between the items: arrows, Home / End, Tab closes.
  const entries = () => [...el.querySelectorAll<HTMLButtonElement>('.ctx-item:not(:disabled)')]
  const onKey = (e: KeyboardEvent) => {
    const list = entries()
    const i = list.indexOf(document.activeElement as HTMLButtonElement)
    let to = -1
    if (e.key === 'ArrowDown') to = (i + 1) % list.length
    else if (e.key === 'ArrowUp') to = i < 0 ? list.length - 1 : (i - 1 + list.length) % list.length
    else if (e.key === 'Home') to = 0
    else if (e.key === 'End') to = list.length - 1
    else if ((e.key === 'ArrowLeft' || e.key === 'ArrowRight') && menu()?.onSide) menu()!.onSide!(e.key === 'ArrowLeft' ? -1 : 1)
    else if (e.key === 'Tab') close()
    else return
    e.preventDefault()
    e.stopPropagation()
    if (to >= 0) list[to]?.focus()
  }
  // Each menu opened (another one of the menu bar included) puts the keyboard on its first item.
  createEffect(() => menu() && queueMicrotask(() => entries()[0]?.focus()))
  onMount(() => {
    const down = (e: MouseEvent) => {
      if (menu() && !el?.contains(e.target as Node)) close()
    }
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && menu()) {
        e.preventDefault()
        e.stopPropagation()
        close()
      }
    }
    window.addEventListener('mousedown', down, true)
    window.addEventListener('keydown', key, true)
    window.addEventListener('blur', close)
    onCleanup(() => {
      window.removeEventListener('mousedown', down, true)
      window.removeEventListener('keydown', key, true)
      window.removeEventListener('blur', close)
    })
  })
  return (
    <Show when={menu()}>
      {(m) => (
        <Portal>
          <div
            ref={el}
            onKeyDown={onKey}
            class="ctx-menu"
            classList={{ 'has-checks': m().items.some((it) => it.checked !== undefined) }}
            role="menu"
            style={{ left: `${Math.min(m().x, innerWidth - 240)}px`, top: `${Math.min(m().y, innerHeight - m().items.length * 28 - 12)}px` }}>
            <For each={m().items}>
              {(it) =>
                it.separator ? (
                  <div class="ctx-sep" />
                ) : (
                  <button
                    class="ctx-item"
                    role={it.checked === undefined ? 'menuitem' : 'menuitemcheckbox'}
                    aria-checked={it.checked}
                    classList={{ danger: it.danger }}
                    disabled={it.disabled}
                    onClick={() => {
                      close()
                      it.action?.()
                    }}
                  >
                    <span class="ctx-label">
                      <span class="ctx-check" classList={{ on: it.checked, box: it.checked !== undefined }} />
                      {it.label}
                    </span>
                    <Show when={it.hint}>
                      <kbd>{it.hint}</kbd>
                    </Show>
                  </button>
                )
              }
            </For>
          </div>
        </Portal>
      )}
    </Show>
  )
}

export function Overlays() {
  return (
    <>
      <PromptHost />
      <PickHost />
      <MenuHost />
    </>
  )
}
