// Tools of the right panel (besides the Database explorer): conflicts, structure of the
// active file, language servers (extensions), properties.
import { createEffect, createResource, createSignal, For, on, onCleanup, Show } from 'solid-js'
import { request, on as onPod } from '../pod/rpc'
import { activeTab, basename, conflictedDocs, docsVersion, getDoc, openFile, project, relPath, root, isLocal, diagnostics } from '../state/project'
import { openConflict } from '../conflict/ConflictDialog'
import * as lspc from '../lsp/client'
import { shortcutOf } from '../keys/bindings'
import { languageName } from '../editor/languages'

export function ConflictsTool() {
  const list = () => (docsVersion(), conflictedDocs())
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">Conflits</span>
      </div>
      <div class="panel-body pad">
        <p class="muted small">
          Fichiers modifiés sur le disque (par l'IA ou un autre outil) pendant que vous les éditiez, et dont la fusion automatique a échoué. Résolution : {shortcutOf('conflict.resolve')}.
        </p>
        <For each={list()} fallback={<p class="muted">Aucun conflit en cours.</p>}>
          {(d) => (
            <div class="conflict-item">
              <button class="link" onClick={() => openFile(d.path)}>
                {relPath(d.path)}
              </button>
              <span class="muted small">rév. distante {d.conflict()?.rev}</span>
              <button class="btn small primary" onClick={() => openConflict(d)}>
                Résoudre
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
        <span class="panel-title">Structure</span>
        <span class="grow" />
        <button class="icon-btn" title="Rafraîchir" onClick={() => setTick((t) => t + 1)}>
          ↻
        </button>
      </div>
      <div class="panel-body tree">
        <Show when={path()} fallback={<p class="muted pad">Aucun fichier actif.</p>}>
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
                <span class="sym-kind" title={lspc.symbolKinds[s.kind]?.[0]}>
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
        <span class="panel-title">Extensions · serveurs de langage</span>
        <span class="grow" />
        <button class="icon-btn" title="Rafraîchir" onClick={refetch}>
          ↻
        </button>
      </div>
      <div class="panel-body pad">
        <p class="muted small">
          Le pod lance un serveur par langage détecté (go.mod, composer.json, package.json, pyproject.toml…) {isLocal() ? 'sur cette machine' : "sur l'hôte SSH"}, et l'arrête deux minutes après la fermeture de la dernière fenêtre du projet. Commande personnalisable dans <code>.ide/project.json</code> (clé <code>lsp</code>).
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
              <span class={`badge ${s.running ? 'ok' : ''}`}>{s.running ? 'actif' : s.detected ? 'détecté' : 'non détecté'}</span>
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
        <span class="panel-title">Propriétés</span>
      </div>
      <div class="panel-body pad">
        <section class="card">
          <h3>Projet</h3>
          <dl class="props">
            <dt>Nom</dt>
            <dd>{project()?.name}</dd>
            <Show when={project()?.description}>
              <dt>Description</dt>
              <dd>{project()!.description}</dd>
            </Show>
            <dt>Cible</dt>
            <dd class="mono">{isLocal() ? root() : `${project()?.ssh?.host}:${root()}`}</dd>
          </dl>
        </section>
        <Show when={tab()}>
          <section class="card">
            <h3>Onglet actif</h3>
            <dl class="props">
              <Show when={tab()!.kind === 'file'} fallback={<><dt>Type</dt><dd>{tab()!.kind}</dd></>}>
                <dt>Fichier</dt>
                <dd>{basename(tab()!.path!)}</dd>
                <dt>Chemin</dt>
                <dd class="mono small">{relPath(tab()!.path!)}</dd>
                <Show when={doc()}>
                  <dt>Langage</dt>
                  <dd>{languageName(doc()!.lang)}</dd>
                  <dt>Lignes</dt>
                  <dd>{(doc()!.changed(), doc()!.lineCount)}</dd>
                  <dt>État</dt>
                  <dd>{doc()!.conflict() ? 'en conflit' : doc()!.dirty() ? 'modifié' : 'enregistré'}{doc()!.readOnly ? ' · lecture seule' : ''}</dd>
                  <dt>Révision</dt>
                  <dd>{doc()!.baseRev}</dd>
                  <dt>Fin de ligne</dt>
                  <dd>{doc()!.text.includes('\r\n') ? 'CRLF' : 'LF'}</dd>
                </Show>
                <Show when={stat()}>
                  <dt>Taille</dt>
                  <dd>{stat()!.size.toLocaleString()} octets</dd>
                  <dt>Modifié le</dt>
                  <dd>{new Date(stat()!.mtime).toLocaleString()}</dd>
                </Show>
                <dt>Diagnostics</dt>
                <dd>{errors()}</dd>
              </Show>
            </dl>
          </section>
        </Show>
      </div>
    </div>
  )
}
