// Forwards tool: the port forwards and the app previews of the core, in their own tool when the
// Docker plugin, which shows them as tabs, is disabled on the project.
import { createSignal, Show } from 'solid-js'
import { isLocal } from '../state/project'
import { t } from '../i18n'
import { ForwardsTab } from './ForwardsTab'
import { PreviewsTab } from './PreviewsTab'

export function ForwardsTool() {
  const [picked, setPicked] = createSignal<'forwards' | 'previews'>('previews')
  const tab = () => (picked() === 'forwards' && !isLocal() ? 'forwards' : 'previews')
  return (
    <div class="panel docker-tool" data-testid="forwards-tool">
      <div class="tabbar tool-tabbar" role="tablist">
        <Show when={!isLocal()}>
          <div class="tab" role="tab" aria-selected={tab() === 'forwards'} classList={{ active: tab() === 'forwards' }} onClick={() => setPicked('forwards')}>
            <span class="tab-title">{t('Port forwards')}</span>
          </div>
        </Show>
        <div class="tab" role="tab" aria-selected={tab() === 'previews'} classList={{ active: tab() === 'previews' }} onClick={() => setPicked('previews')}>
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
