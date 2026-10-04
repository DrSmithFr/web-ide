// Settings pop-in: navigation on the left, content on the right.
import { createMemo, createResource, createSignal, For, Index, onCleanup, onMount, Show } from 'solid-js'
import { unwrap } from 'solid-js/store'
import { Modal } from '../ui/overlay'
import { settings, updateSettings, replaceSettings, type Settings } from '../state/settings'
import { accents, accentOf, themes, themeById, tokenLabels, tokenTypes } from './themes'
import {
  actions, actionsFor, activeLayout, captureKeys, comboFromEvent, comboLabel, detectedLayout, effectiveBindings, presets, suggestedLayout, actionById,
} from '../keys/bindings'
import { builtinGrammar, grammarDefs } from '../editor/languages'
import { Grammar, type GrammarDef, type RuleDef } from '../editor/tokenizer'
import { request } from '../pod/rpc'
import { errorToast, toast } from '../ui/toast'
import { Doc } from '../editor/doc'
import { EditorView } from '../editor/view'
import { samples } from './samples'
import { browserLang, fmtDate, languages, t, tn, type LangSetting } from '../i18n'

const sections = [
  ['language', 'Language'],
  ['themes', 'Themes'],
  ['fonts', 'Fonts'],
  ['editor', 'Editor'],
  ['keys', 'Keyboard shortcuts'],
  ['syntax', 'Syntax highlighting'],
  ['workspace', 'Workspace and pod'],
  ['history', 'Settings history'],
] as const

const [open, setOpen] = createSignal<string | null>(null)
export function openSettings(section = 'themes') {
  setOpen(section)
}

export function SettingsHost() {
  return (
    <Show when={open()}>
      <Modal title={t('Settings')} onClose={() => setOpen(null)} class="modal-settings">
        <div class="settings">
          <nav class="settings-nav">
            <For each={sections}>
              {([id, label]) => (
                <button classList={{ active: open() === id }} onClick={() => setOpen(id)}>
                  {t(label)}
                </button>
              )}
            </For>
          </nav>
          <div class="settings-body">
            <Show when={open() === 'language'}>
              <LanguageSettings />
            </Show>
            <Show when={open() === 'themes'}>
              <Themes />
            </Show>
            <Show when={open() === 'fonts'}>
              <Fonts />
            </Show>
            <Show when={open() === 'editor'}>
              <EditorSettings />
            </Show>
            <Show when={open() === 'keys'}>
              <Keys />
            </Show>
            <Show when={open() === 'syntax'}>
              <Syntax />
            </Show>
            <Show when={open() === 'workspace'}>
              <Workspace />
            </Show>
            <Show when={open() === 'history'}>
              <History />
            </Show>
          </div>
        </div>
      </Modal>
    </Show>
  )
}

/** Read-only highlighted sample, to see a theme or grammar change at once. */
function Preview(props: { lang: string }) {
  let host!: HTMLDivElement
  onMount(() => {
    const doc = new Doc('preview', samples[props.lang] ?? samples.php, { readOnly: true, lang: props.lang })
    const v = new EditorView(doc, { tabSize: settings.editor.tabSize, insertSpaces: true, highlightLine: false, readOnly: true })
    v.mount(host)
    onCleanup(() => v.destroy())
  })
  return <div class="preview editor-host" ref={host} />
}

