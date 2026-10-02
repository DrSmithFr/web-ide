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
    toast("Ce fichier n'est pas en conflit", 'info')
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
    if (pending() && !confirm(`${pending()} conflit(s) non traité(s) : appliquer quand même (la version de base sera gardée pour ces blocs) ?`)) return
    resolveConflict(props.doc, result.join('\n'))
    toast(`Conflit résolu sur ${basename(props.doc.path)} (enregistrer pour écrire le fichier)`, 'ok')
    props.onClose()
  }

  const lines = (t: string) => Math.max(1, t.split('\n').length)

  return (
    <Modal
      title={`Résoudre le conflit · ${relPath(props.doc.path)}`}
      onClose={props.onClose}
      class="modal-full"
      footer={
        <>
          <span class="muted">{pending() ? `${pending()} conflit(s) à traiter` : 'Tous les conflits sont traités'}</span>
          <span class="grow" />
          <button class="btn" onClick={() => openTextTab(`${basename(props.doc.path)} (marqueurs)`, withMarkers(outcome.blocks), props.doc.lang)}>
            Version avec marqueurs
          </button>
          <button class="btn" onClick={() => acceptAll('local')}>
            Tout garder à gauche
          </button>
          <button class="btn" onClick={() => acceptAll('remote')}>
            Tout prendre à droite
          </button>
          <button class="btn primary" onClick={apply}>
            Appliquer
          </button>
        </>
      }
    >
      <div class="merge">
        <div class="merge-head">
          <div>Modification en cours</div>
          <div>Résultat</div>
          <div>Nouvelle version (rév. {c.rev})</div>
        </div>
        <div class="merge-body">
          <For each={rows}>
            {(r, i) => (
              <div class="merge-row" classList={{ conflict: r.kind === 'conflict', resolved: !!r.resolved }}>
                <pre class="merge-side">{r.local}</pre>
                <div class="merge-mid">
                  <Show when={r.kind === 'conflict'}>
                    <div class="merge-gutter">
                      <button class="icon-btn" title="Accepter la gauche" onClick={() => accept(i(), 'local')}>
                        ≫
                      </button>
                      <button class="icon-btn" title="Accepter les deux (gauche puis droite)" onClick={() => accept(i(), 'both')}>
                        ⇔
                      </button>
                      <button class="icon-btn" title="Accepter la droite" onClick={() => accept(i(), 'remote')}>
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
