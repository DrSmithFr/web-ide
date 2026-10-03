// Settings of the assistant besides the servers: system prompt and instructions, and
// compaction of long conversations.
import { createResource, createSignal, For, onMount, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { openTextTab, relPath } from '../state/project'
import { errorToast, toast } from '../ui/toast'
import { config, prefs, savePrefs, setPrefs, type Model } from './state'
import { buildSystemPrompt, DEFAULT_TEMPLATE, loadPromptContext, promptContext, templateOf } from './prompt'

function size(n: number) {
  return n < 1024 ? `${n} o` : `${(n / 1024).toFixed(1)} Ko`
}

export function PromptSettings() {
  const [scope, setScope] = createSignal<'project' | 'global'>('project')
  const [text, setText] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  // Text typed before the context arrives is not replaced by it.
  let edited = false
  const load = async () => {
    const c = await loadPromptContext()
    if (!edited) setText((scope() === 'project' ? c.projectPrompt : c.globalPrompt) ?? '')
  }
  const edit = (v: string) => {
    edited = true
    setText(v)
  }
  onMount(load)
  const switchScope = (s: 'project' | 'global') => {
    edited = false
    setScope(s)
    const c = promptContext()
    setText((s === 'project' ? c?.projectPrompt : c?.globalPrompt) ?? '')
  }
  const save = async (content: string) => {
    setBusy(true)
    try {
      await request('llm.prompt.save', { scope: scope(), content })
      edited = false
      await load()
      toast(content.trim() ? 'Prompt enregistré' : 'Prompt par défaut rétabli', 'ok')
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
    }
  }
  const source = () => templateOf(promptContext()).source
  return (
    <div class="form" data-testid="prompt-settings">
      <p class="muted small">
        Le prompt système commence par ce modèle, puis viennent les fichiers d’instructions et la liste des skills, chargés comme Claude Code. Le prompt du projet (<code>.ide/system-prompt.md</code>) remplace le global. En ce moment :{' '}
        <strong>{source() === 'project' ? 'prompt du projet' : source() === 'global' ? 'prompt global' : 'prompt par défaut'}</strong>.
      </p>
      <div class="field-row">
        <button type="button" class="toggle" classList={{ on: scope() === 'project' }} onClick={() => switchScope('project')}>
          projet
        </button>
        <button type="button" class="toggle" classList={{ on: scope() === 'global' }} onClick={() => switchScope('global')}>
          global
        </button>
        <span class="grow" />
        <button type="button" class="btn small" onClick={() => edit(DEFAULT_TEMPLATE)}>
          Partir du prompt par défaut
        </button>
      </div>
      <textarea class="ai-prompt-edit mono" rows="12" value={text()} onInput={(e) => edit(e.currentTarget.value)} placeholder={`Vide : ${scope() === 'project' ? 'prompt global, sinon ' : ''}prompt par défaut.\n\n${DEFAULT_TEMPLATE}`} name="systemPrompt" />
      <p class="muted small">
        Variables : <code>{'{{project}}'}</code> <code>{'{{root}}'}</code> <code>{'{{host}}'}</code> <code>{'{{activeFile}}'}</code> <code>{'{{date}}'}</code> <code>{'{{tools}}'}</code> (description des outils, vide si désactivés).
      </p>
      <div class="form-actions">
        <button type="button" class="btn" onClick={() => openTextTab('Prompt système', buildSystemPrompt(promptContext(), true), 'markdown')}>
          Aperçu du prompt complet
        </button>
        <span class="grow" />
        <button type="button" class="btn" disabled={busy()} onClick={() => save('')}>
          Rétablir
        </button>
        <button type="button" class="btn primary" disabled={busy()} onClick={() => save(text())}>
          Enregistrer
        </button>
      </div>
      <fieldset class="fieldset">
        <legend>Fichiers d’instructions</legend>
        <p class="muted small">
          Globaux : <code>~/.claude/CLAUDE.md</code>, <code>~/.codex/AGENTS.md</code>. Projet : <code>CLAUDE.md</code>, <code>.claude/CLAUDE.md</code>, <code>CLAUDE.local.md</code>, <code>AGENTS.md</code>. Les imports <code>@chemin</code> sont suivis.
        </p>
        <For each={promptContext()?.files ?? []} fallback={<p class="muted small">Aucun fichier trouvé.</p>}>
          {(f) => (
            <div class="ai-server-row small" data-testid="instruction-file">
              <span class="badge">{f.scope === 'global' ? 'global' : 'projet'}</span>
              <span class="grow mono ellipsis">{f.scope === 'project' ? relPath(f.path) : f.path}</span>
              <span class="muted">{size(f.content.length)}</span>
            </div>
          )}
        </For>
      </fieldset>
      <fieldset class="fieldset">
        <legend>Skills</legend>
        <p class="muted small">
          <code>~/.claude/skills</code>, <code>~/.agents/skills</code> et les mêmes dossiers dans le projet (prioritaires). Seuls le nom et la description vont dans le prompt ; l’assistant charge le reste avec l’outil <code>load_skill</code>.
        </p>
        <For each={promptContext()?.skills ?? []} fallback={<p class="muted small">Aucun skill trouvé.</p>}>
          {(s) => (
            <div class="ai-server-row small" data-testid="skill">
              <span class="badge">{s.scope === 'global' ? 'global' : 'projet'}</span>
              <div class="grow">
                <strong>{s.name}</strong>
                <div class="muted">{s.description}</div>
              </div>
            </div>
          )}
        </For>
      </fieldset>
    </div>
  )
}

export function CompactionSettings() {
  const [list] = createResource(
    () => prefs.compactServer,
    (server) => (server ? request<{ models: Model[] }>('llm.models', { server }).then((r) => r.models, () => []) : Promise.resolve([] as Model[])),
  )
  const set = <K extends 'autoCompact' | 'compactAt' | 'compactServer' | 'compactModel'>(k: K, v: (typeof prefs)[K]) => {
    setPrefs(k, v)
    savePrefs()
  }
  return (
    <div class="form" data-testid="compaction-settings">
      <p class="muted small">
        Quand une conversation approche de la taille de contexte du modèle, les messages les plus anciens sont remplacés par un résumé (ils restent visibles, repliés). Les messages récents, environ un quart du contexte, sont gardés tels quels. Le bouton « compacter » sous la zone de message le fait à la demande.
      </p>
      <label class="check">
        <input type="checkbox" checked={prefs.autoCompact} onChange={(e) => set('autoCompact', e.currentTarget.checked)} />
        Compaction automatique
      </label>
      <label class="field">
        <span>Seuil : {prefs.compactAt} % du contexte</span>
        <input type="range" min="40" max="95" step="5" value={prefs.compactAt} onInput={(e) => set('compactAt', Number(e.currentTarget.value))} />
      </label>
      <div class="field-row">
        <label class="field grow">
          <span>Modèle qui résume</span>
          <select value={prefs.compactServer} onChange={(e) => (set('compactServer', e.currentTarget.value), set('compactModel', ''))} name="compactServer">
            <option value="">Le modèle de la conversation</option>
            <For each={config.servers}>{(s) => <option value={s.id}>{s.name}</option>}</For>
          </select>
        </label>
        <Show when={prefs.compactServer}>
          <label class="field grow">
            <span>Modèle</span>
            <select value={prefs.compactModel} onChange={(e) => set('compactModel', e.currentTarget.value)} name="compactModel">
              <option value="">— choisir —</option>
              <For each={list() ?? []}>{(m) => <option value={m.id}>{m.id}</option>}</For>
            </select>
          </label>
        </Show>
      </div>
      <Show when={prefs.compactServer && !prefs.compactModel}>
        <p class="warn small">Choisir le modèle qui résume, sinon la compaction échouera.</p>
      </Show>
    </div>
  )
}
