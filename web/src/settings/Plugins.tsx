// Settings > Plugins, with a project open: the plugins of the pod, enabled or disabled on the
// project (.ide/plugins.json of its main project), their permissions and capabilities.
import { Index, Show } from 'solid-js'
import { pluginStates, setPluginEnabled } from '../plugins/registry'
import type { PluginState } from '../plugins/types'
import { errorToast } from '../ui/toast'
import { t } from '../i18n'

/** The enabled plugins requiring a capability that the plugin provides (and the core does not). */
function dependents(s: PluginState): string[] {
  const provides = s.manifest.provides ?? []
  return pluginStates()
    .filter((o) => o !== s && o.enabled && (o.manifest.requires ?? []).some((c) => provides.includes(c)))
    .map((o) => o.manifest.name)
}

export function PluginsSection() {
  return (
    <section class="form" data-testid="plugins">
      <p class="muted small">{t('Plugins add tools to the IDE. They are enabled per project, for every window of the project and its worktrees.')}</p>
      {/* Index: a row stays when its state changes, so its checkbox keeps the focus. */}
      <Index each={pluginStates()} fallback={<p class="muted">{t('No plugin in this pod.')}</p>}>
        {(s) => (
          <div class="plugin-row" data-plugin={s().manifest.id}>
            <label class="check">
              <input type="checkbox" checked={!s().disabled || !!s().manifest.required} disabled={!!s().manifest.required} data-testid={`plugin-${s().manifest.id}`}
                onChange={(e) => {
                  const on = e.currentTarget.checked
                  const deps = dependents(s())
                  if (!on && deps.length && !confirm(t('These plugins need {name} and will stop: {list}', { name: s().manifest.name, list: deps.join(', ') }))) {
                    e.currentTarget.checked = true
                    return
                  }
                  void setPluginEnabled(s().manifest.id, on).catch(errorToast)
                }} />
              {t(s().manifest.name)} <span class="muted small">{s().manifest.version}</span>
              <Show when={s().manifest.required}>
                <span class="muted small">{t('Always on')}</span>
              </Show>
            </label>
            <Show when={s().missing?.length}>
              <p class="err small">{t('Off: needs {list}', { list: s().missing!.join(', ') })}</p>
            </Show>
            <Show when={s().manifest.permissions?.length}>
              <p class="muted small">{t('Permissions: {list}', { list: s().manifest.permissions!.join(', ') })}</p>
            </Show>
          </div>
        )}
      </Index>
    </section>
  )
}
