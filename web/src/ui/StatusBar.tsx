// Status bar under the editor area: caret position, then the format of the active file
// (line separator, encoding, indentation) and its language. A click on a format item
// opens a menu to convert the file (saved at once) or to choose its indentation.
import { createMemo, Show } from 'solid-js'
import { activeTab, docsVersion, getDoc, saveDoc } from '../state/project'
import { settings } from '../state/settings'
import type { Doc, FileFormat } from '../editor/doc'
import type { Indent } from '../editor/indent'
import { contextMenu, type MenuItem } from './overlay'
import { cursorInfo } from './status'
import { t, tn } from '../i18n'

const encodings: [string, string][] = [
  ['utf-8', 'UTF-8'],
  ['utf-8-bom', 'UTF-8 BOM'],
  ['utf-16le', 'UTF-16 LE'],
  ['utf-16be', 'UTF-16 BE'],
  ['windows-1252', 'Windows-1252'],
]
const encodingName = (e: string) => encodings.find(([id]) => id === e)?.[1] ?? e

export function indentName(i: Indent | null) {
  const tabs = i ? i.tabs : !settings.editor.insertSpaces
  return tabs ? t('Tab') : tn(i ? i.size : settings.editor.tabSize, '{n} space', '{n} spaces')
}

/** Converts the file: the new format is written to the disk with the text. */
async function convert(d: Doc, f: Partial<FileFormat>) {
  const before = d.format()
  d.setFormat({ ...before, ...f })
  if (!(await saveDoc(d))) d.setFormat(before)
}

function menuAt(e: MouseEvent, items: MenuItem[]) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  contextMenu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.top - 4 }), items)
}

export function StatusBar() {
  const doc = createMemo(() => {
    docsVersion()
    const tab = activeTab()
    return tab?.kind === 'file' && tab.path ? getDoc(tab.path) : null
  })
  return (
    <div class="status-bar" data-testid="status-bar">
      <span class="grow" />
      <Show when={cursorInfo()}>
        {(c) => (
          <span class="sb-item" data-testid="status-cursor">
            {c().line}:{c().col}
            {c().sel ? ` (${tn(c().sel, '{n} char', '{n} chars')})` : ''}
          </span>
        )}
      </Show>
      <Show when={doc()}>
        {(d) => (
          <>
            <button
              class="sb-item"
              data-testid="status-eol"
              title={t('Line separator')}
              onClick={(e) =>
                menuAt(e, [
                  { label: 'LF (Unix, macOS)', checked: d().format().eol === 'lf', disabled: d().readOnly, action: () => convert(d(), { eol: 'lf' }) },
                  { label: 'CRLF (Windows)', checked: d().format().eol === 'crlf', disabled: d().readOnly, action: () => convert(d(), { eol: 'crlf' }) },
                ])
              }
            >
              {d().format().eol.toUpperCase()}
            </button>
            <button
              class="sb-item"
              data-testid="status-encoding"
              title={t('File encoding')}
              onClick={(e) =>
                menuAt(
                  e,
                  encodings.map(([id, name]) => ({ label: name, checked: d().format().encoding === id, disabled: d().readOnly, action: () => convert(d(), { encoding: id }) })),
                )
              }
            >
              {encodingName(d().format().encoding)}
            </button>
            <button
              class="sb-item"
              data-testid="status-indent"
              title={t('Indentation')}
              onClick={(e) => {
                const cur = d().indent()
                const is = (tabs: boolean, size = 0) => !!cur && cur.tabs === tabs && (tabs || cur.size === size)
                menuAt(e, [
                  { label: t('Tab'), checked: is(true), action: () => d().chooseIndent({ tabs: true, size: 4 }) },
                  ...[2, 4, 8].map((n) => ({ label: tn(n, '{n} space', '{n} spaces'), checked: is(false, n), action: () => d().chooseIndent({ tabs: false, size: n }) })),
                ])
              }}
            >
              {indentName(d().indent())}
            </button>
          </>
        )}
      </Show>
      <Show when={cursorInfo()}>{(c) => <span class="sb-item">{c().lang}</span>}</Show>
    </div>
  )
}
