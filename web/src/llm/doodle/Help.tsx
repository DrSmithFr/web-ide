// Keys of the doodle, shown by ? (button or key).
import { For } from 'solid-js'
import { t } from '../../i18n'

const groups = () => [
  {
    title: t('Tools'),
    rows: [
      ['V', t('Select')],
      ['P', t('Pen')],
      ['M', t('Marker')],
      ['E', t('Eraser')],
      ['R', t('Rectangle')],
      ['O', t('Ellipse')],
      ['L', t('Line')],
      ['A', t('Arrow')],
      ['T', t('Text')],
      ['K', t('Layout')],
    ],
  },
  {
    title: t('Drawing'),
    rows: [
      ['Shift', t('Square, circle, steps of 45°')],
      ['G', t('Magnetic grid')],
      [t('Stylus eraser end'), t('Erases without changing tool')],
      ['Ctrl+Z / Ctrl+Shift+Z', t('Undo / redo')],
    ],
  },
  {
    title: t('Selection'),
    rows: [
      [t('Shift+click, drag'), t('Add, rubber band')],
      ['Ctrl+A', t('Select all')],
      [t('Arrows (Shift: 10)'), t('Nudge')],
      ['Ctrl+D', t('Duplicate')],
      ['Ctrl+C / Ctrl+X / Ctrl+V', t('Copy, cut, paste (between doodles too)')],
      [t('Del'), t('Delete')],
      [t('Double click'), t('Edit a text, name a zone')],
    ],
  },
  {
    title: t('View'),
    rows: [
      [t('Wheel, Space+drag'), t('Pan')],
      [t('Ctrl+wheel, pinch'), t('Zoom')],
      ['0', t('Fit the frame')],
      ['Ctrl+V', t('Paste an image as the background')],
      ['Esc', t('Clear the selection, then close')],
    ],
  },
]

export function Help(props: { onClose: () => void }) {
  return (
    <div class="dd-help" role="dialog" aria-label={t('Keyboard shortcuts')} onPointerDown={(e) => e.stopPropagation()} data-testid="dd-help">
      <div class="dd-help-head">
        <strong>{t('Keyboard shortcuts')}</strong>
        <button class="icon-btn" title={t('Close (Esc)')} onClick={props.onClose}>
          ✕
        </button>
      </div>
      <div class="dd-help-body">
        <For each={groups()}>
          {(g) => (
            <section>
              <h3>{g.title}</h3>
              <For each={g.rows}>
                {([k, label]) => (
                  <div class="dd-help-row">
                    <kbd>{k}</kbd>
                    <span>{label}</span>
                  </div>
                )}
              </For>
            </section>
          )}
        </For>
      </div>
    </div>
  )
}
