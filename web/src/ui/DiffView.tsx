// Side-by-side diff of a file (tab of the editor): working tree against the index
// ("changes"), or the index against HEAD ("staged"). The working side follows the
// open buffer while it is edited.
import { createEffect, createMemo, createResource, createSignal, For, Match, on, Show, Switch } from 'solid-js'
import { request } from '../pod/rpc'
import { docsVersion, getDoc, openFile, relPath, setActivePane, type TabState } from '../state/project'
import { gitRevision, gitStatus, refreshGit } from '../state/git'
import { lineHunks } from '../editor/linediff'
import { errorToast } from './toast'
import { t, tn } from '../i18n'

const CONTEXT = 3

type Row =
  | { kind: 'same'; a: number; b: number; text: string }
  | { kind: 'change'; hunk: number; a: number | null; b: number | null; left: string | null; right: string | null }
  | { kind: 'fold'; id: number; count: number }

export function DiffView(props: { tab: TabState; paneId: string }) {
  const path = props.tab.path!
  const staged = !!props.tab.staged
  const [expanded, setExpanded] = createSignal<Set<number>>(new Set())
  const [current, setCurrent] = createSignal(0)
  let body!: HTMLDivElement

  // The working side: the buffer when the file is open, else the file on disk.
  const workText = createMemo(() => {
    docsVersion()
    const d = getDoc(path)
    if (!d) return null
    d.changed()
    return d.text
  })
  const [texts] = createResource(
    () => ({ rev: gitRevision(), work: staged ? null : workText() }),
    async ({ work }) => {
      const old = await request('git.show', { path, rev: staged ? 'HEAD' : '' })
      let neu: string
      if (staged) neu = (await request('git.show', { path, rev: '' })).content
      else if (work !== null) neu = work
      else {
        try {
          neu = (await request('fs.read', { path })).content ?? ''
        } catch {
          neu = ''
        }
      }
      return { old: old.content as string, neu }
    },
  )

  const diff = createMemo(() => {
    const t = texts()
    if (!t) return null
    const a = t.old.split('\n')
    const b = t.neu.split('\n')
    const hunks = lineHunks(a, b)
    if (!hunks) return { tooBig: true as const, rows: [] as Row[], hunks: 0, added: 0, removed: 0 }
    const rows: Row[] = []
    let ia = 0
    let ib = 0
    let fold = 0
    let added = 0
    let removed = 0
    const same = (count: number, first: boolean, last: boolean) => {
      // Unchanged lines: only the context around the changes is shown.
      const keepHead = first ? 0 : CONTEXT
      const keepTail = last ? 0 : CONTEXT
      const id = fold++
      if (count > keepHead + keepTail + 2 && !expanded().has(id)) {
        for (let k = 0; k < keepHead; k++) rows.push({ kind: 'same', a: ia + k, b: ib + k, text: a[ia + k] })
        rows.push({ kind: 'fold', id, count: count - keepHead - keepTail })
        for (let k = count - keepTail; k < count; k++) rows.push({ kind: 'same', a: ia + k, b: ib + k, text: a[ia + k] })
      } else for (let k = 0; k < count; k++) rows.push({ kind: 'same', a: ia + k, b: ib + k, text: a[ia + k] })
      ia += count
      ib += count
    }
    hunks.forEach((h, n) => {
      same(h.a[0] - ia, n === 0, false)
      const len = Math.max(h.a[1], h.b[1])
      for (let k = 0; k < len; k++) {
        const hasA = k < h.a[1]
        const hasB = k < h.b[1]
        rows.push({ kind: 'change', hunk: n, a: hasA ? h.a[0] + k : null, b: hasB ? h.b[0] + k : null, left: hasA ? a[h.a[0] + k] : null, right: hasB ? b[h.b[0] + k] : null })
      }
      removed += h.a[1]
      added += h.b[1]
      ia = h.a[0] + h.a[1]
      ib = h.b[0] + h.b[1]
    })
    same(a.length - ia, hunks.length === 0, true)
    return { tooBig: false as const, rows, hunks: hunks.length, added, removed }
  })

  const go = (dir: 1 | -1) => {
    const d = diff()
    if (!d?.hunks) return
    const next = (current() + dir + d.hunks) % d.hunks
    setCurrent(next)
    body.querySelector(`[data-hunk="${next}"]`)?.scrollIntoView({ block: 'center' })
  }
  createEffect(on(() => diff()?.hunks, () => setCurrent(0), { defer: true }))

  const isStagedNow = () => gitStatus()?.files.some((f) => f.path === path && f.index !== '.' && !f.untracked)

  const toggleStage = async () => {
    try {
      await request(staged ? 'git.unstage' : 'git.stage', { paths: [path] })
    } catch (e) {
      errorToast(e)
    }
    refreshGit(0)
  }

  return (
    <div class="diff-view" onMouseDown={() => setActivePane(props.paneId)}>
      <div class="toolbar">
        <span class="small">
          {relPath(path)} · {staged ? t('index ↔ HEAD') : t('working tree ↔ index')}
        </span>
        <Show when={diff() && !diff()!.tooBig}>
          <span class="diff-stats">
            <span class="ok">+{diff()!.added}</span> <span class="danger">−{diff()!.removed}</span>
          </span>
        </Show>
        <span class="grow" />
        <button class="icon-btn" title={t('Previous change')} disabled={!diff()?.hunks} onClick={() => go(-1)}>
          ↑
        </button>
        <button class="icon-btn" title={t('Next change')} disabled={!diff()?.hunks} onClick={() => go(1)}>
          ↓
        </button>
        <button class="btn small" onClick={() => openFile(path)}>
          {t('Open the file')}
        </button>
        <button class="btn small" onClick={toggleStage}>
          {staged ? t('Unstage') : isStagedNow() ? t('Stage again') : t('Stage')}
        </button>
      </div>
      <div class="diff-body" ref={body}>
        <Switch>
          <Match when={texts.error}>
            <p class="danger pad">{String(texts.error?.message ?? texts.error)}</p>
          </Match>
          <Match when={!diff()}>
            <p class="muted pad">{t('Loading…')}</p>
          </Match>
          <Match when={diff()!.tooBig}>
            <p class="muted pad">{t('Differences too large to show line by line.')}</p>
          </Match>
          <Match when={!diff()!.hunks}>
            <p class="muted pad">{t('No difference.')}</p>
          </Match>
          <Match when={true}>
            <table class="diff">
              <tbody>
                <For each={diff()!.rows}>
                  {(r) =>
                    r.kind === 'fold' ? (
                      <tr class="diff-fold" onClick={() => setExpanded(new Set([...expanded(), r.id]))}>
                        <td colspan="4">⋯ {tn(r.count, '{n} identical line', '{n} identical lines')}</td>
                      </tr>
                    ) : r.kind === 'same' ? (
                      <tr>
                        <td class="diff-no">{r.a + 1}</td>
                        <td class="diff-line">{r.text}</td>
                        <td class="diff-no">{r.b + 1}</td>
                        <td class="diff-line">{r.text}</td>
                      </tr>
                    ) : (
                      <tr data-hunk={r.hunk} classList={{ current: r.hunk === current() }}>
                        <td class="diff-no">{r.a !== null ? r.a + 1 : ''}</td>
                        <td class="diff-line" classList={{ del: r.left !== null, empty: r.left === null }}>
                          {r.left ?? ''}
                        </td>
                        <td class="diff-no">{r.b !== null ? r.b + 1 : ''}</td>
                        <td class="diff-line" classList={{ add: r.right !== null, empty: r.right === null }}>
                          {r.right ?? ''}
                        </td>
                      </tr>
                    )
                  }
                </For>
              </tbody>
            </table>
          </Match>
        </Switch>
      </div>
    </div>
  )
}
