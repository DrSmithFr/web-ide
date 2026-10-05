// Tool bar of the doodle: tools, their options, undo / redo, grid, frame and zoom.
import { For, Show } from 'solid-js'
import { Icon } from '../../ui/icons'
import { t } from '../../i18n'
import { fluoColors, PEN_SIZES, penColors, presets, type FluoColor, type PenColor, type Preset, type TextSize } from './model'
import type { History } from './history'
import { canCapture } from './background'

export type Tool = 'select' | 'pen' | 'marker' | 'eraser' | 'rect' | 'ellipse' | 'line' | 'arrow' | 'text' | 'layout'

export interface Tools {
  tool: Tool
  penSize: keyof typeof PEN_SIZES
  penColor: PenColor
  fluoColor: FluoColor
  textSize: TextSize
  eraser: 'pixel' | 'object'
  pressure: boolean
  grid: boolean
  fill: boolean
}

const TOOLS_KEY = 'doodle.tools'
const defaults: Tools = { tool: 'pen', penSize: 'normal', penColor: 'ink', fluoColor: 'yellow', textSize: 'm', eraser: 'pixel', pressure: true, grid: false, fill: false }

export function loadTools(): Tools {
  try {
    return { ...defaults, ...JSON.parse(localStorage.getItem(TOOLS_KEY) ?? '{}') }
  } catch {
    return defaults
  }
}

export function saveTools(v: Tools) {
  try {
    localStorage.setItem(TOOLS_KEY, JSON.stringify(v))
  } catch {}
}

/** Keys of the tools. */
export const toolKeys: Record<string, Tool> = { v: 'select', p: 'pen', m: 'marker', e: 'eraser', r: 'rect', o: 'ellipse', l: 'line', a: 'arrow', t: 'text', k: 'layout' }

const colorLabel = (c: PenColor | FluoColor) =>
  ({ ink: t('Black'), red: t('Red'), blue: t('Blue'), green: t('Green'), yellow: t('Yellow'), lime: t('Green'), pink: t('Pink'), cyan: t('Cyan') })[c]

export const presetLabel = (id: Preset) => (id === 'square' ? t('Square') : id === 'mobile' ? t('Mobile') : id === 'free' ? t('Free') : id === 'image' ? t('Image') : id)

const textSizeLabel = { s: () => t('Small text'), m: () => t('Medium text'), l: () => t('Large text') }

