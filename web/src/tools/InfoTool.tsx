// Infos tool of the right panel: properties of the project and the active tab, connections
// (target of the project, local SSH keys and hosts), language servers (extensions).
import { createResource, For, onCleanup, Show } from 'solid-js'
import { request, on as onPod, podState } from '../pod/rpc'
import { activeTab, basename, docsVersion, getDoc, project, relPath, root, isLocal, diagnostics } from '../state/project'
import { newConsole } from '../console/consoles'
import { languageName } from '../editor/languages'
import { fmtDate, fmtNumber, t } from '../i18n'

export function InfoTool() {
  const [ssh, { refetch: refetchSsh }] = createResource(() => request<any>('ssh.info').catch(() => null))
  const [lsp, { refetch: refetchLsp }] = createResource(() => request<any[]>('lsp.status').catch(() => []))
  onCleanup(onPod('lsp.status', () => refetchLsp()))
  const refresh = () => {
    refetchSsh()
    refetchLsp()
  }
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Infos')}</span>
        <span class="grow" />
        <button class="icon-btn" title={t('Refresh')} onClick={refresh}>
          ↻
        </button>
      </div>
      <div class="panel-body pad">
        <section class="info-section" data-testid="info-properties">
          <h2>{t('Properties')}</h2>
          <ProjectCard />
          <ActiveTabCard />
        </section>
        <section class="info-section" data-testid="info-connections">
          <h2>{t('Connections')}</h2>
          <SshCards info={ssh()} />
        </section>
        <section class="info-section" data-testid="info-extensions">
          <h2>{t('Extensions · language servers')}</h2>
          <LanguageServers status={lsp() ?? []} />
        </section>
      </div>
    </div>
  )
}

function ProjectCard() {
  const ssh = () => project()?.ssh
  return (
    <section class="card">
      <h3>{t('Project')}</h3>
      <dl class="props">
        <dt>{t('Name')}</dt>
        <dd>{project()?.name}</dd>
        <Show when={project()?.description}>
          <dt>{t('Description')}</dt>
          <dd>{project()!.description}</dd>
        </Show>
        <dt>{t('Type')}</dt>
        <dd>{isLocal() ? 'Local' : 'SSH'}</dd>
        <Show when={!isLocal() && ssh()}>
          <dt>{t('Host')}</dt>
          <dd>
            {ssh()!.user ? `${ssh()!.user}@` : ''}
            {ssh()!.host}:{ssh()!.port}
          </dd>
          <dt>{t('Authentication')}</dt>
          <dd>{t({ agent: 'Agent + default keys', key: 'Dedicated key', password: 'Password' }[ssh()!.auth] ?? ssh()!.auth)}</dd>
        </Show>
        <dt>{t('Root')}</dt>
        <dd class="mono">{root()}</dd>
        <dt>Pod</dt>
        <dd>
          <span class={`dot dot-${podState()}`} /> {t(podState())}
        </dd>
      </dl>
    </section>
  )
}

function ActiveTabCard() {
  const tab = () => activeTab()
  const doc = () => (docsVersion(), tab()?.kind === 'file' ? getDoc(tab()!.path!) : null)
  const [stat] = createResource(
    () => (tab()?.kind === 'file' ? tab()!.path : null),
    (p) => request('fs.stat', { path: p }).catch(() => null),
  )
  const errors = () => (tab()?.path ? (diagnostics[tab()!.path!] ?? []).length : 0)
  return (
    <Show when={tab()}>
      <section class="card">
        <h3>{t('Active tab')}</h3>
        <dl class="props">
          <Show when={tab()!.kind === 'file'} fallback={<><dt>{t('Type')}</dt><dd>{tab()!.kind}</dd></>}>
            <dt>{t('File')}</dt>
            <dd>{basename(tab()!.path!)}</dd>
            <dt>{t('Path')}</dt>
            <dd class="mono small">{relPath(tab()!.path!)}</dd>
            <Show when={doc()}>
              <dt>{t('code|Language')}</dt>
              <dd>{languageName(doc()!.lang)}</dd>
              <dt>{t('Lines')}</dt>
              <dd>{(doc()!.changed(), doc()!.lineCount)}</dd>
              <dt>{t('State')}</dt>
              <dd>{doc()!.conflict() ? t('in conflict') : doc()!.dirty() ? t('modified') : t('saved')}{doc()!.readOnly ? ` · ${t('read-only')}` : ''}</dd>
              <dt>{t('Revision')}</dt>
              <dd>{doc()!.baseRev}</dd>
              <dt>{t('Line ending')}</dt>
              <dd>{doc()!.text.includes('\r\n') ? 'CRLF' : 'LF'}</dd>
            </Show>
            <Show when={stat()}>
              <dt>{t('Size')}</dt>
              <dd>{t('{n} bytes', { n: fmtNumber(stat()!.size) })}</dd>
              <dt>{t('Modified on')}</dt>
              <dd>{fmtDate(stat()!.mtime)}</dd>
            </Show>
            <dt>{t('Diagnostics')}</dt>
            <dd>{errors()}</dd>
          </Show>
        </dl>
      </section>
    </Show>
  )
}

function SshCards(props: { info: any }) {
  return (
    <>
      <section class="card">
        <h3>{t('Local SSH keys')}</h3>
        <p class="muted small">{t('Private keys stay on the machine of the pod; only their names are shown.')}</p>
        <p class="small">{props.info?.agent ? t('SSH agent: available') : t('SSH agent: missing')}</p>
        <ul class="plain-list mono small">
          <For each={props.info?.keys ?? []} fallback={<li class="muted">{t('No key in ~/.ssh')}</li>}>
            {(k: string) => <li>{k}</li>}
          </For>
        </ul>
      </section>
      <section class="card">
        <h3>{t('Hosts of ~/.ssh/config')}</h3>
        <ul class="plain-list">
          <For each={props.info?.hosts ?? []} fallback={<li class="muted small">{t('No host declared')}</li>}>
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
    </>
  )
}

function LanguageServers(props: { status: any[] }) {
  const names: Record<string, string> = { go: 'Go', php: 'PHP', python: 'Python', typescript: 'JavaScript / TypeScript' }
  return (
    <>
      <p class="muted small">
        {isLocal()
          ? t('The pod runs a server per detected language (go.mod, composer.json, package.json, pyproject.toml…) on this machine, and stops it two minutes after the last window of the project is closed. Command configurable in {file} (key {key}).', { file: '.ide/project.json', key: 'lsp' })
          : t('The pod runs a server per detected language (go.mod, composer.json, package.json, pyproject.toml…) on the SSH host, and stops it two minutes after the last window of the project is closed. Command configurable in {file} (key {key}).', { file: '.ide/project.json', key: 'lsp' })}
      </p>
      <For each={props.status}>
        {(s) => (
          <div class="ext-row" classList={{ dim: !s.detected && !s.running }}>
            <div>
              <strong>{names[s.lang] ?? s.lang}</strong>
              <div class="muted small mono">{s.command?.join(' ') ?? ''}</div>
              <Show when={s.error}>
                <div class="warn small">{s.error}</div>
              </Show>
            </div>
            <span class={`badge ${s.running ? 'ok' : ''}`}>{s.running ? t('running') : s.detected ? t('detected') : t('not detected')}</span>
          </div>
        )}
      </For>
    </>
  )
}
