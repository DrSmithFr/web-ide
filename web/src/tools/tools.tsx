// Tools of the right panel (besides the Database explorer): conflicts, structure of the
// active file, language servers (extensions), properties.
import { createEffect, createResource, createSignal, For, on, onCleanup, Show } from 'solid-js'
import { request, on as onPod } from '../pod/rpc'
import { activeTab, basename, conflictedDocs, docsVersion, getDoc, openFile, project, relPath, root, isLocal, diagnostics } from '../state/project'
import { openConflict } from '../conflict/ConflictDialog'
import * as lspc from '../lsp/client'
import { shortcutOf } from '../keys/bindings'
import { languageName } from '../editor/languages'
import { fmtDate, fmtNumber, t } from '../i18n'

export function ConflictsTool() {
  const list = () => (docsVersion(), conflictedDocs())
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Conflicts')}</span>
      </div>
      <div class="panel-body pad">
        <p class="muted small">
          {t('Files changed on disk (by the AI or another tool) while you were editing them, and whose automatic merge failed. Resolution: {shortcut}.', { shortcut: shortcutOf('conflict.resolve') })}
        </p>
        <For each={list()} fallback={<p class="muted">{t('No conflict.')}</p>}>
          {(d) => (
            <div class="conflict-item">
              <button class="link" onClick={() => openFile(d.path)}>
                {relPath(d.path)}
              </button>
              <span class="muted small">{t('remote rev. {rev}', { rev: d.conflict()?.rev ?? '' })}</span>
              <button class="btn small primary" onClick={() => openConflict(d)}>
                {t('Resolve')}
              </button>
            </div>
          )}
        </For>
      </div>
    </div>
  )
}

export function StructureTool() {
  const path = () => (activeTab()?.kind === 'file' ? activeTab()!.path! : '')
  const [tick, setTick] = createSignal(0)
  let timer: number | undefined
  createEffect(
    on([path, docsVersion], () => {
      const d = path() ? getDoc(path()) : null
      if (!d) return
      const off = d.onChange(() => {
        clearTimeout(timer)
        timer = window.setTimeout(() => setTick((t) => t + 1), 1200)
      })
      onCleanup(off)
    }),
  )
  const [symbols] = createResource(
    () => (path() ? { p: path(), t: tick(), v: docsVersion() } : null),
    async ({ p }) => {
      try {
        return { list: lspc.flatten(await lspc.documentSymbols(p)), error: '' }
      } catch (e) {
        return { list: [], error: (e as Error).message }
      }
    },
  )
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Structure')}</span>
        <span class="grow" />
        <button class="icon-btn" title={t('Refresh')} onClick={() => setTick((t) => t + 1)}>
          ↻
        </button>
      </div>
      <div class="panel-body tree">
        <Show when={path()} fallback={<p class="muted pad">{t('No active file.')}</p>}>
          <Show when={symbols()?.error}>
            <p class="muted pad small">{symbols()!.error}</p>
          </Show>
          <For each={symbols()?.list ?? []}>
            {({ s, depth }) => (
              <div
                class="tree-row"
                style={{ 'padding-left': `${depth * 14 + 8}px` }}
                onClick={() => openFile({ path: path(), line: s.selectionRange.start.line, col: s.selectionRange.start.character })}
              >
                <span class="sym-kind" title={t(lspc.symbolKinds[s.kind]?.[0] ?? "")}>
                  {lspc.symbolKinds[s.kind]?.[1] ?? '·'}
                </span>
                <span class="tree-name">{s.name}</span>
                <Show when={s.detail}>
                  <span class="tree-detail">{s.detail}</span>
                </Show>
              </div>
            )}
          </For>
        </Show>
      </div>
    </div>
  )
}

export function ExtensionsTool() {
  const [status, { refetch }] = createResource(() => request<any[]>('lsp.status').catch(() => []))
  const off = onPod('lsp.status', () => refetch())
  onCleanup(off)
  const names: Record<string, string> = { go: 'Go', php: 'PHP', python: 'Python', typescript: 'JavaScript / TypeScript' }
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Extensions · language servers')}</span>
        <span class="grow" />
        <button class="icon-btn" title={t('Refresh')} onClick={refetch}>
          ↻
        </button>
      </div>
      <div class="panel-body pad">
        <p class="muted small">
          {isLocal()
            ? t('The pod runs a server per detected language (go.mod, composer.json, package.json, pyproject.toml…) on this machine, and stops it two minutes after the last window of the project is closed. Command configurable in {file} (key {key}).', { file: '.ide/project.json', key: 'lsp' })
            : t('The pod runs a server per detected language (go.mod, composer.json, package.json, pyproject.toml…) on the SSH host, and stops it two minutes after the last window of the project is closed. Command configurable in {file} (key {key}).', { file: '.ide/project.json', key: 'lsp' })}
        </p>
        <For each={status() ?? []}>
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
      </div>
    </div>
  )
}

export function PropertiesTool() {
  const tab = () => activeTab()
  const doc = () => (docsVersion(), tab()?.kind === 'file' ? getDoc(tab()!.path!) : null)
  const [stat] = createResource(
    () => (tab()?.kind === 'file' ? tab()!.path : null),
    (p) => request('fs.stat', { path: p }).catch(() => null),
  )
  const errors = () => (tab()?.path ? (diagnostics[tab()!.path!] ?? []).length : 0)
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Properties')}</span>
      </div>
      <div class="panel-body pad">
        <section class="card">
          <h3>{t('Project')}</h3>
          <dl class="props">
            <dt>{t('Name')}</dt>
            <dd>{project()?.name}</dd>
            <Show when={project()?.description}>
              <dt>{t('Description')}</dt>
              <dd>{project()!.description}</dd>
            </Show>
            <dt>{t('Target')}</dt>
            <dd class="mono">{isLocal() ? root() : `${project()?.ssh?.host}:${root()}`}</dd>
          </dl>
        </section>
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
      </div>
    </div>
  )
}
