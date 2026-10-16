import { render } from 'solid-js/web'
import { Match, onMount, Switch } from 'solid-js'
import './styles.css'
import { startPod } from './pod/rpc'
import { traceBoot } from './pod/transport'
import { installPush, installVisibility } from './pod/push'
import { loadSettings } from './state/settings'
import { detectLayout, installKeyHandler } from './keys/bindings'
import { installMouseNav } from './keys/mouse'
import { installRoving } from './ui/roving'
import { route } from './app/router'
import { Home } from './pages/Home'
import { ProjectPage } from './pages/ProjectPage'
import { DetachedConsole, DetachedEditor, DetachedTool } from './pages/Detached'
import { Overlays } from './ui/overlay'
import { Toasts } from './ui/toast'
import { SettingsHost } from './settings/SettingsModal'
import { ConflictHost } from './conflict/ConflictDialog'

traceBoot('start') // the bundle is parsed and runs

function App() {
  onMount(() => {
    installVisibility() // its first report waits for the connection
    installPush() // a pod this window reaches later gets this device's subscription
    startPod()
    loadSettings()
    detectLayout()
    installKeyHandler()
    installMouseNav()
    installRoving()
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
        {/* Keyed on the id only: switching desks keeps the page (and its buffers). */}
        <Match when={route().name === 'project' && (route() as { id: string }).id} keyed>
          {(id) => <ProjectPage id={id} />}
        </Match>
        <Match when={route().name === 'editor' && route()} keyed>
          {(r) => <DetachedEditor id={(r as any).id} />}
        </Match>
        <Match when={route().name === 'tool' && route()} keyed>
          {(r) => <DetachedTool id={(r as any).id} toolId={(r as any).toolId} />}
        </Match>
        <Match when={route().name === 'console' && route()} keyed>
          {(r) => <DetachedConsole id={(r as any).id} consoleId={(r as any).consoleId} />}
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
