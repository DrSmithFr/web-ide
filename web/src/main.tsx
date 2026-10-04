import { render } from 'solid-js/web'
import { Match, onMount, Switch } from 'solid-js'
import './styles.css'
import { startPod } from './pod/rpc'
import { loadSettings } from './state/settings'
import { detectLayout, installKeyHandler } from './keys/bindings'
import { route } from './app/router'
import { Home } from './pages/Home'
import { ProjectPage } from './pages/ProjectPage'
import { DetachedEditor, DetachedTool } from './pages/Detached'
import { Overlays } from './ui/overlay'
import { Toasts } from './ui/toast'
import { SettingsHost } from './settings/SettingsModal'
import { ConflictHost } from './conflict/ConflictDialog'

function App() {
  onMount(() => {
    startPod()
    loadSettings()
    detectLayout()
    installKeyHandler()
    // Keep the browser shortcuts (Ctrl+S "save page"...) away from the IDE.
    window.addEventListener('beforeunload', (e) => {
      if (document.querySelector('.tab.dirty')) e.preventDefault()
    })
  })
  return (
    <>
      <Switch>
        <Match when={route().name === 'home'}>
          <Home />
        </Match>
        <Match when={route().name === 'project' && route()} keyed>
          {(r) => <ProjectPage id={(r as any).id} />}
        </Match>
        <Match when={route().name === 'editor' && route()} keyed>
          {(r) => <DetachedEditor id={(r as any).id} />}
        </Match>
        <Match when={route().name === 'tool' && route()} keyed>
          {(r) => <DetachedTool id={(r as any).id} toolId={(r as any).toolId} />}
        </Match>
      </Switch>
      <SettingsHost />
      <ConflictHost />
      <Overlays />
      <Toasts />
    </>
  )
}

render(() => <App />, document.getElementById('app')!)