function Themes() {
  const theme = () => themeById(settings.theme)
  const color = (tok: string) => settings.tokenColors[settings.theme]?.[tok] ?? theme().tokens[tok]
  return (
    <section>
      <h3>{t('Theme')}</h3>
      <div class="theme-grid">
        <For each={themes}>
          {(th) => (
            <button class="theme-card" classList={{ active: settings.theme === th.id }} onClick={() => updateSettings((s) => (s.theme = th.id), `Theme ${th.name}`)}>
              <span class="theme-swatch" style={{ background: th.ui.bg, color: th.ui.fg, 'border-color': th.ui.line }}>
                <span style={{ color: th.tokens.keyword }}>fn</span> <span style={{ color: th.tokens.function }}>main</span>
                <span style={{ color: th.tokens.punctuation }}>()</span> <span style={{ color: th.tokens.string }}>"ok"</span>
              </span>
              <span>{t(th.name)}</span>
            </button>
          )}
        </For>
      </div>
      <h3>{t('Accent color')}</h3>
      <p class="muted small">{t('Marks the focused part of the window: its active tab and its tool icon.')}</p>
      <div class="accent-grid">
        <For each={[{ id: '', name: 'Theme' }, ...accents]}>
          {(a) => (
            <button class="accent-card" classList={{ active: settings.accent === a.id }} data-accent={a.id} onClick={() => updateSettings((s) => (s.accent = a.id), 'Accent color')}>
              <span class="accent-dot" style={{ background: accentOf(theme(), a.id)[0] }} />
              <span>{t(a.name)}</span>
            </button>
          )}
        </For>
      </div>
      <div class="checks">
        <label class="check">
          <input type="checkbox" checked={settings.visualFocus} onChange={(e) => updateSettings((s) => (s.visualFocus = e.currentTarget.checked), 'Visual focus')} />
          {t('Visual focus mode: everything but the focused part in monochrome')}
        </label>
        <label class="check">
          <input type="checkbox" checked={settings.focusOutline} onChange={(e) => updateSettings((s) => (s.focusOutline = e.currentTarget.checked), 'Focus outline')} />
          {t('Outline around the focused part')}
        </label>
      </div>
      <h3>{t('Token colors · {theme}', { theme: t(theme().name) })}</h3>
      <p class="muted small">{t('No bold: the highlighting API only handles the color, the background, the underline and italics.')}</p>
      <div class="token-grid">
        <For each={tokenTypes}>
          {(tok) => (
            <label class="token-color">
              <input
                type="color"
                value={color(tok)}
                onInput={(e) => {
                  const v = e.currentTarget.value
                  updateSettings((s) => {
                    s.tokenColors[s.theme] = { ...(s.tokenColors[s.theme] ?? {}), [tok]: v }
                  }, 'Token color')
                }}
              />
              <span style={{ color: color(tok) }}>{t(tokenLabels[tok])}</span>
            </label>
          )}
        </For>
      </div>
      <button class="btn small" disabled={!settings.tokenColors[settings.theme]} onClick={() => updateSettings((s) => delete s.tokenColors[s.theme], 'Default colors')}>
        {t('Default colors of the theme')}
      </button>
      <Preview lang="php" />
    </section>
  )
}

function Fonts() {
  const num = (path: 'size' | 'lineHeight' | 'uiSize', v: string) => {
    const n = parseFloat(v)
    if (n > 0) updateSettings((s) => ((s.font as any)[path] = n), 'Font')
  }
  return (
    <section class="form">
      <label class="field">
        <span>{t('Code font (CSS stack)')}</span>
        <input value={settings.font.family} onChange={(e) => updateSettings((s) => (s.font.family = e.currentTarget.value), 'Font')} />
      </label>
      <div class="field-row">
        <label class="field">
          <span>{t('Code size (px)')}</span>
          <input type="number" min="8" max="32" value={settings.font.size} onChange={(e) => num('size', e.currentTarget.value)} />
        </label>
        <label class="field">
          <span>{t('Line height')}</span>
          <input type="number" min="1" max="2.5" step="0.05" value={settings.font.lineHeight} onChange={(e) => num('lineHeight', e.currentTarget.value)} />
        </label>
        <label class="field">
          <span>{t('Interface size (px)')}</span>
          <input type="number" min="10" max="20" value={settings.font.uiSize} onChange={(e) => num('uiSize', e.currentTarget.value)} />
        </label>
      </div>
      <label class="check">
        <input type="checkbox" checked={settings.font.ligatures} onChange={(e) => updateSettings((s) => (s.font.ligatures = e.currentTarget.checked), 'Ligatures')} />
        {t('Ligatures')}
      </label>
      <Preview lang="typescript" />
    </section>
  )
}

