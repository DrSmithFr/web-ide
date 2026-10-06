// The board of the conversation: its pages (the doodles sent, later the drawings of the
// assistant), one shown large with zoom and pan, the others as a strip of thumbnails. Pages
// never change: Reuse opens an editable copy in the doodle modal, sent as a new doodle.
import { createMemo, For, Show } from 'solid-js'
import { Icon } from '../../ui/icons'
import { t } from '../../i18n'
import { newDoodle, reuseDoodle } from '../Composer'
import { doodleSession } from '../doodle/session'
import { toSVG } from '../doodle/render'
import { DoodleView } from './DoodleView'
import { pages, selectedPage, selectPage, showBoard, type Page } from './pages'
import './board.css'

function Thumb(props: { page: Page; on: boolean }) {
  // The stored preview of a doodle, else the page drawn small.
  const src = createMemo(() => {
    if (props.page.thumb) return props.page.thumb
    const f = props.page.doc.frame
    return 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(toSVG(props.page.doc, 96, Math.round((96 * f.h) / f.w)))
  })
  return (
    <button class="bd-thumb" classList={{ on: props.on }} title={`${t('Page {n}', { n: props.page.number })} · ${props.page.name}`} onClick={() => selectPage(props.page.key)} data-testid="bd-thumb">
      <img src={src()} alt="" />
      <span>{props.page.number}</span>
    </button>
  )
}

export function Board(props: { closable: boolean }) {
  let fit = () => {}
  const page = () => selectedPage()
  const move = (d: number) => {
    const list = pages()
    const i = list.findIndex((p) => p.key === page()?.key)
    const next = list[i + d]
    if (next) selectPage(next.key)
  }
  return (
    <div
      class="bd-board"
      tabindex={0}
      data-testid="bd-board"
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return
        if (e.key === 'ArrowLeft') move(-1)
        else if (e.key === 'ArrowRight') move(1)
      }}
    >
      <Show
        when={page()}
        fallback={
          <div class="bd-empty" data-testid="bd-empty">
            <Icon name="pen" size={22} />
            <p>{t('Doodles of this conversation and drawings of the assistant appear here.')}</p>
            <button class="btn" disabled={!!doodleSession()} onClick={() => newDoodle()}>
              {t('New doodle')}
            </button>
          </div>
        }
      >
        {(p) => (
          <>
            <div class="bd-head">
              <strong class="ellipsis" data-testid="bd-title">
                {t('Page {n}', { n: p().number })} · {p().name}
              </strong>
              <span class="muted small bd-by">{p().from === 'user' ? t('drawn by you') : t('drawn by the assistant')}</span>
              <span class="grow" />
              <button class="btn small" title={t('Opens an editable copy, joined to the next message')} disabled={!!doodleSession()} onClick={() => reuseDoodle({ name: p().name, kind: 'doodle', size: 0, doodle: p().doc })} data-testid="bd-reuse">
                <Icon name="edit" size={13} /> {t('Reuse')}
              </button>
              <button class="btn small" onClick={() => fit()} data-testid="bd-fit">
                {t('Fit')}
              </button>
              <Show when={props.closable}>
                <button class="icon-btn" title={t('Close the board')} onClick={() => showBoard(false)}>
                  <Icon name="close" size={14} />
                </button>
              </Show>
            </div>
            <div class="bd-stage">
              <DoodleView doc={p().doc} ref={(api) => (fit = api.fit)} />
            </div>
            <Show when={p().description}>
              <details class="bd-desc">
                <summary>{t('Description sent to the model')}</summary>
                <pre>{p().description}</pre>
              </details>
            </Show>
            <div class="bd-strip" data-testid="bd-strip">
              <For each={pages()}>{(x) => <Thumb page={x} on={x.key === p().key} />}</For>
            </div>
          </>
        )}
      </Show>
    </div>
  )
}
