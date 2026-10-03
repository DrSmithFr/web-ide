// Editor of the icon of a project: glyph or text, shape, colour or gradient.
import { createMemo, createResource, createSignal, For, Show } from 'solid-js'
import { createStore, unwrap } from 'solid-js/store'
import { Modal } from './overlay'
import { errorToast } from './toast'
import { angles, defaultSpec, iconURL, type IconShape, type IconSpec, palette, renderIcon, saveIcon, shapes } from './projectIcon'
import { t } from '../i18n'

const shapeLabels: Record<IconShape, string> = { circle: 'Circle', rounded: 'Rounded square', square: 'Square', hexagon: 'Hexagon', diamond: 'Diamond' }
const arrows = ['→', '↘', '↓', '↙', '←', '↖', '↑', '↗']

function Swatches(props: { value: string; onPick: (c: string) => void; testid: string }) {
  return (
    <div class="ie-swatches" data-testid={props.testid}>
      <For each={palette}>
        {(c) => <button type="button" class="ie-swatch" classList={{ on: props.value === c }} style={{ background: c }} title={c} onClick={() => props.onPick(c)} />}
      </For>
      <input type="color" value={props.value} title={t('Other colour')} onInput={(e) => props.onPick(e.currentTarget.value)} />
    </div>
  )
}

export function IconEditor(props: { id: string; name: string; spec: IconSpec | null; onClose: () => void }) {
  const [spec, setSpec] = createStore<IconSpec>(structuredClone(props.spec ?? defaultSpec(props.id, props.name)))
  const [glyphs] = createResource(() => import('./glyphs').then((m) => m.glyphs))
  const [query, setQuery] = createSignal('')
  const svg = createMemo(() => renderIcon(spec, spec.kind === 'glyph' ? (glyphs()?.[spec.glyph ?? ''] ?? '') : ''))
  const names = createMemo(() => Object.keys(glyphs() ?? {}).filter((n) => n.includes(query().trim().toLowerCase())))

  const save = async () => {
    try {
      const s = structuredClone(unwrap(spec))
      if (s.kind === 'glyph' && !s.glyph) s.kind = 'text'
      await saveIcon(props.id, s, svg())
      props.onClose()
    } catch (e) {
      errorToast(e)
    }
  }

  return (
    <Modal
      title={t('Icon · {name}', { name: props.name })}
      class="icon-editor"
      onClose={props.onClose}
      footer={
        <>
          <button class="btn" onClick={() => setSpec(defaultSpec(props.id, props.name))}>
            {t('Default icon')}
          </button>
          <span class="grow" />
          <button class="btn" onClick={props.onClose}>
            {t('Cancel')}
          </button>
          <button class="btn primary" onClick={save} data-testid="icon-save">
            {t('Save')}
          </button>
        </>
      }
    >
      <div class="ie">
        <div class="ie-preview">
          <img src={iconURL(svg())} width="96" height="96" alt="" data-testid="icon-preview" />
          <div class="ie-small">
            <img src={iconURL(svg())} width="32" height="32" alt="" />
            <img src={iconURL(svg())} width="16" height="16" alt="" />
          </div>
        </div>
        <div class="ie-form">
          <div class="segmented" role="radiogroup">
            <button type="button" role="radio" aria-checked={spec.kind === 'glyph'} classList={{ on: spec.kind === 'glyph' }} onClick={() => setSpec('kind', 'glyph')} data-testid="icon-kind-glyph">
              {t('Icon')}
            </button>
            <button type="button" role="radio" aria-checked={spec.kind === 'text'} classList={{ on: spec.kind === 'text' }} onClick={() => setSpec('kind', 'text')} data-testid="icon-kind-text">
              {t('Text')}
            </button>
          </div>
          <Show
            when={spec.kind === 'glyph'}
            fallback={
              <label class="field">
                <span>{t('Text (1 to 3 characters)')}</span>
                <input value={spec.text ?? ''} maxLength={3} onInput={(e) => setSpec('text', e.currentTarget.value)} data-testid="icon-text" />
              </label>
            }
          >
            <input class="ie-search" placeholder={t('Search an icon…')} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} data-testid="icon-search" />
            <div class="ie-glyphs">
              <For each={names()} fallback={<span class="muted small">{glyphs.loading ? t('Loading…') : t('No icon')}</span>}>
                {(n) => (
                  <button type="button" class="ie-glyph" classList={{ on: spec.glyph === n }} title={n} data-glyph={n} onClick={() => setSpec('glyph', n)}>
                    <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" innerHTML={glyphs()![n]} />
                  </button>
                )}
              </For>
            </div>
          </Show>
          <div class="field">
            <span>{t('Shape')}</span>
            <div class="ie-shapes">
              <For each={shapes}>
                {(s) => (
                  <button type="button" class="ie-shape" classList={{ on: spec.shape === s }} title={t(shapeLabels[s])} data-shape={s} onClick={() => setSpec('shape', s)}>
                    <img src={iconURL(renderIcon({ kind: 'text', text: '', shape: s, color: '#8a94a6', fg: '#fff' }))} width="22" height="22" alt="" />
                  </button>
                )}
              </For>
            </div>
          </div>
          <div class="field">
            <span>{t('Background')}</span>
            <Swatches value={spec.color} onPick={(c) => setSpec('color', c)} testid="icon-color" />
          </div>
          <label class="ie-check">
            <input type="checkbox" checked={!!spec.color2} onChange={(e) => setSpec({ color2: e.currentTarget.checked ? palette[(palette.indexOf(spec.color) + 2) % 12] : undefined, angle: spec.angle ?? 45 })} data-testid="icon-gradient" />
            {t('Gradient')}
          </label>
          <Show when={spec.color2}>
            <Swatches value={spec.color2!} onPick={(c) => setSpec('color2', c)} testid="icon-color2" />
            <div class="ie-angles" title={t('Direction')}>
              <For each={angles}>
                {(a, i) => (
                  <button type="button" class="ie-angle" classList={{ on: (spec.angle ?? 90) === a }} onClick={() => setSpec('angle', a)} data-angle={a}>
                    {arrows[i()]}
                  </button>
                )}
              </For>
            </div>
          </Show>
          <div class="field">
            <span>{t('Foreground')}</span>
            <Swatches value={spec.fg} onPick={(c) => setSpec('fg', c)} testid="icon-fg" />
          </div>
        </div>
      </div>
    </Modal>
  )
}
