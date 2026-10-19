// The items of the status bar for a file tab in focus: the caret position (a click opens "Go to
// line"), the format of the file (line separator, encoding, indentation) and its language. A
// format item's menu converts the file (saved at once) or chooses its indentation.
import type { Doc, MenuItem, TabState } from '../api'
import type { StatusItem } from '../types'
import { cursor, docsVersion, editorSettings, getDoc, runAction, saveDoc, shortcutOf, t, tn } from './plugin'

type FileFormat = ReturnType<Doc['format']>
type Indent = ReturnType<Doc['indent']>

const encodings: [string, string][] = [
  ['utf-8', 'UTF-8'],
  ['utf-8-bom', 'UTF-8 BOM'],
  ['utf-16le', 'UTF-16 LE'],
  ['utf-16be', 'UTF-16 BE'],
  ['windows-1252', 'Windows-1252'],
]
const encodingName = (e: string) => encodings.find(([id]) => id === e)?.[1] ?? e

function indentName(i: Indent) {
  const tabs = i ? i.tabs : !editorSettings().insertSpaces
  return tabs ? t('Tab') : tn(i ? i.size : editorSettings().tabSize, '{n} space', '{n} spaces')
}

/** Converts the file: the new format is written to the disk with the text. */
async function convert(d: Doc, f: Partial<FileFormat>) {
  const before = d.format()
  d.setFormat({ ...before, ...f })
  if (!(await saveDoc(d))) d.setFormat(before)
}

function indentMenu(d: Doc): MenuItem[] {
  const cur = d.indent()
  const is = (tabs: boolean, size = 0) => !!cur && cur.tabs === tabs && (tabs || cur.size === size)
  return [
    { label: t('Tab'), checked: is(true), action: () => d.chooseIndent({ tabs: true, size: 4 }) },
    ...[2, 4, 8].map((n) => ({ label: tn(n, '{n} space', '{n} spaces'), checked: is(false, n), action: () => d.chooseIndent({ tabs: false, size: n }) })),
  ]
}

export function fileStatus(tab: TabState): StatusItem[] {
  docsVersion()
  const d = tab.path ? getDoc(tab.path) : undefined
  if (!d) return []
  const c = cursor()
  const items: StatusItem[] = []
  if (c)
    items.push({
      id: 'cursor',
      testid: 'status-cursor',
      text: `${c.line}:${c.col}${c.sel ? ` (${tn(c.sel, '{n} char', '{n} chars')})` : ''}`,
      title: `${t('Go to line…')} (${shortcutOf('nav.gotoLine')})`,
      action: () => runAction('nav.gotoLine'),
    })
  items.push(
    {
      id: 'eol',
      testid: 'status-eol',
      text: d.format().eol.toUpperCase(),
      title: t('Line separator'),
      menu: () => [
        { label: 'LF (Unix, macOS)', checked: d.format().eol === 'lf', disabled: d.readOnly, action: () => convert(d, { eol: 'lf' }) },
        { label: 'CRLF (Windows)', checked: d.format().eol === 'crlf', disabled: d.readOnly, action: () => convert(d, { eol: 'crlf' }) },
      ],
    },
    {
      id: 'encoding',
      testid: 'status-encoding',
      text: encodingName(d.format().encoding),
      title: t('File encoding'),
      menu: () => encodings.map(([id, name]) => ({ label: name, checked: d.format().encoding === id, disabled: d.readOnly, action: () => convert(d, { encoding: id }) })),
    },
    { id: 'indent', testid: 'status-indent', text: indentName(d.indent()), title: t('Indentation'), menu: () => indentMenu(d) },
  )
  if (c) items.push({ id: 'language', text: c.lang })
  return items
}
