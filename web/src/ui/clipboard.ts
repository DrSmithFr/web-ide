// Clipboard history: every text copied or cut in the page (copy events, and copyText for the
// copy buttons and menu entries) is kept by the pod, shared by every window and project.
// Copies made outside the browser cannot be seen without the clipboard-read permission.
import { createSignal } from 'solid-js'
import { on, request } from '../pod/rpc'
import { settings } from '../state/settings'
import { toast } from './toast'
import { t } from '../i18n'

export interface ClipEntry {
  text: string
  /** Milliseconds since the epoch. */
  at: number
}

const [entries, setEntries] = createSignal<ClipEntry[]>([])
export { entries as clipEntries }

on('clipboard.changed', (l: ClipEntry[]) => setEntries(l ?? []))

export async function loadClipboard() {
  setEntries(await request<ClipEntry[]>('clipboard.list').catch(() => entries()))
}

export function recordCopy(text: string) {
  if (text) request('clipboard.add', { text, max: settings.editor.clipboardSize }).catch(() => {})
}

export function forgetCopy(text: string) {
  return request('clipboard.remove', { text })
}

/** Writes a text to the clipboard and keeps it in the history; false (and a message) when the browser refuses. */
export async function copyText(text: string): Promise<boolean> {
  recordCopy(text)
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    toast(t('The browser refused to write to the clipboard'), 'warn')
    return false
  }
}

/** Selected text of a field, else of the page. */
function selectedText() {
  const a = document.activeElement
  if (a instanceof HTMLInputElement || a instanceof HTMLTextAreaElement) return a.value.slice(a.selectionStart ?? 0, a.selectionEnd ?? 0)
  return getSelection()?.toString() ?? ''
}

// The handlers of the editor and the terminals fill the clipboard data; a native copy leaves
// it empty, the selection is then the copied text.
for (const type of ['copy', 'cut'] as const)
  document.addEventListener(type, (e) => recordCopy(e.clipboardData?.getData('text/plain') || selectedText()))
