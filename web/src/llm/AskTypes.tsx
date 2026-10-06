// The widgets of ask_user, one per question type: choice (cards), idea (a proposal, four
// answers, swipe on touch), compare (two approaches side by side), rank (order, or the top
// N), scenario (a concrete situation, then choices). The current answer, the free answer
// and the note live in AskCard (one signal per question index); the widgets are stateless
// and recreated on every step change, so all they do is render the shared accessors and
// report the answer through setAnswer(i, entry).
import { For, Show } from 'solid-js'
import { Icon } from '../ui/icons'
import { t } from '../i18n'
import type { Question, QuestionOption } from './state'
import type { AnswerEntry } from './ask'

/**
 * props of a widget: the question, its index, and the shared state of AskCard. The
 * shared arrays (sel, order, ideaPick, ideaText) are accessors so the widgets follow the
 * signals even though they are recreated on every step change.
 */
export interface AskBodyProps {
  q: Question
  i: number
  setAnswer: (i: number, e: AnswerEntry | null) => void
  /** selected labels, per question (choice / compare / scenario). */
  sel: () => string[][]
  /** current order, per question (rank). */
  order: () => string[][]
  /** the "idea" answer picked, per question ('No' | 'Yes' | 'Exactly' | 'Yes, but…' | ''). */
  ideaPick: () => string[]
  /** the "Yes, but…" text, per question (idea). */
  ideaText: () => string[]
  setIdeaText: (i: number, v: string) => void
  setOrder: (i: number, labels: string[]) => void
}

// ---------- the options list (choice and scenario) ----------

/** A choice card: the label, the description, then the pros (✓) and the cons (✗). */
function OptionCard(props: { o: QuestionOption; on: boolean; multiple?: boolean; onClick: () => void }) {
  return (
    <button class="ai-ask-option" classList={{ on: props.on }} data-testid="ai-ask-option" onClick={props.onClick}>
      <span class={props.multiple ? 'ai-ask-box' : 'ai-ask-radio'} />
      <span class="ai-ask-option-body">
        <strong>{props.o.label}</strong>
        <Show when={props.o.description}>
          <span class="muted small"> — {props.o.description}</span>
        </Show>
        <Show when={(props.o.pros?.length ?? 0) + (props.o.cons?.length ?? 0) > 0}>
          <span class="ai-ask-pc">
            <For each={props.o.pros}>
              {(p) => (
                <span class="ai-ask-pro">
                  <Icon name="check" size={11} /> {p}
                </span>
              )}
            </For>
            <For each={props.o.cons}>
              {(c) => (
                <span class="ai-ask-con">
                  <Icon name="close" size={11} /> {c}
                </span>
              )}
            </For>
          </span>
        </Show>
      </span>
    </button>
  )
}

/** The list of options of a question (choice, scenario). */
function OptionsList(props: AskBodyProps & { multiple?: boolean }) {
  const q = props.q
  const cur = () => props.sel()[props.i] ?? []
  const toggle = (label: string) => {
    const c = cur()
    const next = props.multiple ? (c.includes(label) ? c.filter((x) => x !== label) : [...c, label]) : c.includes(label) ? [] : [label]
    props.setAnswer(props.i, next.length ? { kind: 'choices', value: next } : null)
  }
  return (
    <div class="ai-ask-options">
      <For each={q.options}>
        {(o) => <OptionCard o={o} on={cur().includes(o.label)} multiple={props.multiple} onClick={() => toggle(o.label)} />}
      </For>
    </div>
  )
}

/** "choice" (and the default, without a type): the list of cards. */
export function AskChoice(props: AskBodyProps) {
  return <OptionsList {...props} multiple={props.q.multiple} />
}

/** "scenario": the concrete situation, then the choices. */
export function AskScenario(props: AskBodyProps) {
  return (
    <div class="ai-ask-scenario">
      <Show when={props.q.situation}>
        <div class="ai-ask-situation" data-testid="ai-ask-situation">
          <Icon name="info" size={14} />
          <div>
            <strong>{t('Situation')}</strong>
            <p>{props.q.situation}</p>
          </div>
        </div>
      </Show>
      <OptionsList {...props} />
    </div>
  )
}

