// Statistics of the conversation (with its sub-agents) or of the project: speeds, time spent
// generating, thinking and in the tools, failures of the tools, the context and its
// compactions. Computed by the pod (agent.stats); followed live during an answer.
import { createEffect, createResource, createSignal, For, on, onCleanup, Show } from 'solid-js'
import { request } from '../pod/rpc'
import { t } from '../i18n'
import { chat, currentModel, live } from './state'
import { formatTokens } from './parts'

interface Speed {
  tokens: number
  ms: number
  perSecond: number
}
interface Tool {
  name: string
  calls: number
  ms: number
  waitMs: number
  failures?: Record<string, number>
}
interface Part {
  kind: 'system' | 'user' | 'summary' | 'assistant' | 'tool'
  name?: string
  tokens: number
}
interface Point {
  at?: number
  tokens: number
  compaction?: boolean
}
interface Gap {
  at?: number
  ms?: number
  steps: number
  peak: number
  tokens: number
}
export interface StatsData {
  conversations: number
  answers: number
  first?: number
  last?: number
  read: Speed
  write: Speed
  prompt: number
  cached: number
  generationMs: number
  thinkMs: number
  reasoningTokens: number
  efforts: { effort: string; answers: number; thinkMs: number; medianMs: number }[] | null
  tools: Tool[] | null
  toolCalls: number
  toolMs: number
  failures?: Record<string, number>
  withTools: number
  multi: number
  repeats: number
  context?: {
    tokens: number
    parts: Part[] | null
    curve: Point[]
    compactions: Gap[]
  }
}
interface ChatResult {
  stats: StatsData
  children: { id: string; title: string; stats: StatsData }[] | null
}
interface ProjectResult {
  stats: StatsData
  models: string[]
  efforts: string[]
}

const failureKinds = ['usage', 'exit', 'error', 'denied'] as const
const failureLabels: Record<string, () => string> = {
  usage: () => t('Misuse'),
  exit: () => t('Exit code'),
  error: () => t('Error'),
  denied: () => t('Refused'),
}
const effortLabels: Record<string, () => string> = {
  xhigh: () => t('Max'),
  medium: () => t('Medium'),
  low: () => t('Low'),
  '': () => t('Not recorded'),
}
const periods = [
  { id: 'all', label: () => t('All'), ms: 0 },
  { id: 'day', label: () => t('24 hours'), ms: 86_400_000 },
  { id: 'week', label: () => t('7 days'), ms: 7 * 86_400_000 },
  { id: 'month', label: () => t('30 days'), ms: 30 * 86_400_000 },
]

const failureText = (r?: Record<string, number>) =>
  failureKinds
    .filter((k) => r?.[k])
    .map((k) => `${failureLabels[k]()} ${r![k]}`)
    .join(' · ')
