// Settings of the assistant besides the servers: system prompt and instructions, and
// compaction of long conversations.
import { createResource, createSignal, For, onMount, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { openTextTab, relPath } from '../state/project'
import { Modal } from '../ui/overlay'
import { Icon } from '../ui/icons'
import { errorToast, toast } from '../ui/toast'
import { applyConfig, config, loadModels, prefs, savePrefs, select, setPrefs, type Mode, type Model, type ModelConf, type ServerView } from './state'
import { languages, probeGpu, speech, whisperModels } from './transcribe'
import { formatSize } from './parts'
import { defaultTemplate, loadPromptContext, promptContext, storedTemplates, systemPrompt, templateOf } from './prompt'
import { fmtSize, t } from '../i18n'

const size = (n: number) => fmtSize(n)

export function PromptSettings() {
  const [scope, setScope] = createSignal<'project' | 'global'>('project')
  const [kind, setKind] = createSignal<Mode>('build')
  const stored = (c: ReturnType<typeof promptContext>, sc: 'project' | 'global', k: Mode) => storedTemplates(c, k)[sc] ?? ''
  const [text, setText] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  // Text typed before the context arrives is not replaced by it.
  let edited = false
  const load = async () => {
    const c = await loadPromptContext()
    if (!edited) setText(stored(c, scope(), kind()))
  }
  const edit = (v: string) => {
    edited = true
    setText(v)
  }
  onMount(load)
  const switchScope = (s: 'project' | 'global') => {
    edited = false
    setScope(s)
    setText(stored(promptContext(), s, kind()))
  }
  const switchKind = (k: Mode) => {
    edited = false
    setKind(k)
    setText(stored(promptContext(), scope(), k))
  }
  const defaultText = () => defaultTemplate(kind())
  const save = async (content: string) => {
    setBusy(true)
    try {
      await request('llm.prompt.save', { scope: scope(), kind: kind(), content })
      edited = false
      await load()
      toast(content.trim() ? t('Prompt saved') : t('Default prompt restored'), 'ok')
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
    }
  }
  const source = () => templateOf(promptContext(), kind()).source
  return (
    <div class="form" data-testid="prompt-settings">
      <p class="muted small">
        {t('The system prompt starts with this template, followed by the instruction files and the list of skills, loaded like Claude Code. The project prompt ({build}, {plan} for the Plan mode, {briefing} for the Briefing mode) replaces the global one. Currently:', { build: '.ide/system-prompt.md', plan: '.ide/plan-prompt.md', briefing: '.ide/briefing-prompt.md' })}{' '}
        <strong>{source() === 'project' ? t('project prompt') : source() === 'global' ? t('global prompt') : t('default prompt')}</strong>.
      </p>
      <div class="field-row">
        <button type="button" class="toggle" classList={{ on: kind() === 'build' }} onClick={() => switchKind('build')} data-testid="prompt-build">
          build
        </button>
        <button type="button" class="toggle" classList={{ on: kind() === 'plan' }} onClick={() => switchKind('plan')} data-testid="prompt-plan">
          plan
        </button>
        <button type="button" class="toggle" classList={{ on: kind() === 'briefing' }} onClick={() => switchKind('briefing')} data-testid="prompt-briefing">
          briefing
        </button>
        <span class="sep" />
        <button type="button" class="toggle" classList={{ on: scope() === 'project' }} onClick={() => switchScope('project')}>
          {t('project')}
        </button>
        <button type="button" class="toggle" classList={{ on: scope() === 'global' }} onClick={() => switchScope('global')}>
          {t('global')}
        </button>
        <span class="grow" />
        <button type="button" class="btn small" onClick={() => edit(defaultText())}>
          {t('Start from the default prompt')}
        </button>
      </div>
      <textarea class="ai-prompt-edit mono" rows="12" value={text()} onInput={(e) => edit(e.currentTarget.value)} placeholder={`${scope() === 'project' ? t('Empty: global prompt, else default prompt.') : t('Empty: default prompt.')}\n\n${defaultText()}`} name="systemPrompt" />
      <p class="muted small">
        {t('Variables:')} <code>{'{{project}}'}</code> <code>{'{{root}}'}</code> <code>{'{{host}}'}</code> <code>{'{{activeFile}}'}</code> <code>{'{{date}}'}</code> <code>{'{{tools}}'}</code> {t('(description of the tools, empty when they are off).')}
      </p>
      <div class="form-actions">
        <button type="button" class="btn" onClick={() =>
            systemPrompt(kind())
              .then((text) => openTextTab(kind() === 'plan' ? t('System prompt (Plan)') : kind() === 'briefing' ? t('System prompt (Briefing)') : t('System prompt'), text, 'markdown'))
              .catch(errorToast)
          }>
          {t('Preview of the full prompt')}
        </button>
        <span class="grow" />
        <button type="button" class="btn" disabled={busy()} onClick={() => save('')}>
          {t('Restore')}
        </button>
        <button type="button" class="btn primary" disabled={busy()} onClick={() => save(text())}>
          {t('Save')}
        </button>
      </div>
      <fieldset class="fieldset">
        <legend>{t('Instruction files')}</legend>
        <p class="muted small">
          {t('Global:')} <code>~/.claude/CLAUDE.md</code>, <code>~/.codex/AGENTS.md</code>. {t('Project:')} <code>CLAUDE.md</code>, <code>.claude/CLAUDE.md</code>, <code>CLAUDE.local.md</code>, <code>AGENTS.md</code>. {t('Imports with {at} are followed.', { at: '@path' })}
        </p>
        <For each={promptContext()?.files ?? []} fallback={<p class="muted small">{t('No file found.')}</p>}>
          {(f) => (
            <div class="ai-server-row small" data-testid="instruction-file">
              <span class="badge">{f.scope === 'global' ? t('global') : t('project')}</span>
              <span class="grow mono ellipsis">{f.scope === 'project' ? relPath(f.path) : f.path}</span>
              <span class="muted">{size(f.content.length)}</span>
            </div>
          )}
        </For>
      </fieldset>
      <fieldset class="fieldset">
        <legend>Skills</legend>
        <p class="muted small">
          <code>~/.claude/skills</code>, <code>~/.agents/skills</code> {t('and the same folders in the project (first). Only the name and the description go in the prompt; the assistant loads the rest with the {tool} tool.', { tool: 'load_skill' })}
        </p>
        <For each={promptContext()?.skills ?? []} fallback={<p class="muted small">{t('No skill found.')}</p>}>
          {(s) => (
            <div class="ai-server-row small" data-testid="skill">
              <span class="badge">{s.scope === 'global' ? t('global') : t('project')}</span>
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

export function PlanSettings() {
  const [list] = createResource(
    () => prefs.planServer,
    (server) => (server ? request<{ models: Model[] }>('llm.models', { server }).then((r) => r.models, () => []) : Promise.resolve([] as Model[])),
  )
  const set = (k: 'planServer' | 'planModel', v: string) => {
    setPrefs(k, v)
    savePrefs()
  }
  return (
    <div class="form" data-testid="plan-settings">
      <p class="muted small">
        {t('In Plan mode (Shift+Tab in the message box), the assistant explores and proposes a plan without changing files: edit_file and write_file are removed, and a bash command that does not look like a reading asks for your approval. Its prompt is edited in the “Prompt and instructions” tab (plan button). When the plan is ready, “Execute this plan” switches to Build with the model of the conversation.')}
      </p>
      <div class="field-row">
        <label class="field grow">
          <span>{t('Model of the Plan mode')}</span>
          <select value={prefs.planServer} onChange={(e) => (set('planServer', e.currentTarget.value), set('planModel', ''))} name="planServer">
            <option value="">{t('The model of the conversation')}</option>
            <For each={config.servers}>{(s) => <option value={s.id}>{s.name}</option>}</For>
          </select>
        </label>
        <Show when={prefs.planServer}>
          <label class="field grow">
            <span>{t('Model')}</span>
            <select value={prefs.planModel} onChange={(e) => set('planModel', e.currentTarget.value)} name="planModel">
              <option value="">{t('— choose —')}</option>
              <For each={list() ?? []}>{(m) => <option value={m.id}>{m.id}</option>}</For>
            </select>
          </label>
        </Show>
      </div>
    </div>
  )
}

export function SubAgentSettings() {
  const [list] = createResource(
    () => config.childServer,
    (server) => (server ? request<{ models: Model[] }>('llm.models', { server }).then((r) => r.models, () => []) : Promise.resolve([] as Model[])),
  )
  const set = (server: string, model: string) => request('llm.children', { server, model }).then(applyConfig, errorToast)
  return (
    <div class="form" data-testid="subagent-settings">
      <p class="muted small">
        {t('The assistant can delegate a task to a sub-agent: a conversation with a fresh context that runs in the background and reports to it. It may run on another server, a cloud provider for instance: mark the servers “Available to sub-agents” in the Servers tab, with a note that tells the assistant when to use them.')}
      </p>
      <div class="field-row">
        <label class="field grow">
          <span>{t('Default server of the sub-agents')}</span>
          <select value={config.childServer ?? ''} onChange={(e) => set(e.currentTarget.value, '')} name="childServer">
            <option value="">{t('The model of the conversation')}</option>
            <For each={config.servers.filter((s) => s.children)}>{(s) => <option value={s.id}>{s.name}</option>}</For>
          </select>
        </label>
        <Show when={config.childServer}>
          <label class="field grow">
            <span>{t('Model')}</span>
            <select value={config.childModel ?? ''} onChange={(e) => set(config.childServer!, e.currentTarget.value)} name="childModel">
              <option value="">{t('— choose —')}</option>
              <For each={list() ?? []}>{(m) => <option value={m.id}>{m.id}</option>}</For>
            </select>
          </label>
        </Show>
      </div>
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
        {t('When a conversation nears the context size of the model, the oldest messages are replaced by a summary (they stay visible, folded). The recent messages, about a quarter of the context, are kept as they are. The “compact” button under the message box does it on demand.')}
      </p>
      <label class="check">
        <input type="checkbox" checked={prefs.autoCompact} onChange={(e) => set('autoCompact', e.currentTarget.checked)} />
        {t('Automatic compaction')}
      </label>
      <label class="field">
        <span>{t('Threshold: {n} % of the context', { n: prefs.compactAt })}</span>
        <input type="range" min="40" max="95" step="5" value={prefs.compactAt} onInput={(e) => set('compactAt', Number(e.currentTarget.value))} />
      </label>
      <div class="field-row">
        <label class="field grow">
          <span>{t('Summarizing model')}</span>
          <select value={prefs.compactServer} onChange={(e) => (set('compactServer', e.currentTarget.value), set('compactModel', ''))} name="compactServer">
            <option value="">{t('The model of the conversation')}</option>
            <For each={config.servers}>{(s) => <option value={s.id}>{s.name}</option>}</For>
          </select>
        </label>
        <Show when={prefs.compactServer}>
          <label class="field grow">
            <span>{t('Model')}</span>
            <select value={prefs.compactModel} onChange={(e) => set('compactModel', e.currentTarget.value)} name="compactModel">
              <option value="">{t('— choose —')}</option>
              <For each={list() ?? []}>{(m) => <option value={m.id}>{m.id}</option>}</For>
            </select>
          </label>
        </Show>
      </div>
      <Show when={prefs.compactServer && !prefs.compactModel}>
        <p class="warn small">{t('Choose the summarizing model, otherwise the compaction will fail.')}</p>
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
      <legend>{t('Local transcription (Whisper)')}</legend>
      <p class="muted small">
        {t('Microphone dictation and audio files transcribed in the browser: the sound does not leave this machine. The model is downloaded once by the pod (folder {dir} of the data), then works offline.', { dir: 'models' })}
        {speech.device ? ` ${t('Engine: {engine}.', { engine: speech.device === 'webgpu' ? t('WebGPU (graphics card)') : t('WebAssembly (processor)') })}` : ''}
      </p>
      <div class="field-row">
        <label class="field grow">
          <span>{t('Model')}</span>
          <select value={prefs.whisperModel} onChange={(e) => set('whisperModel', e.currentTarget.value)} name="whisperModel">
            <For each={whisperModels}>
              {(m) => (
                <option value={m.id} disabled={m.webgpuOnly && !(speech.device === 'webgpu' && speech.f16)}>
                  {t(m.label)} · {t('{n} MB', { n: speech.device === 'webgpu' ? m.size.webgpu : m.size.wasm || m.size.webgpu })}
                </option>
              )}
            </For>
          </select>
        </label>
        <label class="field">
          <span>{t('Spoken language')}</span>
          <select value={prefs.whisperLang} onChange={(e) => set('whisperLang', e.currentTarget.value)} name="whisperLang">
            <For each={languages}>{([id, label]) => <option value={id}>{t(label)}</option>}</For>
          </select>
        </label>
      </div>
      <label class="check small">
        <input type="checkbox" checked={prefs.audioToModel} onChange={(e) => (setPrefs('audioToModel', e.currentTarget.checked), savePrefs())} />
        {t('Send audio files as they are to the models that listen to audio (otherwise: local transcription)')}
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
                  {t('Delete')}
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
  const blank = { id: '', name: '', kind: 'auto' as ServerView['kind'], url: '', apiKey: '', context: 0, parallel: 1, hasKey: false, clearKey: false, note: '', children: false, models: [] as ModelConf[] }
  const [form, setForm] = createSignal({ ...blank })
  const [busy, setBusy] = createSignal(false)
  const edit = (s: ServerView) => setForm({ ...blank, ...s, apiKey: '', context: s.context ?? 0, parallel: s.parallel || 1, note: s.note ?? '', children: !!s.children, models: (s.models ?? []).map((m) => ({ ...m })) })
  const field = (k: keyof ReturnType<typeof form>) => (e: Event) => setForm({ ...form(), [k]: (e.currentTarget as HTMLInputElement).value })
  const save = async (e: Event) => {
    e.preventDefault()
    setBusy(true)
    try {
      const f = form()
      const models = f.models.filter((m) => m.id.trim()).map((m) => ({ ...m, id: m.id.trim(), context: Number(m.context) || 0 }))
      const view = await request('llm.server.save', {
        id: f.id,
        name: f.name.trim(),
        kind: f.kind,
        url: f.url,
        apiKey: f.apiKey,
        context: Number(f.context) || 0,
        parallel: Math.max(1, Number(f.parallel) || 1),
        clearKey: f.clearKey,
        note: f.note.trim(),
        children: f.children,
        models,
      })
      applyConfig(view)
      const saved = f.id ? view.servers.find((s: ServerView) => s.id === f.id) : view.servers[view.servers.length - 1]
      setForm({ ...blank })
      if (saved && !config.server) await select(saved.id, '')
      else if (saved?.id === config.server) await loadModels()
      toast(t('Server saved'), 'ok')
    } catch (err) {
      errorToast(err)
    } finally {
      setBusy(false)
    }
  }
  const remove = async (s: ServerView) => {
    if (!confirm(t('Delete the server “{name}”?', { name: s.name }))) return
    try {
      applyConfig(await request('llm.server.delete', { id: s.id }))
      if (config.server === s.id) await select('', '')
    } catch (err) {
      errorToast(err)
    }
  }
  const setModel = (i: number, patch: Partial<ModelConf>) => setForm({ ...form(), models: form().models.map((m, j) => (j === i ? { ...m, ...patch } : m)) })
  type Tab = 'servers' | 'prompt' | 'plan' | 'subagents' | 'compaction' | 'speech'
  const [tab, setTab] = createSignal<Tab>('servers')
  const tabs: [Tab, string][] = [
    ['servers', 'Servers'],
    ['prompt', 'Prompt and instructions'],
    ['plan', 'Plan mode'],
    ['subagents', 'Sub-agents'],
    ['compaction', 'Compaction'],
    ['speech', 'Transcription'],
  ]
  return (
    <Modal title={t('Assistant settings')} onClose={props.onClose} class="ai-servers modal-wide">
      <div class="ai-tabs" role="tablist">
        <For each={tabs}>
          {([id, label]) => (
            <button type="button" role="tab" class="ai-tab" classList={{ active: tab() === id }} aria-selected={tab() === id} onClick={() => setTab(id)}>
              {t(label)}
            </button>
          )}
        </For>
      </div>
      <Show when={tab() === 'prompt'}>
        <PromptSettings />
      </Show>
      <Show when={tab() === 'plan'}>
        <PlanSettings />
      </Show>
      <Show when={tab() === 'subagents'}>
        <SubAgentSettings />
      </Show>
      <Show when={tab() === 'compaction'}>
        <CompactionSettings />
      </Show>
      <Show when={tab() === 'speech'}>
        <SpeechSettings />
      </Show>
      <div class="form" style={{ display: tab() === 'servers' ? undefined : 'none' }}>
        <For each={config.servers} fallback={<p class="muted">{t('No server. Add llama.cpp (llama-server) or Ollama below.')}</p>}>
          {(s) => (
            <div class="ai-server-row">
              <div class="grow">
                <strong>{s.name}</strong>
                <div class="muted small mono">
                  {s.url} · {s.kind === 'auto' ? t('auto-detect') : s.kind === 'ollama' ? 'Ollama' : s.kind === 'openai' ? t('OpenAI-compatible provider') : 'llama.cpp / OpenAI'}
                  {s.hasKey ? ` · ${t('API key')}` : ''}
                  {s.children ? ` · ${t('sub-agents')}` : ''}
                </div>
              </div>
              <button class="btn small" onClick={() => edit(s)}>
                {t('Edit')}
              </button>
              <button class="btn small danger" onClick={() => remove(s)}>
                {t('Delete')}
              </button>
            </div>
          )}
        </For>
        <form class="fieldset" onSubmit={save}>
          <legend>{form().id ? t('Edit the server') : t('Add a server')}</legend>
          <div class="field-row">
            <label class="field grow">
              <span>{t('Address (IP:port or URL)')}</span>
              <input value={form().url} onInput={field('url')} placeholder="127.0.0.1:8080" required name="url" />
            </label>
            <label class="field">
              <span>{t('Type')}</span>
              <select value={form().kind} onChange={field('kind')} name="kind">
                <option value="auto">{t('Auto-detect')}</option>
                <option value="llamacpp">llama.cpp / OpenAI</option>
                <option value="ollama">Ollama</option>
                <option value="openai">{t('OpenAI-compatible provider (OpenAI, OpenRouter…)')}</option>
              </select>
            </label>
          </div>
          <div class="field-row">
            <label class="field grow">
              <span>{t('Name (optional)')}</span>
              <input value={form().name} onInput={field('name')} name="name" />
            </label>
            <label class="field grow">
              <span>{t('API key (optional)')}</span>
              <input type="password" value={form().apiKey} onInput={field('apiKey')} placeholder={form().hasKey ? t('unchanged') : ''} autocomplete="off" name="apiKey" />
            </label>
          </div>
          <Show when={form().kind === 'openai'}>
            <p class="muted small">{t('The key stays on the pod: the page never receives it. Requests to a paid provider cost money.')}</p>
          </Show>
          <div class="field">
            <span>{t('Models typed by hand (when the server does not list them, or to set what they can do)')}</span>
            <For each={form().models}>
              {(m, i) => (
                <div class="ai-model-conf" data-testid="model-conf">
                  <input value={m.id} placeholder={t('model id')} onInput={(e) => setModel(i(), { id: e.currentTarget.value })} name="modelId" />
                  <input type="number" min="0" step="1024" value={m.context ?? 0} title={t('Context size (0: unknown)')} onInput={(e) => setModel(i(), { context: Number(e.currentTarget.value) })} class="w-next" />
                  <label class="check small">
                    <input type="checkbox" checked={m.tools} onChange={(e) => setModel(i(), { tools: e.currentTarget.checked })} />
                    {t('tools')}
                  </label>
                  <label class="check small">
                    <input type="checkbox" checked={m.vision} onChange={(e) => setModel(i(), { vision: e.currentTarget.checked })} />
                    {t('image')}
                  </label>
                  <label class="check small">
                    <input type="checkbox" checked={m.thinking} onChange={(e) => setModel(i(), { thinking: e.currentTarget.checked })} />
                    {t('reasoning')}
                  </label>
                  <button type="button" class="icon-btn small" title={t('Remove')} onClick={() => setForm({ ...form(), models: form().models.filter((_, j) => j !== i()) })}>
                    <Icon name="close" size={12} />
                  </button>
                </div>
              )}
            </For>
            <button type="button" class="btn small" onClick={() => setForm({ ...form(), models: [...form().models, { id: '', context: 0, tools: true, vision: false, thinking: false }] })} data-testid="model-conf-add">
              <Icon name="plus" size={12} /> {t('Type a model')}
            </button>
          </div>
          <label class="check">
            <input type="checkbox" checked={form().children} onChange={(e) => setForm({ ...form(), children: e.currentTarget.checked })} name="children" />
            {t('Available to sub-agents')}
          </label>
          <Show when={form().children}>
            <label class="field">
              <span>{t('Note for the assistant choosing it (strengths, cost…)')}</span>
              <input value={form().note} onInput={field('note')} placeholder={t('e.g. strong at code, paid')} name="note" />
            </label>
          </Show>
          <Show when={form().kind !== 'llamacpp' && form().kind !== 'openai'}>
            <label class="field">
              <span>{t('Context asked to Ollama (num_ctx, 0 = model default)')}</span>
              <input type="number" min="0" step="1024" value={form().context} onInput={field('context')} class="w-next" name="context" />
            </label>
          </Show>
          <label class="field">
            <span>{t('Conversations at once (the others wait their turn)')}</span>
            <input type="number" min="1" max="16" step="1" value={form().parallel} onInput={field('parallel')} class="w-next" name="parallel" />
          </label>
          <Show when={form().hasKey}>
            <label class="check small">
              <input type="checkbox" checked={form().clearKey} onChange={(e) => setForm({ ...form(), clearKey: e.currentTarget.checked })} />
              {t('Delete the saved key')}
            </label>
          </Show>
          <div class="form-actions">
            <Show when={form().id}>
              <button type="button" class="btn" onClick={() => setForm({ ...blank })}>
                {t('Cancel')}
              </button>
            </Show>
            <button class="btn primary" disabled={busy()}>
              {form().id ? t('Save') : t('Add')}
            </button>
          </div>
        </form>
      </div>
    </Modal>
  )
}