/**
 * "compare": two approaches side by side (stacked on a narrow screen), each with its pros
 * and cons, a "VS" between them. Picking one excludes the other.
 */
export function AskCompare(props: AskBodyProps) {
  const q = props.q
  const pick = (label: string) => {
    const cur = props.sel()[props.i] ?? []
    props.setAnswer(props.i, cur.includes(label) ? null : { kind: 'choices', value: [label] })
  }
  const sel = () => props.sel()[props.i] ?? []
  return (
    <div class="ai-ask-compare" data-testid="ai-ask-compare">
      <OptionCard o={q.options[0] ?? { label: '—' }} on={sel()[0] === q.options[0]?.label} onClick={() => q.options[0] && pick(q.options[0].label)} />
      <span class="ai-ask-vs">{t('VS')}</span>
      <OptionCard o={q.options[1] ?? { label: '—' }} on={sel()[0] === q.options[1]?.label} onClick={() => q.options[1] && pick(q.options[1].label)} />
    </div>
  )
}

/**
 * "idea": one proposal, four answers (No, Yes but…, Yes, Exactly), and on touch a swipe:
 * left = No, right = Yes. "Yes, but…" opens the free text of the question.
 */
export function AskIdea(props: AskBodyProps) {
  const q = props.q
  const pick = (v: 'No' | 'Yes' | 'Exactly' | 'Yes, but…') => {
    const value = v === 'Yes, but…' ? `Yes, but: ${props.ideaText()[props.i].trim()}` : v
    props.setAnswer(props.i, { kind: 'idea', value: value as any })
    if (v === 'Yes, but…') requestAnimationFrame(() => ta?.focus())
  }
  const on = (v: string) => props.ideaPick()[props.i] === v
  let ta: HTMLTextAreaElement | undefined
  // Swipe: the card follows the finger, horizontal moves only (the thread scrolls on
  // pan-y), past 80px it decides; a plain tap does not count.
  let card: HTMLElement | null = null
  let sx = 0
  let sy = 0
  let dx = 0
  let dragging = false
  return (
    <div
      class="ai-ask-idea"
      data-testid="ai-ask-idea"
      tabindex={0}
      onKeyDown={(e) => {
        if (e.key === 'ArrowLeft') pick('No')
        else if (e.key === 'ArrowRight') pick('Yes')
      }}
      onPointerDown={(e) => {
        if (e.button !== 0 || (e.target as HTMLElement).closest('button, textarea, input, a')) return
        card = e.currentTarget.querySelector('.ai-ask-idea-card')
        sx = e.clientX
        sy = e.clientY
        dx = 0
        dragging = true
        try {
          e.currentTarget.setPointerCapture(e.pointerId)
        } catch {
          /* synthetic event (tests): the moves come on the element anyway */
        }
      }}
      onPointerMove={(e) => {
        if (!dragging || !card) return
        dx = e.clientX - sx
        const dy = e.clientY - sy
        if (Math.abs(dy) > Math.abs(dx) * 1.5) {
          dragging = false
          card.style.transform = ''
          return
        }
        card.style.transform = `translateX(${dx}px) rotate(${dx / 25}deg)`
        card.style.opacity = String(Math.max(0.4, 1 - Math.abs(dx) / 400))
      }}
      onPointerUp={() => {
        if (!dragging) return
        dragging = false
        if (card) {
          card.style.transform = ''
          card.style.opacity = ''
        }
        if (dx < -80) pick('No')
        else if (dx > 80) pick('Yes')
      }}
      onPointerCancel={() => {
        dragging = false
        if (card) {
          card.style.transform = ''
          card.style.opacity = ''
        }
      }}
    >
      <div class="ai-ask-idea-card">
        <Show when={q.header}>
          <span class="badge">{q.header}</span>
        </Show>
        <p class="ai-ask-idea-proposal">{q.question}</p>
        <div class="ai-ask-idea-actions">
          <button class="btn" classList={{ on: on('No') }} onClick={() => pick('No')} data-testid="ai-ask-idea-no">
            <Icon name="close" size={14} /> {t('No')}
          </button>
          <button class="btn" classList={{ on: on('Yes, but…') }} onClick={() => pick('Yes, but…')} data-testid="ai-ask-idea-but">
            <Icon name="edit" size={14} /> {t('Yes, but…')}
          </button>
          <button class="btn" classList={{ on: on('Yes') }} onClick={() => pick('Yes')} data-testid="ai-ask-idea-yes">
            <Icon name="check" size={14} /> {t('Yes')}
          </button>
          <button class="btn" classList={{ on: on('Exactly') }} onClick={() => pick('Exactly')} data-testid="ai-ask-idea-exactly">
            <Icon name="sparkle" size={14} /> {t('Exactly')}
          </button>
        </div>
        <Show when={on('Yes, but…') || (props.ideaText()[props.i] ?? '').trim()}>
          <textarea
            ref={(e) => {
              ta = e
              e.style.height = 'auto'
              e.style.height = `${Math.min(e.scrollHeight, 160)}px`
            }}
            class="input ai-ask-idea-text-input"
            placeholder={t('What to change, keep or add…')}
            value={props.ideaText()[props.i]}
            onInput={(e) => {
              const el = e.currentTarget
              props.setIdeaText(props.i, el.value)
              el.style.height = 'auto'
              el.style.height = `${Math.min(el.scrollHeight, 160)}px`
              props.setAnswer(props.i, { kind: 'idea', value: `Yes, but: ${el.value}` as any })
            }}
            data-testid="ai-ask-idea-text"
            rows={2}
          />
        </Show>
      </div>
    </div>
  )
}