const pct = (n: number, of: number) => (of > 0 ? `${Math.round((n / of) * 100)} %` : '–')
/** A duration as a clock: 34:51, or 3:34:51 from an hour; under a second, <0:01. */
export function clock(v: number) {
  if (!(v > 0)) return '–'
  if (v < 1000) return '<0:01'
  const total = Math.round(v / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const sec = String(total % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`
}
const ms = clock
const speed = (s: Speed) => (s.perSecond > 0 ? `${s.perSecond.toFixed(1)} t/s` : '–')
const sum = (r?: Record<string, number>) => Object.values(r ?? {}).reduce((a, b) => a + b, 0)

export function Stats() {
  const [scope, setScope] = createSignal<'chat' | 'project'>('chat')
  const [model, setModel] = createSignal('')
  const [effort, setEffort] = createSignal('')
  const [period, setPeriod] = createSignal('all')

  const params = () =>
    scope() === 'chat'
      ? chat.id && chat.messages.length
        ? { id: chat.id }
        : null
      : {
          project: true,
          model: model(),
          effort: effort(),
          from: periods.find((p) => p.id === period())!.ms ? Date.now() - periods.find((p) => p.id === period())!.ms : 0,
        }
  const [data, { refetch }] = createResource(params, (p) => request<ChatResult & ProjectResult>('agent.stats', p))

  // Live: the conversation shown is followed at most every 1.5 s while it changes.
  let timer: ReturnType<typeof setTimeout> | undefined
  createEffect(
    on(
      () => [chat.messages.length, live.busy, live.tool] as const,
      () => {
        if (scope() !== 'chat' || timer) return
        timer = setTimeout(() => {
          timer = undefined
          refetch()
        }, 1500)
      },
      { defer: true },
    ),
  )
  onCleanup(() => clearTimeout(timer))

  return (
    <div class="st-view" data-testid="st-view">
      <div class="st-head">
        <div class="segmented" role="radiogroup">
          <button type="button" role="radio" aria-checked={scope() === 'chat'} classList={{ on: scope() === 'chat' }} onClick={() => setScope('chat')} data-testid="st-scope-chat">
            {t('This conversation')}
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={scope() === 'project'}
            classList={{ on: scope() === 'project' }}
            onClick={() => setScope('project')}
            data-testid="st-scope-project"
          >
            {t('Whole project')}
          </button>
        </div>
        <Show when={scope() === 'project'}>
          <div class="st-filters">
            <select value={model()} onChange={(e) => setModel(e.currentTarget.value)} title={t('Model')}>
              <option value="">{t('All the models')}</option>
              <For each={data()?.models ?? []}>{(m) => <option value={m}>{m}</option>}</For>
            </select>
            <select value={effort()} onChange={(e) => setEffort(e.currentTarget.value)} title={t('Reasoning effort')} data-testid="st-effort">
              <option value="">{t('All the efforts')}</option>
              <For each={data()?.efforts ?? []}>{(e) => <option value={e}>{effortLabels[e]?.() ?? e}</option>}</For>
            </select>
            <select value={period()} onChange={(e) => setPeriod(e.currentTarget.value)} title={t('Period')}>
              <For each={periods}>{(p) => <option value={p.id}>{p.label()}</option>}</For>
            </select>
          </div>
        </Show>
      </div>
      <Show when={data.error}>
        <p class="st-empty">{String(data.error?.message ?? data.error)}</p>
      </Show>
      <Show
        when={data()?.stats}
        keyed
        fallback={
          <Show when={!data.loading}>
            <p class="st-empty">{t('Nothing to measure yet.')}</p>
          </Show>
        }
      >
        {(s) => (
          <Show when={s.answers > 0} fallback={<p class="st-empty">{t('Nothing to measure yet.')}</p>}>
            <Body s={s} children={scope() === 'chat' ? (data()?.children ?? []) : []} project={scope() === 'project'} />
          </Show>
        )}
      </Show>
    </div>
  )
}

function Tile(props: { label: string; value: string; hint?: string; testid?: string }) {
  return (
    <div class="st-tile" title={props.hint} data-testid={props.testid}>
      <span class="st-tile-value">{props.value}</span>
      <span class="st-tile-label">{props.label}</span>
    </div>
  )
}

function Body(props: { s: StatsData; children: ChatResult['children'] & {}; project: boolean }) {
  const s = () => props.s
  const failures = () => sum(s().failures)
  return (
    <>
      <Show when={props.project}>
        <p class="st-note">
          {t('{n} conversations, {a} answers', {
            n: s().conversations,
            a: s().answers,
          })}
        </p>
      </Show>
      <Show when={!props.project && props.children.length}>
        <p class="st-note">
          {t('Including {n} sub-agents (detail below).', {
            n: props.children.length,
          })}
        </p>
      </Show>
      <div class="st-tiles">
        <Tile
          label={t('Reading')}
          value={speed(s().read)}
          hint={t('{n} tokens read outside the cache', {
            n: formatTokens(s().read.tokens),
          })}
          testid="st-read"
        />
        <Tile
          label={t('Writing')}
          value={speed(s().write)}
          hint={t('{n} tokens generated', {
            n: formatTokens(s().write.tokens),
          })}
          testid="st-write"
        />
        <Tile label={t('Generation')} value={ms(s().generationMs)} hint={t('{n} answers', { n: s().answers })} testid="st-generation" />
        <Tile
          label={t('Thinking')}
          value={ms(s().thinkMs)}
          hint={t('{p} of the generation time', {
            p: pct(s().thinkMs, s().generationMs),
          })}
        />
        <Tile label={t('Thinking tokens')} value={pct(s().reasoningTokens, s().write.tokens)} hint={t('Estimated share of the generated tokens')} />
        <Tile label={t('Cache hit')} value={pct(s().cached, s().prompt)} hint={t('Share of the prompts read from the cache')} />
        <Tile
          label={t('Tools')}
          value={ms(s().toolMs)}
          hint={t('{n} calls, without the approval waits', {
            n: s().toolCalls,
          })}
        />
        <Tile label={t('Tool failures')} value={pct(failures(), s().toolCalls)} hint={t('{n} of {m} calls', { n: failures(), m: s().toolCalls })} testid="st-failures" />
        <Tile label={t('Tool chains')} value={pct(s().multi, s().withTools)} hint={t('Answers calling several tools at once, among those calling tools')} testid="st-multi" />
        <Tile label={t('Repeated calls')} value={String(s().repeats)} hint={t('Calls identical to one made since the last message of the user')} />
      </div>

      <Show when={s().efforts?.length}>
        <h4>{t('Reasoning effort')}</h4>
        <table class="st-table">
          <thead>
            <tr>
              <th>{t('Effort')}</th>
              <th>{t('Answers')}</th>
              <th>{t('Thinking')}</th>
              <th>{t('Mean')}</th>
              <th>{t('Median')}</th>
            </tr>
          </thead>
          <tbody>
            <For each={s().efforts}>
              {(e) => (
                <tr>
                  <td>{effortLabels[e.effort]?.() ?? e.effort}</td>
                  <td>{e.answers}</td>
                  <td>{ms(e.thinkMs)}</td>
                  <td>{ms(e.thinkMs / e.answers)}</td>
                  <td>{ms(e.medianMs)}</td>
                </tr>
              )}
            </For>
          </tbody>
        </table>
      </Show>

      <Show when={s().tools?.length}>
        <h4>{t('Tools')}</h4>
        <Show when={failures()}>
          <p class="st-note" data-testid="st-failure-kinds">
            {t('Failures: {list}', { list: failureText(s().failures) })}
          </p>
        </Show>
        <table class="st-table" data-testid="st-tools">
          <thead>
            <tr>
              <th>{t('Tool')}</th>
              <th>{t('Calls')}</th>
              <th>{t('Time')}</th>
              <th>{t('Mean')}</th>
              <th title={t('Waiting for your approval')}>{t('Wait')}</th>
              <th>{t('Failures')}</th>
            </tr>
          </thead>
          <tbody>
            <For each={s().tools}>
              {(tool) => (
                <tr>
                  <td class="mono">{tool.name}</td>
                  <td>{tool.calls}</td>
                  <td>{ms(tool.ms)}</td>
                  <td>{ms(tool.ms / tool.calls)}</td>
                  <td>{ms(tool.waitMs)}</td>
                  <td classList={{ 'st-bad': !!sum(tool.failures) }} title={failureText(tool.failures)}>
                    {sum(tool.failures) || ''}
                  </td>
                </tr>
              )}
            </For>
          </tbody>
        </table>
      </Show>

      <Show when={s().context}>{(c) => <ContextView c={c()} />}</Show>

      <Show when={props.children.length}>
        <h4>{t('Sub-agents')}</h4>
        <table class="st-table">
          <thead>
            <tr>
              <th>{t('Conversation')}</th>
              <th>{t('Answers')}</th>
              <th>{t('Generation')}</th>
              <th>{t('Thinking')}</th>
              <th>{t('Tool failures')}</th>
            </tr>
          </thead>
          <tbody>
            <For each={props.children}>
              {(k) => (
                <tr>
                  <td class="ellipsis" title={k.title}>
                    {k.title}
                  </td>
                  <td>{k.stats.answers}</td>
                  <td>{ms(k.stats.generationMs)}</td>
                  <td>{ms(k.stats.thinkMs)}</td>
                  <td>{pct(sum(k.stats.failures), k.stats.toolCalls)}</td>
                </tr>
              )}
            </For>
          </tbody>
        </table>
      </Show>
    </>
  )
}

const partLabel = (p: Part) =>
  p.kind === 'tool'
    ? p.name!
    : (
        {
          system: t('System prompt and tools'),
          user: t('Your messages'),
          summary: t('Summaries'),
          assistant: t('Answers and calls'),
        } as Record<string, string>
      )[p.kind]

function ContextView(props: { c: NonNullable<StatsData['context']> }) {
  const size = () => currentModel()?.context ?? 0
  const top = () => Math.max(1, ...(props.c.parts ?? []).map((p) => p.tokens))
  return (
    <>
      <h4>{t('Context')}</h4>
      <p class="st-note" data-testid="st-context">
        {size()
          ? t('{n} tokens of {m} ({p})', {
              n: formatTokens(props.c.tokens),
              m: formatTokens(size()),
              p: pct(props.c.tokens, size()),
            })
          : t('{n} tokens', { n: formatTokens(props.c.tokens) })}
        {' · '}
        {t('the earlier reasoning is not sent back to the model; the parts are estimated.')}
      </p>
      <div class="st-bars">
        <For each={props.c.parts ?? []}>
          {(p) => (
            <div class="st-bar-row" title={`${partLabel(p)} · ${formatTokens(p.tokens)} · ${pct(p.tokens, props.c.tokens)}`}>
              <span class="st-bar-label ellipsis" classList={{ mono: p.kind === 'tool' }}>
                {partLabel(p)}
              </span>
              <span class="st-bar-track">
                <span class="st-bar" style={{ width: `${(p.tokens / top()) * 100}%` }} />
              </span>
              <span class="st-bar-value">{pct(p.tokens, props.c.tokens)}</span>
            </div>
          )}
        </For>
      </div>
      <Show when={props.c.curve.filter((p) => !p.compaction).length > 1}>
        <Curve points={props.c.curve} size={size()} />
      </Show>
      <Show when={props.c.compactions.length}>
        <h4>{t('Compactions')}</h4>
        <table class="st-table" data-testid="st-compactions">
          <thead>
            <tr>
              <th>#</th>
              <th>{t('After')}</th>
              <th>{t('Steps')}</th>
              <th>{t('Peak of the context')}</th>
              <th>{t('Generated')}</th>
            </tr>
          </thead>
          <tbody>
            <For each={props.c.compactions}>
              {(g, i) => (
                <tr>
                  <td>{i() + 1}</td>
                  <td>{ms(g.ms ?? 0)}</td>
                  <td>{g.steps}</td>
                  <td>{formatTokens(g.peak)}</td>
                  <td>{formatTokens(g.tokens)}</td>
                </tr>
              )}
            </For>
          </tbody>
        </table>
      </Show>
    </>
  )
}

/** The prompt of each answer, the compactions as dashed lines; the hovered step is told. */
function Curve(props: { points: Point[]; size: number }) {
  const W = 320
  const H = 110
  const pad = 4
  const [hover, setHover] = createSignal<number | null>(null)
  const steps = () => props.points
  const top = () => Math.max(props.size, ...steps().map((p) => p.tokens), 1)
  const x = (i: number) => pad + (i / Math.max(1, steps().length - 1)) * (W - 2 * pad)
  const y = (v: number) => H - pad - (v / top()) * (H - 2 * pad)
  const path = () => {
    let d = ''
    let pen = false
    steps().forEach((p, i) => {
      if (p.compaction) return void (pen = false)
      d += `${pen ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.tokens).toFixed(1)}`
      pen = true
    })
    return d
  }
  return (
    <figure class="st-curve" data-testid="st-curve">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        onPointerMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect()
          const i = Math.round((((e.clientX - r.left) / r.width) * (W - 2 * pad)) / ((W - 2 * pad) / Math.max(1, steps().length - 1)))
          setHover(Math.min(steps().length - 1, Math.max(0, i)))
        }}
        onPointerLeave={() => setHover(null)}
      >
        <Show when={props.size}>
          <line class="st-curve-limit" x1={pad} x2={W - pad} y1={y(props.size)} y2={y(props.size)} />
        </Show>
        <For each={steps()}>
          {(p, i) => (
            <Show when={p.compaction}>
              <line class="st-curve-compaction" x1={x(i())} x2={x(i())} y1={pad} y2={H - pad} />
            </Show>
          )}
        </For>
        <path class="st-curve-line" d={path()} />
        <Show when={hover() !== null && !steps()[hover()!].compaction}>
          <circle class="st-curve-dot" cx={x(hover()!)} cy={y(steps()[hover()!].tokens)} r="3" />
        </Show>
      </svg>
      <figcaption>
        <Show when={hover() !== null} fallback={t('Context at each answer; the dashed lines are the compactions.')}>
          {steps()[hover()!].compaction
            ? t('Compaction')
            : t('Answer {n}: {m} tokens', {
                n: steps()
                  .slice(0, hover()! + 1)
                  .filter((p) => !p.compaction).length,
                m: formatTokens(steps()[hover()!].tokens),
              })}
        </Show>
      </figcaption>
    </figure>
  )
}