function EditorSettings() {
  return (
    <section class="form">
      <div class="field-row">
        <label class="field">
          <span>{t('Tab size')}</span>
          <input type="number" min="1" max="8" value={settings.editor.tabSize} onChange={(e) => updateSettings((s) => (s.editor.tabSize = parseInt(e.currentTarget.value, 10) || 4), 'Tab size')} />
        </label>
      </div>
      <label class="check">
        <input type="checkbox" checked={settings.editor.insertSpaces} onChange={(e) => updateSettings((s) => (s.editor.insertSpaces = e.currentTarget.checked), 'Indentation')} />
        {t('Indent with spaces')}
      </label>
      <label class="check">
        <input type="checkbox" checked={settings.editor.highlightLine} onChange={(e) => updateSettings((s) => (s.editor.highlightLine = e.currentTarget.checked), 'Current line')} />
        {t('Highlight the current line')}
      </label>
      <label class="check">
        <input type="checkbox" checked={settings.editor.indentGuides} onChange={(e) => updateSettings((s) => (s.editor.indentGuides = e.currentTarget.checked), 'Indentation guides')} />
        {t('Show the indentation guides')}
      </label>
    </section>
  )
}

function Keys() {
  const [filter, setFilter] = createSignal('')
  const [capturing, setCapturing] = createSignal<string | null>(null)
  onCleanup(() => captureKeys(null))

  const startCapture = (action: string) => {
    setCapturing(action)
    captureKeys((e) => {
      if (e.key === 'Escape') {
        stop()
        return
      }
      const combo = comboFromEvent(e)
      if (!combo) return
      stop()
      assign(action, combo)
    })
  }
  const stop = () => {
    setCapturing(null)
    captureKeys(null)
  }
  const assign = (action: string, combo: string) => {
    const others = actionsFor(combo).filter((a) => a !== action)
    if (others.length) {
      const names = others.map((a) => t(actionById.get(a)?.label ?? a)).join(', ')
      if (!confirm(t('{combo} is already used by: {actions}.\nRemove it from these actions and assign it here?', { combo: comboLabel(combo), actions: names }))) return
    }
    const eff = structuredClone(effectiveBindings())
    updateSettings((s) => {
      for (const a of others) s.keyboard.overrides[a] = eff[a].filter((c) => c !== combo)
      const cur = s.keyboard.overrides[action] ?? eff[action] ?? []
      if (!cur.includes(combo)) s.keyboard.overrides[action] = [...cur, combo]
    }, 'Shortcut')
  }
  const removeCombo = (action: string, combo: string) => {
    const cur = effectiveBindings()[action] ?? []
    updateSettings((s) => (s.keyboard.overrides[action] = cur.filter((c) => c !== combo)), 'Shortcut')
  }
  const reset = (action: string) => updateSettings((s) => delete s.keyboard.overrides[action], 'Shortcut reset')

  const list = createMemo(() => {
    const f = filter().toLowerCase()
    return actions.filter((a) => !f || t(a.label).toLowerCase().includes(f) || t(a.category).toLowerCase().includes(f) || (effectiveBindings()[a.id] ?? []).some((c) => comboLabel(c).toLowerCase().includes(f)))
  })

  return (
    <section>
      <div class="field-row">
        <label class="field">
          <span>{t('Keyboard layout')}</span>
          <select value={settings.keyboard.layout} onChange={(e) => updateSettings((s) => (s.keyboard.layout = e.currentTarget.value as any), 'Keyboard layout')}>
            <option value="auto">{detectedLayout() ? t('Automatic (detected: {layout})', { layout: presets[detectedLayout()!].name }) : t('Automatic (suggested: {layout})', { layout: presets[suggestedLayout()].name })}</option>
            <option value="qwerty">QWERTY</option>
            <option value="azerty">AZERTY</option>
          </select>
        </label>
        <label class="field grow">
          <span>{t('Filter')}</span>
          <input placeholder={t('action, category or key')} value={filter()} onInput={(e) => setFilter(e.currentTarget.value)} />
        </label>
      </div>
      <p class="muted small">
        {t('Active preset: {layout}. Shortcuts are bound to the physical position of the keys; your changes are saved as overrides of the preset.', { layout: presets[activeLayout()].name })}
        {detectedLayout() ? '' : ` ${t('Automatic detection is not available in this browser: the language is used as a suggestion.')}`}
      </p>
      <table class="keys-table">
        <tbody>
          <For each={list()}>
            {(a) => (
              <tr classList={{ overridden: !!settings.keyboard.overrides[a.id] }}>
                <td>
                  <div>{t(a.label)}</div>
                  <div class="muted small">{t(a.category)}</div>
                </td>
                <td class="keys-cell">
                  <For each={effectiveBindings()[a.id] ?? []}>
                    {(c) => (
                      <span class="chip">
                        <kbd>{comboLabel(c)}</kbd>
                        <button class="chip-x" title={t('Remove')} onClick={() => removeCombo(a.id, c)}>
                          ✕
                        </button>
                      </span>
                    )}
                  </For>
                  <Show when={capturing() === a.id} fallback={<button class="btn small" onClick={() => startCapture(a.id)}>+</button>}>
                    <span class="capture">{t('Press the combination… (Esc to cancel)')}</span>
                  </Show>
                </td>
                <td>
                  <Show when={settings.keyboard.overrides[a.id]}>
                    <button class="btn small" onClick={() => reset(a.id)} title={t('Back to the preset')}>
                      {t('Reset')}
                    </button>
                  </Show>
                </td>
              </tr>
            )}
          </For>
        </tbody>
      </table>
    </section>
  )
}

