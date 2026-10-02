import { createSignal, For } from 'solid-js'

export type ToastKind = 'info' | 'ok' | 'warn' | 'error'
interface Toast {
  id: number
  kind: ToastKind
  text: string
  action?: { label: string; run: () => void }
}

const [toasts, setToasts] = createSignal<Toast[]>([])
let seq = 0

export function toast(text: string, kind: ToastKind = 'info', action?: Toast['action'], ms = kind === 'error' ? 8000 : 3500) {
  const id = ++seq
  setToasts((t) => [...t.slice(-4), { id, kind, text, action }])
  setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), ms)
}

export function errorToast(e: unknown) {
  toast(e instanceof Error ? e.message : String(e), 'error')
}

export function Toasts() {
  return (
    <div class="toasts" role="status" aria-live="polite">
      <For each={toasts()}>
        {(t) => (
          <div class={`toast toast-${t.kind}`}>
            <span>{t.text}</span>
            {t.action && (
              <button
                class="link"
                onClick={() => {
                  t.action!.run()
                  setToasts((l) => l.filter((x) => x.id !== t.id))
                }}
              >
                {t.action.label}
              </button>
            )}
          </div>
        )}
      </For>
    </div>
  )
}
