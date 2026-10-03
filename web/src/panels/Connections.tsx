// Connections panel: target of the project (local or SSH) and the local SSH key set.
import { createResource, For, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { isLocal, project, root } from '../state/project'
import { newConsole } from '../console/consoles'
import { podState } from '../pod/rpc'
import { t } from '../i18n'

export function Connections() {
  const [info, { refetch }] = createResource(() => request('ssh.info'))
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Connections')}</span>
        <span class="grow" />
        <button class="icon-btn" title={t('Refresh')} onClick={refetch}>
          ↻
        </button>
      </div>
      <div class="panel-body pad">
        <section class="card">
          <h3>{t('Project')}</h3>
          <dl class="props">
            <dt>{t('Type')}</dt>
            <dd>{isLocal() ? 'Local' : 'SSH'}</dd>
            <Show when={!isLocal() && project()?.ssh}>
              <dt>{t('Host')}</dt>
              <dd>
                {project()!.ssh!.user ? `${project()!.ssh!.user}@` : ''}
                {project()!.ssh!.host}:{project()!.ssh!.port}
              </dd>
              <dt>{t('Authentication')}</dt>
              <dd>{t({ agent: 'Agent + default keys', key: 'Dedicated key', password: 'Password' }[project()!.ssh!.auth] ?? project()!.ssh!.auth)}</dd>
            </Show>
            <dt>{t('Root')}</dt>
            <dd class="mono">{root()}</dd>
            <dt>Pod</dt>
            <dd>
              <span class={`dot dot-${podState()}`} /> {t(podState())}
            </dd>
          </dl>
        </section>
        <section class="card">
          <h3>{t('Local SSH keys')}</h3>
          <p class="muted small">{t('Private keys stay on the machine of the pod; only their names are shown.')}</p>
          <p class="small">{info()?.agent ? t('SSH agent: available') : t('SSH agent: missing')}</p>
          <ul class="plain-list mono small">
            <For each={info()?.keys ?? []} fallback={<li class="muted">{t('No key in ~/.ssh')}</li>}>
              {(k: string) => <li>{k}</li>}
            </For>
          </ul>
        </section>
        <section class="card">
          <h3>{t('Hosts of ~/.ssh/config')}</h3>
          <ul class="plain-list">
            <For each={info()?.hosts ?? []} fallback={<li class="muted small">{t('No host declared')}</li>}>
              {(h: any) => (
                <li class="host-row">
                  <div>
                    <div>{h.alias}</div>
                    <div class="muted small mono">
                      {h.user ? `${h.user}@` : ''}
                      {h.hostName || h.alias}
                      {h.port ? `:${h.port}` : ''}
                    </div>
                  </div>
                  <Show when={isLocal()}>
                    <button class="btn small" title={t('Open a terminal: ssh {host}', { host: h.alias })} onClick={() => newConsole({ command: ['ssh', h.alias], title: `ssh ${h.alias}` })}>
                      {t('Terminal')}
                    </button>
                  </Show>
                </li>
              )}
            </For>
          </ul>
        </section>
      </div>
    </div>
  )
}