function download(name: string, data: unknown) {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(a.href), 1000)
}

function Syntax() {
  const [lang, setLang] = createSignal('php')
  const def = () => grammarDefs().find((g) => g.id === lang())!
  const errors = createMemo(() => new Grammar(structuredClone(def())).errors)
  const edit = (f: (g: GrammarDef) => void) =>
    updateSettings((s) => {
      const g: GrammarDef = structuredClone(unwrap(s.syntax[lang()]) ?? builtinGrammar(lang()) ?? def())
      f(g)
      s.syntax[lang()] = g
    }, `Colorisation ${def().name}`)

  const setRule = (state: string, i: number, patch: Partial<RuleDef>) => edit((g) => Object.assign(g.states[state][i], patch))
  const move = (state: string, i: number, d: number) =>
    edit((g) => {
      const l = g.states[state]
      const j = i + d
      if (j < 0 || j >= l.length) return
      ;[l[i], l[j]] = [l[j], l[i]]
    })

  const importFile = async (file: File) => {
    try {
      const data = JSON.parse(await file.text())
      const list: GrammarDef[] = Array.isArray(data) ? data : data.states ? [data] : Object.values(data)
      if (!list.every((g) => g && g.id && g.states)) throw new Error(t('expected format: a grammar {id, name, extensions, states} or a list'))
      await request('settings.snapshot', { label: 'Before highlighting import' })
      updateSettings((s) => {
        for (const g of list) s.syntax[g.id] = g
      }, `Import of ${list.length} grammar(s)`)
      toast(tn(list.length, '{n} grammar imported; the previous state is in the history', '{n} grammars imported; the previous state is in the history'), 'ok')
    } catch (e) {
      errorToast(e)
    }
  }

  return (
    <section>
      <div class="field-row">
        <label class="field">
          <span>{t('code|Language')}</span>
          <select value={lang()} onChange={(e) => setLang(e.currentTarget.value)}>
            <For each={grammarDefs()}>{(g) => <option value={g.id}>{g.name}{settings.syntax[g.id] ? ` (${t('changed')})` : ''}</option>}</For>
          </select>
        </label>
        <span class="grow" />
        <button class="btn small" onClick={() => download(`highlighting-${lang()}.json`, def())}>
          {t('Export')}
        </button>
        <button class="btn small" onClick={() => download('highlighting.json', grammarDefs())}>
          {t('Export all')}
        </button>
        <label class="btn small">
          {t('Import…')}
          <input type="file" accept="application/json,.json" hidden onChange={(e) => e.currentTarget.files?.[0] && importFile(e.currentTarget.files[0])} />
        </label>
        <button class="btn small" disabled={!settings.syntax[lang()]} onClick={() => updateSettings((s) => delete s.syntax[lang()], `Default ${def().name} highlighting`)}>
          {t('Reset')}
        </button>
      </div>
      <div class="field-row">
        <label class="field grow">
          <span>{t('Extensions')}</span>
          <input value={def().extensions.join(', ')} onChange={(e) => edit((g) => (g.extensions = e.currentTarget.value.split(',').map((x) => x.trim()).filter(Boolean)))} />
        </label>
        <label class="field grow">
          <span>{t('Detection by content (regex)')}</span>
          <input class="mono" value={def().detect ?? ''} onChange={(e) => edit((g) => (g.detect = e.currentTarget.value || undefined))} />
        </label>
      </div>
      <p class="muted small">
        {t('Ordered rules: at each position, the first matching regex wins. “Next state” pushes a state (comment, multi-line string…); “@pop” goes back to the previous state.')}
      </p>
      <Show when={errors().length}>
        <div class="test-result danger">
          <For each={errors()}>{(e) => <div class="mono small">{e}</div>}</For>
        </div>
      </Show>
      <For each={Object.keys(def().states)}>
        {(state) => (
          <div class="grammar-state">
            <h4>
              {t('State')} <code>{state}</code>
            </h4>
            <table class="rules">
              <thead>
                <tr>
                  <th>{t('Token')}</th>
                  <th>{t('Regex')}</th>
                  <th>{t('Flags')}</th>
                  <th>{t('Next state')}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                <Index each={def().states[state]}>
                  {(r, i) => (
                    <tr>
                      <td>
                        <select value={r().include ? '@include' : r().token} onChange={(e) => setRule(state, i, { token: e.currentTarget.value })} disabled={!!r().include}>
                          <option value="text">{t('(text)')}</option>
                          <For each={tokenTypes}>{(t) => <option value={t}>{t}</option>}</For>
                          <Show when={r().include}>
                            <option value="@include">{t('include')}</option>
                          </Show>
                        </select>
                      </td>
                      <td>
                        <Show when={!r().include} fallback={<span class="muted">{t('includes the state “{state}”', { state: r().include ?? '' })}</span>}>
                          <input class="mono" value={r().regex} onChange={(e) => setRule(state, i, { regex: e.currentTarget.value })} />
                        </Show>
                      </td>
                      <td>
                        <input class="mono w-flags" value={r().flags ?? ''} onChange={(e) => setRule(state, i, { flags: e.currentTarget.value || undefined })} />
                      </td>
                      <td>
                        <input class="mono w-next" value={r().next ?? ''} placeholder="—" onChange={(e) => setRule(state, i, { next: e.currentTarget.value || undefined })} />
                      </td>
                      <td class="nowrap">
                        <button class="icon-btn" title={t('Up')} onClick={() => move(state, i, -1)}>
                          ↑
                        </button>
                        <button class="icon-btn" title={t('Down')} onClick={() => move(state, i, 1)}>
                          ↓
                        </button>
                        <button class="icon-btn" title={t('Delete')} onClick={() => edit((g) => g.states[state].splice(i, 1))}>
                          ✕
                        </button>
                      </td>
                    </tr>
                  )}
                </Index>
              </tbody>
            </table>
            <button class="btn small" onClick={() => edit((g) => g.states[state].unshift({ token: 'keyword', regex: '\\bmot\\b' }))}>
              {t('+ Rule at the top')}
            </button>
            <button class="btn small" onClick={() => edit((g) => g.states[state].push({ token: 'keyword', regex: '\\bmot\\b' }))}>
              {t('+ Rule at the end')}
            </button>
          </div>
        )}
      </For>
      <button
        class="btn small"
        onClick={() => {
          const name = window.prompt(t('Name of the new state'))
          if (name) edit((g) => (g.states[name] = []))
        }}
      >
        {t('+ State')}
      </button>
      <Show when={samples[lang()]} keyed>
        <Preview lang={lang()} />
      </Show>
    </section>
  )
}