export function Toolbar(props: {
  tools: Tools
  setTools: (p: Partial<Tools>) => void
  dark: boolean
  h: History
  /** Something is selected: colors and text sizes apply to it. */
  selection: boolean
  onColor: (c: PenColor) => void
  onTextSize: (s: TextSize) => void
  onFill: (fill: boolean) => void
  /** The selection holds rectangles or ellipses. */
  fillable: boolean
  onDelete: () => void
  onPreset: (p: Preset) => void
  onPickBackground: () => void
  onRemoveBackground: () => void
  onScreenshot: () => void
  zoom: number
  onZoom: (factor: number) => void
  onFit: () => void
}) {
  const tl = () => props.tools
  const tool = (id: Tool, icon: string, label: string, key: string) => (
    <button class="dd-btn" classList={{ on: tl().tool === id }} title={`${label} (${key})`} aria-pressed={tl().tool === id} onClick={() => props.setTools({ tool: id })} data-testid={`dd-${id}`}>
      <Icon name={icon} size={16} />
    </button>
  )
  const inks = () => ['pen', 'rect', 'ellipse', 'line', 'arrow', 'text', 'layout'].includes(tl().tool) || (tl().tool === 'select' && props.selection)
  const sizes = () => ['pen', 'rect', 'ellipse', 'line', 'arrow'].includes(tl().tool)
  const textSizes = () => tl().tool === 'text' || (tl().tool === 'select' && props.selection)
  return (
    <div class="dd-toolbar" role="toolbar" aria-label={t('Drawing tools')}>
      {tool('select', 'cursor', t('Select'), 'V')}
      {tool('pen', 'pen', t('Pen'), 'P')}
      {tool('marker', 'marker', t('Marker'), 'M')}
      {tool('eraser', 'eraser', t('Eraser'), 'E')}
      <span class="sep" />
      {tool('rect', 'rect', t('Rectangle'), 'R')}
      {tool('ellipse', 'ellipse', t('Ellipse'), 'O')}
      {tool('line', 'line', t('Line'), 'L')}
      {tool('arrow', 'arrow', t('Arrow'), 'A')}
      {tool('text', 'text', t('Text'), 'T')}
      {tool('layout', 'layout', t('Layout: draw a box, then split its zones'), 'K')}
      <span class="sep" />
      <Show when={sizes()}>
        <For each={['thin', 'normal'] as const}>
          {(s) => (
            <button class="dd-btn dd-size" classList={{ on: tl().penSize === s }} title={s === 'thin' ? t('Thin pen') : t('Normal pen')} onClick={() => props.setTools({ penSize: s })}>
              <span style={{ width: `${PEN_SIZES[s] * 2}px`, height: `${PEN_SIZES[s] * 2}px` }} />
            </button>
          )}
        </For>
      </Show>
      <Show when={textSizes()}>
        <div class="dd-seg" role="group">
          <For each={['s', 'm', 'l'] as const}>
            {(s) => (
              <button classList={{ on: tl().textSize === s }} title={textSizeLabel[s]()} onClick={() => props.onTextSize(s)} data-testid={`dd-text-${s}`}>
                {s.toUpperCase()}
              </button>
            )}
          </For>
        </div>
      </Show>
      <Show when={inks()}>
        <For each={Object.keys(penColors) as PenColor[]}>
          {(c) => (
            <button class="dd-swatch" classList={{ on: tl().penColor === c }} title={colorLabel(c)} style={{ background: penColors[c][props.dark ? 'dark' : 'light'] }} onClick={() => props.onColor(c)} data-testid={`dd-color-${c}`} />
          )}
        </For>
      </Show>
      <Show when={tl().tool === 'rect' || tl().tool === 'ellipse' || (tl().tool === 'select' && props.fillable)}>
        <button class="dd-btn" classList={{ on: tl().fill }} title={t('Filled: a light tint of the color inside')} aria-pressed={tl().fill} onClick={() => props.onFill(!tl().fill)} data-testid="dd-fill">
          <Icon name="fill" size={16} />
        </button>
      </Show>
      <Show when={tl().tool === 'pen'}>
        <label class="dd-check small" title={t('The width follows the pressure of the stylus')}>
          <input type="checkbox" checked={tl().pressure} onChange={(e) => props.setTools({ pressure: e.currentTarget.checked })} /> {t('Pressure')}
        </label>
      </Show>
      <Show when={tl().tool === 'marker'}>
        <For each={Object.keys(fluoColors) as FluoColor[]}>
          {(c) => (
            <button class="dd-swatch fluo" classList={{ on: tl().fluoColor === c }} title={colorLabel(c)} style={{ background: fluoColors[c][props.dark ? 'dark' : 'light'] }} onClick={() => props.setTools({ fluoColor: c })} data-testid={`dd-fluo-${c}`} />
          )}
        </For>
      </Show>
      <Show when={tl().tool === 'eraser'}>
        <div class="dd-seg" role="group">
          <button classList={{ on: tl().eraser === 'pixel' }} title={t('Erases what it touches')} onClick={() => props.setTools({ eraser: 'pixel' })} data-testid="dd-eraser-pixel">
            {t('Pixel')}
          </button>
          <button classList={{ on: tl().eraser === 'object' }} title={t('Removes a whole stroke')} onClick={() => props.setTools({ eraser: 'object' })} data-testid="dd-eraser-object">
            {t('Object')}
          </button>
        </div>
      </Show>
      <Show when={tl().tool === 'select' && props.selection}>
        <button class="dd-btn" title={t('Delete (Del)')} onClick={props.onDelete} data-testid="dd-delete">
          <Icon name="trash" size={16} />
        </button>
      </Show>
      <span class="sep" />
      <button class="dd-btn" title={t('Undo (Ctrl+Z)')} disabled={!props.h.canUndo()} onClick={() => props.h.undo()} data-testid="dd-undo">
        <Icon name="undo" size={16} />
      </button>
      <button class="dd-btn" title={t('Redo (Ctrl+Shift+Z)')} disabled={!props.h.canRedo()} onClick={() => props.h.redo()} data-testid="dd-redo">
        <Icon name="redo" size={16} />
      </button>
      <span class="grow" />
      <button class="dd-btn" title={t('Background image… (or paste one with Ctrl+V)')} onClick={props.onPickBackground} data-testid="dd-bg">
        <Icon name="image" size={16} />
      </button>
      <Show when={canCapture()}>
        <button class="dd-btn" title={t('Screenshot as background')} onClick={props.onScreenshot} data-testid="dd-screenshot">
          <Icon name="screen" size={16} />
        </button>
      </Show>
      <Show when={props.h.doc().background}>
        <button class="dd-btn" title={t('Remove the background image')} onClick={props.onRemoveBackground} data-testid="dd-bg-remove">
          <Icon name="imageOff" size={16} />
        </button>
      </Show>
      <span class="sep" />
      <button class="dd-btn" classList={{ on: tl().grid }} title={t('Magnetic grid (G)')} aria-pressed={tl().grid} onClick={() => props.setTools({ grid: !tl().grid })} data-testid="dd-grid">
        <Icon name="grid" size={16} />
      </button>
      <select class="dd-select" title={t('Frame')} value={props.h.doc().preset} onChange={(e) => props.onPreset(e.currentTarget.value as Preset)} data-testid="dd-preset">
        <For each={presets.filter((p) => p.id !== 'image' || props.h.doc().background)}>{(p) => <option value={p.id}>{presetLabel(p.id)}</option>}</For>
      </select>
      <div class="dd-zoom">
        <button title={t('Zoom out')} onClick={() => props.onZoom(1 / 1.25)}>
          −
        </button>
        <button title={t('Fit the frame (0)')} onClick={props.onFit}>
          {Math.round(props.zoom * 100)} %
        </button>
        <button title={t('Zoom in')} onClick={() => props.onZoom(1.25)}>
          +
        </button>
      </div>
    </div>
  )
}
