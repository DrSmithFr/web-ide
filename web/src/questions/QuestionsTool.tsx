// Questions tool: the list of what waits for the user, and the modal that shows one question
// at a time with the last message of its asker.
import { createMemo, createSignal, For, Match, Show, Switch } from 'solid-js'
import { Modal } from '../ui/overlay'
import { EmptyState } from '../ui/EmptyState'
import { errorToast } from '../ui/toast'
import { Icon } from '../ui/icons'
import { Markdown } from '../ui/Markdown'
import { Switch as Toggle } from '../ui/Switch'
import { AskCard, type AskMessage } from './AskCard'
import { settings, updateSettings } from '../state/settings'
import { t, tn } from '../i18n'
import { QuestionForm } from './Form'
import { answerAsk, answerForm, answerPermission, putOff, questions, showQuestion, shown, skipAsk, type Question } from './state'
import './questions.css'

/** The question in one line (the list, the head of the modal). */
function summary(q: Question): string {
  switch (q.kind) {
    case 'permission':
      return t('Allow: {title}?', { title: q.text || t('Tool') })
    case 'form':
      return q.text ?? ''
    default:
      return (q.questions?.length ?? 0) > 1 ? `${q.questions![0].question} (${tn(q.questions!.length, '{n} question', '{n} questions')})` : (q.questions?.[0]?.question ?? '')
  }
}

function ago(ms: number): string {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000))
  if (s < 60) return t('just now')
  if (s < 3600) return t('{n} min ago', { n: Math.round(s / 60) })
  return t('{n} h ago', { n: Math.round(s / 3600) })
}

export function QuestionsTool() {
  return (
    <div class="panel">
      <div class="panel-head">
        <span class="panel-title">{t('Questions')}</span>
      </div>
      <div class="panel-body q-list" data-testid="questions-tool">
        <Toggle
          label={t('Open questions automatically')}
          hint={t('A new question of an AI opens in front of everything.')}
          checked={settings.questions.autoOpen}
          onChange={(v) => updateSettings((s) => (s.questions.autoOpen = v), 'Open questions automatically')}
          testid="questions-auto"
        />
        <For each={questions()} fallback={<EmptyState icon="check" text={t('No question waits for you.')} />}>
          {(q) => (
            <button class="q-item" classList={{ active: shown() === q.id }} onClick={() => showQuestion(q.id)} data-testid="question-item" data-source={q.source}>
              <span class="q-item-head">
                <Icon name={q.kind === 'permission' ? 'lock' : 'comment'} size={13} />
                <span class="q-who">{q.name}</span>
                <span class="muted small grow q-title">{q.title !== q.name ? q.title : ''}</span>
                <span class="muted small">{ago(q.time)}</span>
              </span>
              <span class="q-text">{summary(q)}</span>
            </button>
          )}
        </For>
      </div>
    </div>
  )
}

/** The modal of the question shown (mounted once per window). */
export function QuestionModal() {
  const q = createMemo(() => questions().find((x) => x.id === shown()))
  return (
    <Show when={q()} keyed>
      {(q) => <QuestionDialog q={q} />}
    </Show>
  )
}

function QuestionDialog(props: { q: Question }) {
  const q = () => props.q
  const [busy, setBusy] = createSignal(false)
  const [openLast, setOpenLast] = createSignal(false)
  const position = () => questions().findIndex((x) => x.id === q().id) + 1
  const run = async (f: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await f()
    } catch (e) {
      errorToast(e)
    } finally {
      setBusy(false)
    }
  }
  const longLast = () => (q().last?.length ?? 0) > 600
  // ask_user: the card of the assistant, on a message made for it.
  const msg = (): AskMessage => ({ questions: q().questions, askState: 'pending' })
  return (
    <Modal
      title={t('{name} asks', { name: q().name })}
      class="q-modal"
      onClose={putOff}
      footer={
        <>
          <span class="muted small grow">{t('Question {i} of {n}', { i: position(), n: questions().length })}</span>
          <Show when={q().source === 'mcp'}>
            <button class="btn" disabled={busy()} onClick={() => void run(() => skipAsk(q()))} data-testid="question-skip">
              {t('Skip')}
            </button>
          </Show>
          <button class="btn" onClick={putOff} data-testid="question-later">
            {t('Later')}
          </button>
        </>
      }
    >
      <div class="q-dialog" data-testid="question-modal" data-id={q().id} data-kind={q().kind}>
        <Show when={q().title !== q().name}>
          <div class="muted small q-where">{q().title}</div>
        </Show>
        <Show when={q().last}>
          <div class="q-last" classList={{ folded: longLast() && !openLast() }} data-testid="question-last">
            <Markdown text={q().last!} final={true} />
          </div>
          <Show when={longLast()}>
            <button class="link small" onClick={() => setOpenLast(!openLast())}>
              {openLast() ? t('Show less') : t('Show more')}
            </button>
          </Show>
        </Show>
        <Switch>
          <Match when={q().kind === 'permission'}>
            <div class="q-ask">{summary(q())}</div>
            <div class="q-actions">
              <For each={q().options ?? []}>
                {(o) => (
                  <button
                    class="btn"
                    classList={{ primary: o.kind === 'allow_once', danger: o.kind.startsWith('reject') }}
                    disabled={busy()}
                    onClick={() => void run(() => answerPermission(q(), o.optionId))}
                    data-testid={`question-option-${o.optionId}`}
                  >
                    {o.name}
                  </button>
                )}
              </For>
            </div>
          </Match>
          <Match when={q().kind === 'form'}>
            <div class="q-ask">{q().text}</div>
            <QuestionForm schema={q().schema ?? {}} idPrefix={q().id} busy={busy()} onAnswer={(action, content) => void run(() => answerForm(q(), action, content))} />
          </Match>
          <Match when={q().kind === 'ask'}>
            <AskCard msg={msg()} index={q().index} scope={q().id} onSend={(_, answers, notes, path, off) => void run(() => answerAsk(q(), answers, notes, path, off))} />
          </Match>
        </Switch>
      </div>
    </Modal>
  )
}