function LanguageSettings() {
  return (
    <section class="form">
      <label class="field">
        <span>{t('Language of the interface')}</span>
        <select value={settings.language} onChange={(e) => updateSettings((s) => (s.language = e.currentTarget.value as LangSetting), 'Language')} data-testid="settings-language">
          <option value="auto">{t('Automatic (browser: {name})', { name: languages.find((l) => l.id === browserLang())?.name ?? 'English' })}</option>
          <For each={languages}>{(l) => <option value={l.id}>{l.name}</option>}</For>
        </select>
      </label>
      <p class="muted small">{t('The assistant answers in the language you write in, whatever this setting.')}</p>
    </section>
  )
}

function Workspace() {
  const [info, { refetch }] = createResource(() => request('workspace.get'))
  const [value, setValue] = createSignal('')
  return (
    <section class="form">
      <label class="field">
        <span>{t('Workspace (folder suggested for new local projects)')}</span>
        <div class="field-row">
          <input class="grow" value={value() || info()?.workspace || ''} onInput={(e) => setValue(e.currentTarget.value)} />
          <button
            class="btn"
            onClick={async () => {
              try {
                await request('workspace.set', { path: value() || info()?.workspace })
                refetch()
                toast(t('Workspace saved'), 'ok')
              } catch (e) {
                errorToast(e)
              }
            }}
          >
            {t('Save')}
          </button>
        </div>
      </label>
      <dl class="props">
        <dt>{t('Pod data')}</dt>
        <dd class="mono">{info()?.dataDir}</dd>
        <dt>{t('Content')}</dt>
        <dd class="small">{t('config.json, projects.json, settings.json (history), sessions/, secrets.json (remembered passwords, 0600), known_hosts, token')}</dd>
        <dt>{t('Pairing')}</dt>
        <dd class="small">{t('Token in {path}; delete this file and restart the pod to get a new one.', { path: `${info()?.dataDir ?? ''}/token` })}</dd>
      </dl>
    </section>
  )
}

