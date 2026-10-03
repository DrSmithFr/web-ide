// Detached windows: one panel alone, without the rest of the chrome. Each window is one more
// WebSocket client of the same pod session; the shared buffers keep them in sync.
import { createSignal, onCleanup, onMount, Show } from 'solid-js'
import { Dynamic } from 'solid-js/web'
import { project, closeProject, reopenProject } from '../state/project'
import { EditorArea } from '../ui/EditorArea'
import { TermView, consoles, setConsoleList } from '../console/consoles'
import { NewTicketHost } from '../kanban/Board'
import { leftPanels, openWithAuth, rightPanels, useProjectActions, PodStatus } from './ProjectPage'
import { on as onPod, request } from '../pod/rpc'
import { t } from '../i18n'

function useProject(id: string, title: (name: string) => string) {
  const [ready, setReady] = createSignal(false)
  const [error, setError] = createSignal('')
  onMount(async () => {
    try {
      await openWithAuth(id)
      setReady(true)
      document.title = title(project()?.name ?? '')
    } catch (e) {
      setError((e as Error).message)
    }
  })
  const off = onPod('pod.reconnected', async () => {
    await reopenProject()
    request('console.list').then(setConsoleList).catch(() => {})
  })
  onCleanup(() => {
    off()
    closeProject()
  })
  return { ready, error }
}

function Frame(props: { title: string; ready: boolean; error: string; children: any }) {
  return (
    <div class="detached">
      <header class="detached-head">
        <span>{props.title}</span>
        <span class="grow" />
        <PodStatus />
      </header>
      <div class="detached-body">
        <Show when={props.ready} fallback={<div class="center-msg">{props.error ? <p class="danger">{props.error}</p> : <p class="muted">{t('Connecting…')}</p>}</div>}>
          {props.children}
        </Show>
      </div>
    </div>
  )
}

export function DetachedEditor(props: { id: string }) {
  const s = useProject(props.id, (n) => t('{name} · editor', { name: n }))
  useProjectActions()
  return (
    <Frame title={`${project()?.name ?? ''} · ${t('editor')}`} ready={s.ready()} error={s.error()}>
      <EditorArea detached />
    </Frame>
  )
}

export function DetachedConsole(props: { id: string; consoleId: string }) {
  const s = useProject(props.id, () => 'Console')
  const info = () => consoles().find((c) => c.id === props.consoleId)
  return (
    <Frame title={info()?.title ?? 'Console'} ready={s.ready()} error={s.error()}>
      <Show when={info()} fallback={<p class="muted pad">{t('This console does not exist anymore.')}</p>}>
        <div class="term-full">
          <TermView id={props.consoleId} focus />
        </div>
      </Show>
    </Frame>
  )
}

export function DetachedTool(props: { id: string; toolId: string }) {
  const s = useProject(props.id, () => 'Tool')
  useProjectActions()
  const tool = () => rightPanels[props.toolId] ?? leftPanels[props.toolId]
  return (
    <Frame title={`${tool() ? t(tool()!.label) : props.toolId} · ${project()?.name ?? ''}`} ready={s.ready()} error={s.error()}>
      <Show when={tool()} fallback={<p class="muted pad">{t('Unknown tool.')}</p>}>
        <div class="tool-full">
          <Dynamic component={tool()!.component} />
        </div>
        <NewTicketHost />
      </Show>
    </Frame>
  )
}
