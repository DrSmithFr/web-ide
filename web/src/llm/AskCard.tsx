// AskCard: the ask_user card — one question at a time (the widget of its type), the
// quick answers "I don't know" / "Up to you", the free answer, an optional note, then a
// recap before sending. The answers go back with the tool result (type, answer, note);
// the card stays (folded) in the thread and shows them.
import { For, Show, createSignal } from 'solid-js'
import { Icon } from '../ui/icons'
import { t, tn } from '../i18n'
import type { ChatMessage, Question } from './state'
import type { AnswerEntry } from './ask'
import { answerLabel, DONT_KNOW, UP_TO_YOU } from './ask'
import { AskBody } from './AskTypes'

/** The answer of a sent question, as the user reads it (the stored words are English). */
function showAnswer(q: Question, s: string): string {
  if (q.type === 'idea') {
    if (s === 'No') return t('No')
    if (s === 'Yes') return t('Yes')
    if (s === 'Exactly') return t('Exactly')
    if (s.startsWith('Yes, but: ')) return `${t('Yes, but…')} ${s.slice('Yes, but: '.length)}`
  }
  if (s === DONT_KNOW) return t("I don't know")
  if (s === UP_TO_YOU) return t('Up to you')
  return s
}

export function AskCard(props: { msg: ChatMessage; index: number; onSend: (index: number, answers: (AnswerEntry | null)[][], notes: string[]) => void }) {
  // The message is read through props: the pod replaces it when the answers come back.
  const msg = () => props.msg
  const questions: Question[] = props.msg.questions ?? []
  const n = questions.length
  const pending = () => msg().askState === 'pending'
  const [step, setStep] = createSignal(0)
  // One state per question index: the widget answer, the special answer, the free answer
  // and the note. Kept here (not in the widgets) so a step change recreates the widget
  // without losing the answers.
  const [ans, setAns] = createSignal<(AnswerEntry | null)[]>(questions.map(() => null))
  const [special, setSpecial] = createSignal<(null | 'dontknow' | 'uptoyou')[]>(questions.map(() => null))
  const [free, setFree] = createSignal<string[]>(questions.map(() => ''))
  const [notes, setNotes] = createSignal<string[]>(questions.map(() => ''))
  const [ideaText, setIdeaText] = createSignal<string[]>(questions.map(() => ''))
  const [orders, setOrders] = createSignal<string[][]>(questions.map((q) => q.options.map((o) => o.label)))
  const [noteOpen, setNoteOpen] = createSignal(false)
  const cur = () => questions[step()]
  const curType = () => cur()?.type ?? 'choice'

  /** The answer of a question as sent back (the special one wins, then the widget, then
   *  the free text; rank is always its current order). */
  const entryAt = (i: number): AnswerEntry | null => {
    const q = questions[i]
    const sp = special()[i]
    if (sp) return { kind: sp }
    if ((q.type ?? 'choice') === 'rank') {
      // The order shown is an answer even untouched: Next accepts it.
      return { kind: 'rank', value: orders()[i], top: q.top || undefined }
    }
    const e = ans()[i]
    if (e) return e
    const f = free()[i]?.trim()
    return f ? { kind: 'free', value: f } : null
  }
  // A single-answer question (choice, idea, compare, scenario) moves on by itself; a
  // multiple choice and a rank keep the user (Next advances). "Yes, but…" is typed, stays.
  const setAnswer = (i: number, e: AnswerEntry | null) => {
    setAns((a) => a.map((x, k) => (k === i ? e : x)))
    if (e) setSpecial((s) => s.map((x, k) => (k === i ? null : x)))
    const q = questions[i]
    const type = q.type ?? 'choice'
    const single = type === 'rank' ? false : type === 'choice' ? !q.multiple : true
    if (e && single && i === step() && step() < n - 1 && !(e.kind === 'idea' && e.value.startsWith('Yes, but: '))) requestAnimationFrame(() => setStep(step() + 1))
  }
  const setSpecialAt = (i: number, v: 'dontknow' | 'uptoyou') => setSpecial((s) => s.map((x, k) => (k === i ? (s[i] === v ? null : v) : x)))
  const dispSel = () =>
    questions.map((q, k) => {
      const type = q.type ?? 'choice'
      if (special()[k] || (free()[k] ?? '').trim() || type === 'idea' || type === 'rank') return []
      const e = ans()[k]
      return e?.kind === 'choices' ? e.value : []
    })
  const ideaPick = () =>
    questions.map((q, k) => {
      if (q.type !== 'idea' || special()[k]) return ''
      const e = ans()[k]
      if (e?.kind !== 'idea') return ''
      if (e.value === 'No' || e.value === 'Yes' || e.value === 'Exactly') return e.value
      if (e.value.startsWith('Yes, but: ')) return 'Yes, but…'
      return ''
    })
  const ideaVal = () =>
    questions.map((q, k) => {
      if (q.type !== 'idea') return ''
      const e = ans()[k]
      return e?.kind === 'idea' && e.value.startsWith('Yes, but: ') ? e.value.slice('Yes, but: '.length) : ideaText()[k] ?? ''
    })
  const setIdeaVal = (i: number, v: string) => {
    setIdeaText((x) => x.map((y, k) => (k === i ? v : y)))
    setAns((a) => a.map((y, k) => (k === i ? { kind: 'idea', value: `Yes, but: ${v}` } : y)))
  }
  const setOrder = (i: number, labels: string[]) => setOrders((o) => o.map((x, k) => (k === i ? labels : x)))
  const onSend = () => props.onSend(props.index, questions.map((_, k) => [entryAt(k)]), notes().map((x) => x.trim()))
  const next = () => step() < n - 1 && setStep(step() + 1)
  const body = (k: number) => (
    <AskBody type={cur()?.type} q={questions[k]} i={k} setAnswer={setAnswer} sel={dispSel} order={orders} ideaPick={ideaPick} ideaText={ideaVal} setIdeaText={setIdeaVal} setOrder={setOrder} />
  )
  const recapLi = (k: () => number) => {
    const q = questions[k()]
    const e = () => entryAt(k())
    const note = () => (notes()[k()] ?? '').trim()
    return (
      <li>
        <button class="link" onClick={() => setStep(k())}>
          {q.question}
        </button>
        <strong data-testid="ai-ask-recap-answer">{e() ? answerLabel(e()) : '—'}</strong>
        <Show when={note()}>
          <span class="muted small" data-testid="ai-ask-recap-note">
            {t('Note')}: {note()}
          </span>
        </Show>
      </li>
    )
  }

  return (
    <div class="ai-ask" classList={{ done: !pending() }} data-testid="ai-ask">
      <div class="ai-ask-head">
        <Icon name="info" size={14} />
        <strong>{n > 1 ? tn(n, '{n} question', '{n} questions', { n }) : t('Question')}</strong>
        <span class="grow" />
        <Show when={pending() && step() < n && n > 1}>
          <span class="muted small">
            {step() + 1} / {n}
          </span>
        </Show>
        <Show when={msg().askState === 'answered'}>
          <span class="badge ok">{t('answered')}</span>
        </Show>
        <Show when={msg().askState === 'skipped'}>
          <span class="badge">{t('not answered')}</span>
        </Show>
      </div>

      <Show
        when={pending()}
        fallback={
          <ol class="ai-ask-recap">
            <For each={questions}>
              {(q, k) => {
                const s = (msg().answers?.[k()] ?? []).join(' ; ')
                const note = (msg().notes?.[k()] ?? '').trim()
                return (
                  <li>
                    <span>{q.question}</span>
                    <strong>{s ? showAnswer(q, s) : '—'}</strong>
                    <Show when={note}>
                      <span class="muted small">
                        {t('Note')}: {note}
                      </span>
                    </Show>
                  </li>
                )
              }}
            </For>
          </ol>
        }
      >
        <Show
          when={step() < n}
          fallback={
            <>
              <ol class="ai-ask-recap">
                <For each={questions}>{(_q, k) => recapLi(k)}</For>
              </ol>
              <div class="ai-plan-foot">
                <span class="grow" />
                <button class="btn primary" data-testid="ai-ask-send" onClick={onSend}>
                  {t('Send the answers')}
                </button>
              </div>
            </>
          }
        >
          <div class="ai-ask-q" data-testid="ai-ask-question">
            <Show when={cur()?.header}>
              <span class="badge">{cur()?.header}</span>
            </Show>
            <p class="ai-ask-text">{cur()?.question}</p>
            {body(step())}
            <Show when={curType() !== 'idea'}>
              <input
                class="input"
                data-testid="ai-ask-free"
                placeholder={t('Other answer…')}
                value={free()[step()] ?? ''}
                onInput={(e) => setFree((f) => f.map((x, k) => (k === step() ? e.currentTarget.value : x)))}
                onKeyDown={(e) => e.key === 'Enter' && (entryAt(step()) !== null || curType() === 'rank') && (step() === n - 1 ? setStep(n) : next())}
              />
            </Show>
            <div class="ai-ask-quick">
              <button class="btn small" classList={{ on: special()[step()] === 'dontknow' }} onClick={() => setSpecialAt(step(), 'dontknow')} data-testid="ai-ask-dontknow">
                {t("I don't know")}
              </button>
              <button class="btn small" classList={{ on: special()[step()] === 'uptoyou' }} onClick={() => setSpecialAt(step(), 'uptoyou')} data-testid="ai-ask-uptoyou">
                {t('Up to you')}
              </button>
            </div>
            <div class="ai-ask-note">
              <button class="link" data-testid="ai-ask-note" onClick={() => setNoteOpen(!noteOpen())}>
                {noteOpen() ? `− ${t('Note')}` : `+ ${t('Add a note')}`}
              </button>
              <Show when={noteOpen()}>
                <textarea
                  class="input ai-ask-note-input"
                  placeholder={t('A note for the assistant (optional)…')}
                  value={notes()[step()] ?? ''}
                  onInput={(e) => setNotes((f) => f.map((x, k) => (k === step() ? e.currentTarget.value : x)))}
                  rows={2}
                  data-testid="ai-ask-note-input"
                />
              </Show>
            </div>
          </div>
          <div class="ai-plan-foot">
            <Show when={step() > 0}>
              <button class="btn" onClick={() => setStep(step() - 1)}>
                {t('Previous')}
              </button>
            </Show>
            <span class="grow" />
            <Show
              when={n > 1}
              fallback={
                <button class="btn primary" data-testid="ai-ask-send" disabled={entryAt(step()) === null} onClick={onSend}>
                  {t('Answer')}
                </button>
              }
            >
              <button class="btn primary" data-testid="ai-ask-next" onClick={() => (step() === n - 1 ? setStep(n) : next())}>
                {step() === n - 1 ? t('Summary') : t('Next')}
              </button>
            </Show>
          </div>
        </Show>
      </Show>
    </div>
  )
}
