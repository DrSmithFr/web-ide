// The form of a question asked with elicitation/create (a flat JSON schema): a choice
// (oneOf / enum), several choices (an array of anyOf / enum items), a yes/no, a number or a
// text per field. Claude's AskUserQuestion comes this way: a choice per question, then its
// "Other" text field.
import { createSignal, For, Match, Show, Switch } from 'solid-js'
import { t } from '../i18n'
import type { FormField, FormSchema } from './state'

type Choice = { const: string; title?: string; description?: string }

const choicesOf = (f: FormField): Choice[] | null => f.oneOf ?? f.enum?.map((v) => ({ const: v })) ?? null
const itemsOf = (f: FormField): Choice[] | null => f.items?.anyOf ?? f.items?.enum?.map((v) => ({ const: v })) ?? null

export function QuestionForm(props: {
  schema: FormSchema
  /** Prefix of the ids of the inputs (several forms on a page). */
  idPrefix: string
  busy?: boolean
  onAnswer: (action: 'accept' | 'decline' | 'cancel', content?: Record<string, unknown>) => void
}) {
  const [values, setValues] = createSignal<Record<string, unknown>>({})
  const fields = () => Object.entries(props.schema.properties ?? {})
  const set = (k: string, v: unknown) => setValues((x) => ({ ...x, [k]: v }))
  const missing = () => (props.schema.required ?? []).some((k) => {
    const v = values()[k]
    return v === undefined || v === '' || (Array.isArray(v) && !v.length)
  })
  const submit = () => {
    // Empty fields are left out: nothing chosen.
    const content: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(values())) if (v !== '' && v !== undefined && !(Array.isArray(v) && !v.length)) content[k] = v
    props.onAnswer('accept', content)
  }
  return (
    <form
      class="q-form"
      data-testid="question-form"
      onSubmit={(e) => {
        e.preventDefault()
        if (!missing()) submit()
      }}
    >
      <For each={fields()}>
        {([k, f]) => {
          const id = `${props.idPrefix}-${k}`
          const required = (props.schema.required ?? []).includes(k)
          return (
            <fieldset class="q-field" data-field={k}>
              <legend>
                {f.title ?? k}
                <Show when={required}>
                  <span class="danger"> *</span>
                </Show>
              </legend>
              <Show when={f.description}>
                <div class="muted small">{f.description}</div>
              </Show>
              <Switch
                fallback={
                  <input
                    id={id}
                    type="text"
                    value={(values()[k] as string) ?? ''}
                    onInput={(e) => set(k, e.currentTarget.value)}
                    placeholder={k.endsWith('_custom') ? t('Your own answer (optional)') : ''}
                  />
                }
              >
                <Match when={f.type === 'array' && itemsOf(f)}>
                  {(items) => (
                    <For each={items()}>
                      {(c) => (
                        <label class="q-choice">
                          <input
                            type="checkbox"
                            name={id}
                            checked={((values()[k] as string[]) ?? []).includes(c.const)}
                            onChange={(e) => {
                              const cur = ((values()[k] as string[]) ?? []).filter((x) => x !== c.const)
                              set(k, e.currentTarget.checked ? [...cur, c.const] : cur)
                            }}
                          />
                          <span>
                            {c.title ?? c.const}
                            <Show when={c.description}>
                              <span class="muted small"> — {c.description}</span>
                            </Show>
                          </span>
                        </label>
                      )}
                    </For>
                  )}
                </Match>
                <Match when={choicesOf(f)}>
                  {(choices) => (
                    <For each={choices()}>
                      {(c) => (
                        <label class="q-choice">
                          <input type="radio" name={id} checked={values()[k] === c.const} onChange={() => set(k, c.const)} />
                          <span>
                            {c.title ?? c.const}
                            <Show when={c.description}>
                              <span class="muted small"> — {c.description}</span>
                            </Show>
                          </span>
                        </label>
                      )}
                    </For>
                  )}
                </Match>
                <Match when={f.type === 'boolean'}>
                  <label class="q-choice">
                    <input type="checkbox" checked={values()[k] === true} onChange={(e) => set(k, e.currentTarget.checked)} />
                    <span>{t('Yes')}</span>
                  </label>
                </Match>
                <Match when={f.type === 'integer' || f.type === 'number'}>
                  <input
                    id={id}
                    type="number"
                    min={f.minimum}
                    max={f.maximum}
                    step={f.type === 'integer' ? 1 : 'any'}
                    value={(values()[k] as number) ?? ''}
                    onInput={(e) => set(k, e.currentTarget.value === '' ? undefined : Number(e.currentTarget.value))}
                  />
                </Match>
              </Switch>
            </fieldset>
          )
        }}
      </For>
      <div class="q-actions">
        <button type="submit" class="btn primary" disabled={props.busy || missing()} data-testid="question-submit">
          {t('Send')}
        </button>
        <button type="button" class="btn" disabled={props.busy} onClick={() => props.onAnswer('decline')} data-testid="question-decline" title={t('The AI goes on without an answer')}>
          {t('Skip')}
        </button>
        <button type="button" class="btn danger" disabled={props.busy} onClick={() => props.onAnswer('cancel')} data-testid="question-cancel" title={t('The AI stops what it was doing')}>
          {t('Cancel')}
        </button>
      </div>
    </form>
  )
}

/** The answer sent for a form, as a short text (field: value). */
export function formAnswerText(schema: FormSchema | undefined, answer: Record<string, unknown> | undefined): string {
  if (!answer) return ''
  return Object.entries(answer)
    .map(([k, v]) => `${schema?.properties?.[k]?.title ?? k}: ${Array.isArray(v) ? v.join(', ') : String(v)}`)
    .join(' · ')
}
