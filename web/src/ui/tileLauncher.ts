// Launcher of tiles (Ctrl+Alt+Enter, + of a Grid desk): a file, a tool or a console opens in a
// new tile beside the focused one (state/tiles.ts).
import { capability } from '../plugins/capabilities'
import { searchEverywhere } from '../popups/SearchEverywhere'
import { leaves, session } from '../state/project'
import { dropEmptyTile, focusPane, openEmptyTile, openTile, tileTools } from '../state/tiles'
import { shortcutOf } from '../keys/bindings'
import { pick } from './overlay'
import { t } from '../i18n'

type Choice = { kind: 'file' } | { kind: 'tool'; id: string } | { kind: 'console'; id: string } | { kind: 'terminal' }

export async function tileLauncher(anchor?: { left: number; top: number }) {
  const c = await pick<Choice>({
    placeholder: t('Open in a new tile'),
    anchor,
    items: [
      { label: t('File…'), hint: shortcutOf('nav.gotoFile'), value: { kind: 'file' } },
      ...(capability('terminal') ? [{ label: t('New terminal'), value: { kind: 'terminal' } as Choice }] : []),
      ...(capability('terminal')?.consoles() ?? []).map((x) => ({ label: x.title, detail: t('console'), value: { kind: 'console', id: x.id } as Choice })),
      ...Object.entries(tileTools).map(([id, x]) => ({ label: t(x.label), detail: t('tool'), value: { kind: 'tool', id } as Choice })),
    ],
  })
  if (!c) return
  if (c.kind === 'tool') openTile({ kind: 'tool', tool: c.id })
  else if (c.kind === 'console') openTile({ kind: 'console', console: c.id })
  else if (c.kind === 'terminal') {
    const info = await capability('terminal')?.open({ show: false })
    if (info) openTile({ kind: 'console', console: info.id })
  } else {
    // The file chosen opens in the new tile (the active pane); none chosen: the tile goes.
    const pane = openEmptyTile()
    // After the list gave the focus back, else it takes it from the popup.
    requestAnimationFrame(() => searchEverywhere('files', { onClose: () => leaves().find((l) => l.id === pane && !l.tabs.length) && dropEmptyTile(pane) }))
    return
  }
  focusPane(session.activePane)
}
