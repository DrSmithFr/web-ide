// Settings pop-in: navigation on the left, content on the right.
import { createMemo, createResource, createSignal, For, Index, onCleanup, onMount, Show } from 'solid-js'
import { unwrap } from 'solid-js/store'
import { Modal } from '../ui/overlay'
import { settings, updateSettings, replaceSettings, type Settings } from '../state/settings'
import { themes, themeById, tokenLabels, tokenTypes } from './themes'
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

const sections = [
  ['themes', 'Thèmes'],
  ['fonts', 'Polices'],
  ['editor', 'Éditeur'],
  ['keys', 'Raccourcis clavier'],
  ['syntax', 'Colorisation syntaxique'],
  ['workspace', 'Workspace et pod'],
  ['history', 'Historique des réglages'],
] as const

const [open, setOpen] = createSignal<string | null>(null)
export function openSettings(section = 'themes') {
  setOpen(section)
}

export function SettingsHost() {
  return (
    <Show when={open()}>
      <Modal title="Réglages" onClose={() => setOpen(null)} class="modal-settings">
        <div class="settings">
          <nav class="settings-nav">
            <For each={sections}>
              {([id, label]) => (
                <button classList={{ active: open() === id }} onClick={() => setOpen(id)}>
                  {label}
                </button>
              )}
            </For>
          </nav>
          <div class="settings-body">
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
  const t = () => themeById(settings.theme)
  const color = (tok: string) => settings.tokenColors[settings.theme]?.[tok] ?? t().tokens[tok]
  return (
    <section>
      <h3>Thème</h3>
      <div class="theme-grid">
        <For each={themes}>
          {(th) => (
            <button class="theme-card" classList={{ active: settings.theme === th.id }} onClick={() => updateSettings((s) => (s.theme = th.id), `Thème ${th.name}`)}>
              <span class="theme-swatch" style={{ background: th.ui.bg, color: th.ui.fg, 'border-color': th.ui.line }}>
                <span style={{ color: th.tokens.keyword }}>fn</span> <span style={{ color: th.tokens.function }}>main</span>
                <span style={{ color: th.tokens.punctuation }}>()</span> <span style={{ color: th.tokens.string }}>"ok"</span>
              </span>
              <span>{th.name}</span>
            </button>
          )}
        </For>
      </div>
      <h3>Couleurs des tokens · {t().name}</h3>
      <p class="muted small">Pas de gras : l'API de surlignage ne gère que la couleur, le fond, le soulignement et l'italique.</p>
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
                  }, 'Couleur de token')
                }}
              />
              <span style={{ color: color(tok) }}>{tokenLabels[tok]}</span>
            </label>
          )}
        </For>
      </div>
      <button class="btn small" disabled={!settings.tokenColors[settings.theme]} onClick={() => updateSettings((s) => delete s.tokenColors[s.theme], 'Couleurs par défaut')}>
        Couleurs par défaut du thème
      </button>
      <Preview lang="php" />
    </section>
  )
}

