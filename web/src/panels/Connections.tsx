// Connections panel: target of the project (local or SSH) and the local SSH key set.
import { createResource, For, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { isLocal, project, root } from '../state/project'
import { newConsole } from '../console/consoles'
import { podState } from '../pod/rpc'

export function Connections() {
  const [info, { refetch }] = createResource(() => request('ssh.info'))
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">Connexions</span>
        <span class="grow" />
        <button class="icon-btn" title="Rafraîchir" onClick={refetch}>
          ↻
        </button>
      </div>
      <div class="panel-body pad">
        <section class="card">
          <h3>Projet</h3>
          <dl class="props">
            <dt>Type</dt>
            <dd>{isLocal() ? 'Local' : 'SSH'}</dd>
            <Show when={!isLocal() && project()?.ssh}>
              <dt>Hôte</dt>
              <dd>
                {project()!.ssh!.user ? `${project()!.ssh!.user}@` : ''}
                {project()!.ssh!.host}:{project()!.ssh!.port}
              </dd>
              <dt>Authentification</dt>
              <dd>{{ agent: 'Agent + clés par défaut', key: 'Clé dédiée', password: 'Mot de passe' }[project()!.ssh!.auth] ?? project()!.ssh!.auth}</dd>
            </Show>
            <dt>Racine</dt>
            <dd class="mono">{root()}</dd>
            <dt>Pod</dt>
            <dd>
              <span class={`dot dot-${podState()}`} /> {podState() === 'connected' ? 'connecté' : podState() === 'connecting' ? 'connexion…' : 'déconnecté'}
            </dd>
          </dl>
        </section>
        <section class="card">
          <h3>Clés SSH locales</h3>
          <p class="muted small">Les clés privées restent sur la machine du pod ; seuls leurs noms sont affichés.</p>
          <p class="small">Agent SSH : {info()?.agent ? 'disponible' : 'absent'}</p>
          <ul class="plain-list mono small">
            <For each={info()?.keys ?? []} fallback={<li class="muted">Aucune clé dans ~/.ssh</li>}>
              {(k: string) => <li>{k}</li>}
            </For>
          </ul>
        </section>
        <section class="card">
          <h3>Hôtes de ~/.ssh/config</h3>
          <ul class="plain-list">
            <For each={info()?.hosts ?? []} fallback={<li class="muted small">Aucun hôte déclaré</li>}>
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
                    <button class="btn small" title={`Ouvrir un terminal : ssh ${h.alias}`} onClick={() => newConsole({ command: ['ssh', h.alias], title: `ssh ${h.alias}` })}>
                      Terminal
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