/**
 * "rank": order the items (▲/▼, keyboard and mobile friendly, no drag). With `top`,
 * only the top N are reported and get numbered badges; the rest keep their place.
 */
export function AskRank(props: AskBodyProps) {
  const q = props.q
  const top = q.top ?? 0
  const order = () => props.order()[props.i] ?? q.options.map((o) => o.label)
  const move = (label: string, dir: -1 | 1) => {
    const o = [...order()]
    const at = o.indexOf(label)
    const j = at + dir
    if (j < 0 || j >= o.length) return
    ;[o[at], o[j]] = [o[j], o[at]]
    props.setOrder(props.i, o)
    props.setAnswer(props.i, { kind: 'rank', value: [...o], top: top || undefined })
  }
  return (
    <div class="ai-ask-rank" data-testid="ai-ask-rank">
      <Show when={top}>
        <p class="muted small">{t('Order the items: only the top {n} are reported.', { n: top })}</p>
      </Show>
      <ol class="ai-ask-rank-list">
        <For each={order()}>
          {(label) => {
            // The item keeps its row when the order changes: its place is read again.
            const pos = () => order().indexOf(label)
            const inTop = () => (top ? pos() < top : false)
            return (
              <li class="ai-ask-rank-item" classList={{ picked: inTop() }} data-testid="ai-ask-rank-item">
                <span class="ai-ask-rank-pos">
                  <Show when={inTop()} fallback={<span class="ai-ask-rank-num">{pos() + 1}</span>}>
                    <span class="badge ok">{pos() + 1}</span>
                  </Show>
                </span>
                <span class="ai-ask-rank-label">{label}</span>
                <span class="ai-ask-rank-btns">
                  <button class="btn small" disabled={pos() === 0} title={t('Move up')} onClick={() => move(label, -1)} data-testid="ai-ask-rank-up">
                    <Icon name="up" size={13} />
                  </button>
                  <button class="btn small" disabled={pos() === order().length - 1} title={t('Move down')} onClick={() => move(label, 1)} data-testid="ai-ask-rank-down">
                    <Icon name="down" size={13} />
                  </button>
                </span>
              </li>
            )
          }}
        </For>
      </ol>
    </div>
  )
}

/** The body of the current question: the widget of its type. */
export function AskBody(props: AskBodyProps & { type?: string }) {
  switch (props.type) {
    case 'idea':
      return <AskIdea {...props} />
    case 'compare':
      return <AskCompare {...props} />
    case 'rank':
      return <AskRank {...props} />
    case 'scenario':
      return <AskScenario {...props} />
    default:
      return <AskChoice {...props} />
  }
}