function Fonts() {
  const num = (path: 'size' | 'lineHeight' | 'uiSize', v: string) => {
    const n = parseFloat(v)
    if (n > 0) updateSettings((s) => ((s.font as any)[path] = n), 'Police')
  }
  return (
    <section class="form">
      <label class="field">
        <span>Police du code (pile CSS)</span>
        <input value={settings.font.family} onChange={(e) => updateSettings((s) => (s.font.family = e.currentTarget.value), 'Police')} />
      </label>
      <div class="field-row">
        <label class="field">
          <span>Taille du code (px)</span>
          <input type="number" min="8" max="32" value={settings.font.size} onChange={(e) => num('size', e.currentTarget.value)} />
        </label>
        <label class="field">
          <span>Interligne</span>
          <input type="number" min="1" max="2.5" step="0.05" value={settings.font.lineHeight} onChange={(e) => num('lineHeight', e.currentTarget.value)} />
        </label>
        <label class="field">
          <span>Taille de l'interface (px)</span>
          <input type="number" min="10" max="20" value={settings.font.uiSize} onChange={(e) => num('uiSize', e.currentTarget.value)} />
        </label>
      </div>
      <label class="check">
        <input type="checkbox" checked={settings.font.ligatures} onChange={(e) => updateSettings((s) => (s.font.ligatures = e.currentTarget.checked), 'Ligatures')} />
        Ligatures
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
          <span>Taille de tabulation</span>
          <input type="number" min="1" max="8" value={settings.editor.tabSize} onChange={(e) => updateSettings((s) => (s.editor.tabSize = parseInt(e.currentTarget.value, 10) || 4), 'Tabulation')} />
        </label>
      </div>
      <label class="check">
        <input type="checkbox" checked={settings.editor.insertSpaces} onChange={(e) => updateSettings((s) => (s.editor.insertSpaces = e.currentTarget.checked), 'Indentation')} />
        Indenter avec des espaces
      </label>
      <label class="check">
        <input type="checkbox" checked={settings.editor.highlightLine} onChange={(e) => updateSettings((s) => (s.editor.highlightLine = e.currentTarget.checked), 'Ligne courante')} />
        Surligner la ligne courante
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
      const names = others.map((a) => actionById.get(a)?.label ?? a).join(', ')
      if (!confirm(`${comboLabel(combo)} est déjà utilisé par : ${names}.\nLe retirer de ces actions et l'assigner ici ?`)) return
    }
    const eff = structuredClone(effectiveBindings())
    updateSettings((s) => {
      for (const a of others) s.keyboard.overrides[a] = eff[a].filter((c) => c !== combo)
      const cur = s.keyboard.overrides[action] ?? eff[action] ?? []
      if (!cur.includes(combo)) s.keyboard.overrides[action] = [...cur, combo]
    }, 'Raccourci')
  }
  const removeCombo = (action: string, combo: string) => {
    const cur = effectiveBindings()[action] ?? []
    updateSettings((s) => (s.keyboard.overrides[action] = cur.filter((c) => c !== combo)), 'Raccourci')
  }
  const reset = (action: string) => updateSettings((s) => delete s.keyboard.overrides[action], 'Raccourci réinitialisé')

  const list = createMemo(() => {
    const f = filter().toLowerCase()
    return actions.filter((a) => !f || a.label.toLowerCase().includes(f) || a.category.toLowerCase().includes(f) || (effectiveBindings()[a.id] ?? []).some((c) => comboLabel(c).toLowerCase().includes(f)))
  })

  return (
    <section>
      <div class="field-row">
        <label class="field">
          <span>Disposition du clavier</span>
          <select value={settings.keyboard.layout} onChange={(e) => updateSettings((s) => (s.keyboard.layout = e.currentTarget.value as any), 'Disposition')}>
            <option value="auto">Automatique ({detectedLayout() ? `détectée : ${presets[detectedLayout()!].name}` : `suggérée : ${presets[suggestedLayout()].name}`})</option>
            <option value="qwerty">QWERTY</option>
            <option value="azerty">AZERTY</option>
          </select>
        </label>
        <label class="field grow">
          <span>Filtrer</span>
          <input placeholder="action, catégorie ou touche" value={filter()} onInput={(e) => setFilter(e.currentTarget.value)} />
        </label>
      </div>
      <p class="muted small">
        Preset actif : {presets[activeLayout()].name}. Les raccourcis sont liés à la position physique des touches ; vos changements sont enregistrés comme dérogations au preset.
        {detectedLayout() ? '' : ' Détection automatique indisponible dans ce navigateur : la langue sert de suggestion.'}
      </p>
      <table class="keys-table">
        <tbody>
          <For each={list()}>
            {(a) => (
              <tr classList={{ overridden: !!settings.keyboard.overrides[a.id] }}>
                <td>
                  <div>{a.label}</div>
                  <div class="muted small">{a.category}</div>
                </td>
                <td class="keys-cell">
                  <For each={effectiveBindings()[a.id] ?? []}>
                    {(c) => (
                      <span class="chip">
                        <kbd>{comboLabel(c)}</kbd>
                        <button class="chip-x" title="Retirer" onClick={() => removeCombo(a.id, c)}>
                          ✕
                        </button>
                      </span>
                    )}
                  </For>
                  <Show when={capturing() === a.id} fallback={<button class="btn small" onClick={() => startCapture(a.id)}>+</button>}>
                    <span class="capture">Appuyez sur la combinaison… (Échap pour annuler)</span>
                  </Show>
                </td>
                <td>
                  <Show when={settings.keyboard.overrides[a.id]}>
                    <button class="btn small" onClick={() => reset(a.id)} title="Revenir au preset">
                      Réinitialiser
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
      if (!list.every((g) => g && g.id && g.states)) throw new Error('format attendu : une grammaire {id, name, extensions, states} ou une liste')
      await request('settings.snapshot', { label: 'Avant import de colorisation' })
      updateSettings((s) => {
        for (const g of list) s.syntax[g.id] = g
      }, `Import de ${list.length} grammaire(s)`)
      toast(`${list.length} grammaire(s) importée(s) ; l'état précédent est dans l'historique`, 'ok')
    } catch (e) {
      errorToast(e)
    }
  }

  return (
    <section>
      <div class="field-row">
        <label class="field">
          <span>Langage</span>
          <select value={lang()} onChange={(e) => setLang(e.currentTarget.value)}>
            <For each={grammarDefs()}>{(g) => <option value={g.id}>{g.name}{settings.syntax[g.id] ? ' (modifié)' : ''}</option>}</For>
          </select>
        </label>
        <span class="grow" />
        <button class="btn small" onClick={() => download(`colorisation-${lang()}.json`, def())}>
          Exporter
        </button>
        <button class="btn small" onClick={() => download('colorisation.json', grammarDefs())}>
          Tout exporter
        </button>
        <label class="btn small">
          Importer…
          <input type="file" accept="application/json,.json" hidden onChange={(e) => e.currentTarget.files?.[0] && importFile(e.currentTarget.files[0])} />
        </label>
        <button class="btn small" disabled={!settings.syntax[lang()]} onClick={() => updateSettings((s) => delete s.syntax[lang()], `Colorisation ${def().name} par défaut`)}>
          Réinitialiser
        </button>
      </div>
      <div class="field-row">
        <label class="field grow">
          <span>Extensions</span>
          <input value={def().extensions.join(', ')} onChange={(e) => edit((g) => (g.extensions = e.currentTarget.value.split(',').map((x) => x.trim()).filter(Boolean)))} />
        </label>
        <label class="field grow">
          <span>Détection par contenu (regex)</span>
          <input class="mono" value={def().detect ?? ''} onChange={(e) => edit((g) => (g.detect = e.currentTarget.value || undefined))} />
        </label>
      </div>
      <p class="muted small">
        Règles ordonnées : à chaque position, la première regex qui correspond gagne. « État suivant » empile un état (commentaire, chaîne multiligne…) ; « @pop » revient à l'état précédent.
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
              État <code>{state}</code>
            </h4>
            <table class="rules">
              <thead>
                <tr>
                  <th>Token</th>
                  <th>Regex</th>
                  <th>Drapeaux</th>
                  <th>État suivant</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                <Index each={def().states[state]}>
                  {(r, i) => (
                    <tr>
                      <td>
                        <select value={r().include ? '@include' : r().token} onChange={(e) => setRule(state, i, { token: e.currentTarget.value })} disabled={!!r().include}>
                          <option value="text">(texte)</option>
                          <For each={tokenTypes}>{(t) => <option value={t}>{t}</option>}</For>
                          <Show when={r().include}>
                            <option value="@include">inclure</option>
                          </Show>
                        </select>
                      </td>
                      <td>
                        <Show when={!r().include} fallback={<span class="muted">inclut l'état « {r().include} »</span>}>
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
                        <button class="icon-btn" title="Monter" onClick={() => move(state, i, -1)}>
                          ↑
                        </button>
                        <button class="icon-btn" title="Descendre" onClick={() => move(state, i, 1)}>
                          ↓
                        </button>
                        <button class="icon-btn" title="Supprimer" onClick={() => edit((g) => g.states[state].splice(i, 1))}>
                          ✕
                        </button>
                      </td>
                    </tr>
                  )}
                </Index>
              </tbody>
            </table>
            <button class="btn small" onClick={() => edit((g) => g.states[state].unshift({ token: 'keyword', regex: '\\bmot\\b' }))}>
              + Règle en tête
            </button>
            <button class="btn small" onClick={() => edit((g) => g.states[state].push({ token: 'keyword', regex: '\\bmot\\b' }))}>
              + Règle à la fin
            </button>
          </div>
        )}
      </For>
      <button
        class="btn small"
        onClick={() => {
          const name = window.prompt("Nom du nouvel état")
          if (name) edit((g) => (g.states[name] = []))
        }}
      >
        + État
      </button>
      <Show when={samples[lang()]} keyed>
        <Preview lang={lang()} />
      </Show>
    </section>
  )
}

