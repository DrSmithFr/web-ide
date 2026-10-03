// Settings of the assistant besides the servers: system prompt and instructions, and
// compaction of long conversations.
import { createResource, createSignal, For, onMount, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { openTextTab, relPath } from '../state/project'
import { Modal } from '../ui/overlay'
import { errorToast, toast } from '../ui/toast'
import { applyConfig, config, loadModels, prefs, savePrefs, select, setPrefs, type Model, type ServerView } from './state'
import { languages, probeGpu, speech, whisperModels } from './transcribe'
import { formatSize } from './parts'
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

function SpeechSettings() {
  const [cached, { refetch }] = createResource(() => request<{ repo: string; size: number }[]>('models.list').catch(() => []))
  onMount(probeGpu)
  const set = (k: 'whisperModel' | 'whisperLang', v: string) => {
    setPrefs(k, v)
    savePrefs()
  }
  return (
    <fieldset class="fieldset" data-testid="speech-settings">
      <legend>Transcription locale (Whisper)</legend>
      <p class="muted small">
        Dictée au micro et fichiers audio transcrits dans le navigateur : le son ne quitte pas cette machine. Le modèle est téléchargé une fois par le pod (dossier <code>models</code> des données) puis fonctionne hors ligne.
        {speech.device ? ` Moteur : ${speech.device === 'webgpu' ? 'WebGPU (carte graphique)' : 'WebAssembly (processeur)'}.` : ''}
      </p>
      <div class="field-row">
        <label class="field grow">
          <span>Modèle</span>
          <select value={prefs.whisperModel} onChange={(e) => set('whisperModel', e.currentTarget.value)} name="whisperModel">
            <For each={whisperModels}>
              {(m) => (
                <option value={m.id} disabled={m.webgpuOnly && !(speech.device === 'webgpu' && speech.f16)}>
                  {m.label} · {speech.device === 'webgpu' ? m.size.webgpu : m.size.wasm || m.size.webgpu}
                </option>
              )}
            </For>
          </select>
        </label>
        <label class="field">
          <span>Langue parlée</span>
          <select value={prefs.whisperLang} onChange={(e) => set('whisperLang', e.currentTarget.value)} name="whisperLang">
            <For each={languages}>{([id, label]) => <option value={id}>{label}</option>}</For>
          </select>
        </label>
      </div>
      <label class="check small">
        <input type="checkbox" checked={prefs.audioToModel} onChange={(e) => (setPrefs('audioToModel', e.currentTarget.checked), savePrefs())} />
        Envoyer les fichiers audio tels quels aux modèles qui écoutent l’audio (sinon : transcription locale)
      </label>
      <Show when={(cached() ?? []).length}>
        <div class="small">
          <For each={cached()}>
            {(c) => (
              <div class="ai-server-row">
                <span class="grow mono">{c.repo}</span>
                <span class="muted">{formatSize(c.size)}</span>
                <button
                  type="button"
                  class="btn small danger"
                  onClick={async () => {
                    await request('models.delete', { repo: c.repo }).catch(errorToast)
                    refetch()
                  }}
                >
                  Supprimer
                </button>
              </div>
            )}
          </For>
        </div>
      </Show>
    </fieldset>
  )
}

export function SettingsModal(props: { onClose: () => void }) {
  const blank = { id: '', name: '', kind: 'auto' as ServerView['kind'], url: '', apiKey: '', context: 0, hasKey: false, clearKey: false }
  const [form, setForm] = createSignal({ ...blank })
  const [busy, setBusy] = createSignal(false)
  const edit = (s: ServerView) => setForm({ ...blank, ...s, apiKey: '', context: s.context ?? 0 })
  const field = (k: keyof ReturnType<typeof form>) => (e: Event) => setForm({ ...form(), [k]: (e.currentTarget as HTMLInputElement).value })
  const save = async (e: Event) => {
    e.preventDefault()
    setBusy(true)
    try {
      const f = form()
      const view = await request('llm.server.save', { id: f.id, name: f.name.trim(), kind: f.kind, url: f.url, apiKey: f.apiKey, context: Number(f.context) || 0, clearKey: f.clearKey })
      applyConfig(view)
      const saved = f.id ? view.servers.find((s: ServerView) => s.id === f.id) : view.servers[view.servers.length - 1]
      setForm({ ...blank })
      if (saved && !config.server) await select(saved.id, '')
      else if (saved?.id === config.server) await loadModels()
      toast('Serveur enregistré', 'ok')
    } catch (err) {
      errorToast(err)
    } finally {
      setBusy(false)
    }
  }
  const remove = async (s: ServerView) => {
    if (!confirm(`Supprimer le serveur « ${s.name} » ?`)) return
    try {
      applyConfig(await request('llm.server.delete', { id: s.id }))
      if (config.server === s.id) await select('', '')
    } catch (err) {
      errorToast(err)
    }
  }
  type Tab = 'servers' | 'prompt' | 'compaction' | 'speech'
  const [tab, setTab] = createSignal<Tab>('servers')
  const tabs: [Tab, string][] = [
    ['servers', 'Serveurs'],
    ['prompt', 'Prompt et instructions'],
    ['compaction', 'Compaction'],
    ['speech', 'Transcription'],
  ]
  return (
    <Modal title="Réglages de l’assistant" onClose={props.onClose} class="ai-servers modal-wide">
      <div class="ai-tabs" role="tablist">
        <For each={tabs}>
          {([id, label]) => (
            <button type="button" role="tab" class="ai-tab" classList={{ active: tab() === id }} aria-selected={tab() === id} onClick={() => setTab(id)}>
              {label}
            </button>
          )}
        </For>
      </div>
      <Show when={tab() === 'prompt'}>
        <PromptSettings />
      </Show>
      <Show when={tab() === 'compaction'}>
        <CompactionSettings />
      </Show>
      <Show when={tab() === 'speech'}>
        <SpeechSettings />
      </Show>
      <div class="form" style={{ display: tab() === 'servers' ? undefined : 'none' }}>
        <For each={config.servers} fallback={<p class="muted">Aucun serveur. Ajoutez llama.cpp (llama-server) ou Ollama ci-dessous.</p>}>
          {(s) => (
            <div class="ai-server-row">
              <div class="grow">
                <strong>{s.name}</strong>
                <div class="muted small mono">
                  {s.url} · {s.kind === 'auto' ? 'détection auto' : s.kind === 'ollama' ? 'Ollama' : 'llama.cpp / OpenAI'}
                  {s.hasKey ? ' · clé API' : ''}
                </div>
              </div>
              <button class="btn small" onClick={() => edit(s)}>
                Modifier
              </button>
              <button class="btn small danger" onClick={() => remove(s)}>
                Supprimer
              </button>
            </div>
          )}
        </For>
        <form class="fieldset" onSubmit={save}>
          <legend>{form().id ? 'Modifier le serveur' : 'Ajouter un serveur'}</legend>
          <div class="field-row">
            <label class="field grow">
              <span>Adresse (IP:port ou URL)</span>
              <input value={form().url} onInput={field('url')} placeholder="127.0.0.1:8080" required name="url" />
            </label>
            <label class="field">
              <span>Type</span>
              <select value={form().kind} onChange={field('kind')} name="kind">
                <option value="auto">Détection auto</option>
                <option value="llamacpp">llama.cpp / OpenAI</option>
                <option value="ollama">Ollama</option>
              </select>
            </label>
          </div>
          <div class="field-row">
            <label class="field grow">
              <span>Nom (facultatif)</span>
              <input value={form().name} onInput={field('name')} name="name" />
            </label>
            <label class="field grow">
              <span>Clé API (facultative)</span>
              <input type="password" value={form().apiKey} onInput={field('apiKey')} placeholder={form().hasKey ? 'inchangée' : ''} autocomplete="off" name="apiKey" />
            </label>
          </div>
          <Show when={form().kind !== 'llamacpp'}>
            <label class="field">
              <span>Contexte demandé à Ollama (num_ctx, 0 = défaut du modèle)</span>
              <input type="number" min="0" step="1024" value={form().context} onInput={field('context')} class="w-next" name="context" />
            </label>
          </Show>
          <Show when={form().hasKey}>
            <label class="check small">
              <input type="checkbox" checked={form().clearKey} onChange={(e) => setForm({ ...form(), clearKey: e.currentTarget.checked })} />
              Supprimer la clé enregistrée
            </label>
          </Show>
          <div class="form-actions">
            <Show when={form().id}>
              <button type="button" class="btn" onClick={() => setForm({ ...blank })}>
                Annuler
              </button>
            </Show>
            <button class="btn primary" disabled={busy()}>
              {form().id ? 'Enregistrer' : 'Ajouter'}
            </button>
          </div>
        </form>
      </div>
    </Modal>
  )
}