/** Label of a settings snapshot (saved in English, some with a value). */
function historyLabel(label: string) {
  let m = /^Back to #(\d+)$/.exec(label)
  if (m) return t('Back to #{id}', { id: m[1] })
  m = /^Theme (.+)$/.exec(label)
  if (m) return t('Theme {name}', { name: t(m[1]) })
  return t(label)
}

function History() {
  const [list, { refetch }] = createResource(() => request<any[]>('settings.history'))
  const rollback = async (id: number) => {
    try {
      const e = await request('settings.rollback', { id })
      replaceSettings(e.settings)
      refetch()
      toast(t('Settings restored (#{id}); this can itself be undone', { id }), 'ok')
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <section>
      <p class="muted small">{t('Each change saves a full snapshot. Going back to a version creates a new entry: going back can be undone the same way.')}</p>
      <div class="history-table">
        <For each={list() ?? []}>
          {(e) => (
            <div class="history-entry" classList={{ current: e.current }}>
              <span class="mono muted">#{e.id}</span>
              <span>{historyLabel(e.label)}</span>
              <span class="muted small">{fmtDate(e.ts)}</span>
              <Show when={!e.current} fallback={<span class="badge ok">{t('current')}</span>}>
                <button class="btn small" onClick={() => rollback(e.id)}>
                  {t('Back to this version')}
                </button>
              </Show>
            </div>
          )}
        </For>
      </div>
    </section>
  )
}

export type { Settings }
