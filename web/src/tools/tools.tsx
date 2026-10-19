// Conflicts tool: the files whose merge with the disk failed (the buffers are the core's).
import { For } from 'solid-js'
import { conflictedDocs, docsVersion, openFile, relPath } from '../state/project'
import { openConflict } from '../conflict/ConflictDialog'
import { shortcutOf } from '../keys/bindings'
import { t } from '../i18n'
import { EmptyState } from '../ui/EmptyState'

export function ConflictsTool() {
  const list = () => (docsVersion(), conflictedDocs())
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Conflicts')}</span>
      </div>
      <div class="panel-body pad">
        <p class="muted small">
          {t('Files changed on disk (by the AI or another tool) while you were editing them, and whose automatic merge failed. Resolution: {shortcut}.', { shortcut: shortcutOf('conflict.resolve') })}
        </p>
        <For each={list()} fallback={<EmptyState icon="check" text={t('No conflict.')} />}>
          {(d) => (
            <div class="conflict-item">
              <button class="link" onClick={() => openFile(d.path)}>
                {relPath(d.path)}
              </button>
              <span class="muted small">{t('remote rev. {rev}', { rev: d.conflict()?.rev ?? '' })}</span>
              <button class="btn small primary" onClick={() => openConflict(d)}>
                {t('Resolve')}
              </button>
            </div>
          )}
        </For>
      </div>
    </div>
  )
}
