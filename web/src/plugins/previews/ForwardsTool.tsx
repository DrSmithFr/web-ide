// Port forwards and previews tool: the port forwards of an SSH project and the app previews of
// the pod (before #114 they were tabs of the Docker tool), the tool of the Previews plugin.
import { createSignal, Show } from 'solid-js'
import { api, t } from './plugin'
import { ForwardsTab } from './ForwardsTab'
import { PreviewsTab } from './PreviewsTab'

export function ForwardsTool() {
  // An SSH project opens on its port forwards, a local one on the previews.
  const [picked, setPicked] = createSignal<'forwards' | 'previews'>('forwards')
  const tab = () => (picked() === 'forwards' && !api.project.isLocal() ? 'forwards' : 'previews')
  return (
    <div class="panel docker-tool" data-testid="forwards-tool">
      <div class="tabbar tool-tabbar" role="tablist">
        <Show when={!api.project.isLocal()}>
          <div class="tab" role="tab" data-testid="forwards-tab-forwards" aria-selected={tab() === 'forwards'} classList={{ active: tab() === 'forwards' }} onClick={() => setPicked('forwards')}>
            <span class="tab-title">{t('Port forwards')}</span>
          </div>
        </Show>
        <div class="tab" role="tab" data-testid="forwards-tab-previews" aria-selected={tab() === 'previews'} classList={{ active: tab() === 'previews' }} onClick={() => setPicked('previews')}>
          <span class="tab-title">{t('Previews')}</span>
        </div>
      </div>
      <div class="tool-body">
        <Show when={tab() === 'forwards'} fallback={<PreviewsTab />}>
          <ForwardsTab />
        </Show>
      </div>
    </div>
  )
}