function Workspace() {
  const [info, { refetch }] = createResource(() => request('workspace.get'))
  const [value, setValue] = createSignal('')
  return (
    <section class="form">
      <label class="field">
        <span>Workspace (dossier proposé pour les nouveaux projets locaux)</span>
        <div class="field-row">
          <input class="grow" value={value() || info()?.workspace || ''} onInput={(e) => setValue(e.currentTarget.value)} />
          <button
            class="btn"
            onClick={async () => {
              try {
                await request('workspace.set', { path: value() || info()?.workspace })
                refetch()
                toast('Workspace enregistré', 'ok')
              } catch (e) {
                errorToast(e)
              }
            }}
          >
            Enregistrer
          </button>
        </div>
      </label>
      <dl class="props">
        <dt>Données du pod</dt>
        <dd class="mono">{info()?.dataDir}</dd>
        <dt>Contenu</dt>
        <dd class="small">config.json, projects.json, settings.json (historique), sessions/, secrets.json (mots de passe mémorisés, 0600), known_hosts, token</dd>
        <dt>Appairage</dt>
        <dd class="small">Jeton dans {info()?.dataDir}/token ; supprimer ce fichier puis redémarrer le pod pour en générer un nouveau.</dd>
      </dl>
    </section>
  )
}

function History() {
  const [list, { refetch }] = createResource(() => request<any[]>('settings.history'))
  const rollback = async (id: number) => {
    try {
      const e = await request('settings.rollback', { id })
      replaceSettings(e.settings)
      refetch()
      toast(`Réglages restaurés (#${id}) ; ce retour peut lui-même être annulé`, 'ok')
    } catch (e) {
      errorToast(e)
    }
  }
  return (
    <section>
      <p class="muted small">Chaque modification enregistre un instantané complet. Revenir à une version crée une nouvelle entrée : un retour arrière s'annule de la même façon.</p>
      <div class="history-table">
        <For each={list() ?? []}>
          {(e) => (
            <div class="history-entry" classList={{ current: e.current }}>
              <span class="mono muted">#{e.id}</span>
              <span>{e.label}</span>
              <span class="muted small">{new Date(e.ts).toLocaleString()}</span>
              <Show when={!e.current} fallback={<span class="badge ok">actuel</span>}>
                <button class="btn small" onClick={() => rollback(e.id)}>
                  Revenir à cette version
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
