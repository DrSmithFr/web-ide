// Conflict resolution in three panes (PhpStorm style): current changes on the left,
// editable result in the middle, new version on the right. Each block lines up on one row,
// so the three columns scroll together.
import { createSignal, For, Show } from 'solid-js'
import { createStore } from 'solid-js/store'
import type { Doc } from '../editor/doc'
import { merge3, withMarkers, type Block } from '../editor/merge'
import { Modal } from '../ui/overlay'
import { basename, openTextTab, relPath, resolveConflict } from '../state/project'
import { toast } from '../ui/toast'
import { t } from '../i18n'

interface Row {
  kind: 'ok' | 'conflict'
  local: string
  remote: string
  base: string
  resolved: '' | 'local' | 'remote' | 'both' | 'edited'
}

const [target, setTarget] = createSignal<Doc | null>(null)
export function openConflict(doc: Doc) {
  if (!doc.conflict()) {
    toast(t('This file is not in conflict'), 'info')
    return
  }
  setTarget(doc)
}

export function ConflictHost() {
  return <Show when={target()}>{(d) => <ConflictDialog doc={d()} onClose={() => setTarget(null)} />}</Show>
}

function rowsOf(blocks: Block[]): Row[] {
  return blocks.map((b) =>
    b.kind === 'ok'
      ? { kind: 'ok', local: b.lines.join('\n'), remote: b.lines.join('\n'), base: b.lines.join('\n'), resolved: '' }
      : { kind: 'conflict', local: b.local.join('\n'), remote: b.remote.join('\n'), base: b.base.join('\n'), resolved: '' },
  )
}

function ConflictDialog(props: { doc: Doc; onClose: () => void }) {
  const c = props.doc.conflict()!
  const outcome = merge3(props.doc.text, props.doc.base, c.remote)
  const [rows, setRows] = createStore<Row[]>(rowsOf(outcome.blocks))
  // Result column: one editable text per block; conflicts start from the common base.
  const [result, setResult] = createStore<string[]>(rows.map((r) => (r.kind === 'ok' ? r.local : r.base)))

  const accept = (i: number, side: 'local' | 'remote' | 'both') => {
    const r = rows[i]
    const text = side === 'local' ? r.local : side === 'remote' ? r.remote : [r.local, r.remote].filter((x) => x !== '').join('\n')
    setResult(i, text)
    setRows(i, 'resolved', side)
  }
  const acceptAll = (side: 'local' | 'remote') => rows.forEach((r, i) => r.kind === 'conflict' && accept(i, side))
  const pending = () => rows.filter((r) => r.kind === 'conflict' && !r.resolved).length

  const apply = () => {
    if (pending() && !confirm(t('{n} conflict(s) not handled: apply anyway (the base version is kept for these blocks)?', { n: pending() }))) return
    resolveConflict(props.doc, result.join('\n'))
    toast(t('Conflict resolved on {file} (save to write the file)', { file: basename(props.doc.path) }), 'ok')
    props.onClose()
  }

  const lines = (t: string) => Math.max(1, t.split('\n').length)

  return (
    <Modal
      title={`${t('Resolve the conflict')} · ${relPath(props.doc.path)}`}
      onClose={props.onClose}
      class="modal-full"
      footer={
        <>
          <span class="muted">{pending() ? t('{n} conflict(s) to handle', { n: pending() }) : t('All the conflicts are handled')}</span>
          <span class="grow" />
          <button class="btn" onClick={() => openTextTab(`${basename(props.doc.path)} (${t('markers')})`, withMarkers(outcome.blocks), props.doc.lang)}>
            {t('Version with markers')}
          </button>
          <button class="btn" onClick={() => acceptAll('local')}>
            {t('Keep all the left side')}
          </button>
          <button class="btn" onClick={() => acceptAll('remote')}>
            {t('Take all the right side')}
          </button>
          <button class="btn primary" onClick={apply}>
            {t('Apply')}
          </button>
        </>
      }
    >
      <div class="merge">
        <div class="merge-head">
          <div>{t('Local changes')}</div>
          <div>{t('Result')}</div>
          <div>{t('New version (rev. {rev})', { rev: c.rev })}</div>
        </div>
        <div class="merge-body">
          <For each={rows}>
            {(r, i) => (
              <div class="merge-row" classList={{ conflict: r.kind === 'conflict', resolved: !!r.resolved }}>
                <pre class="merge-side">{r.local}</pre>
                <div class="merge-mid">
                  <Show when={r.kind === 'conflict'}>
                    <div class="merge-gutter">
                      <button class="icon-btn" title={t('Accept the left side')} onClick={() => accept(i(), 'local')}>
                        ≫
                      </button>
                      <button class="icon-btn" title={t('Accept both (left then right)')} onClick={() => accept(i(), 'both')}>
                        ⇔
                      </button>
                      <button class="icon-btn" title={t('Accept the right side')} onClick={() => accept(i(), 'remote')}>
                        ≪
                      </button>
                    </div>
                  </Show>
                  <textarea
                    class="merge-result"
                    spellcheck={false}
                    rows={Math.max(lines(r.local), lines(r.remote), lines(result[i()] ?? ''))}
                    value={result[i()]}
                    onInput={(e) => {
                      setResult(i(), e.currentTarget.value)
                      if (r.kind === 'conflict') setRows(i(), 'resolved', 'edited')
                    }}
                  />
                </div>
                <pre class="merge-side">{r.remote}</pre>
              </div>
            )}
          </For>
        </div>
      </div>
    </Modal>
  )
}
