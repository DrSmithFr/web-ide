// Logs of a container or of the stack: the last 500 lines, then followed live. Text filter,
// service filter (stack), timestamps, ANSI colors; scrolling up pauses the following.
import { createEffect, createMemo, createSignal, For, on, onCleanup, Show } from 'solid-js'
import { on as onPod, request } from '../pod/rpc'
import { Icon } from '../ui/icons'
import { t } from '../i18n'
import { parseAnsi, stripAnsi } from './ansi'
import { profiles } from './state'

const maxLines = 10000

interface Line {
  n: number
  service: string
  time: string
  text: string
  plain: string
}

// docker logs --timestamps: "2026-10-04T17:15:57.747006538Z text"; compose adds "web-1  | ".
const prefix = /^(?:(\S+)\s+\| )?(?:(\d{4}-\d\d-\d\dT[\d:.]+(?:Z|[+-]\d\d:\d\d)) ?)?/

function parse(raw: string, n: number): Line {
  const m = prefix.exec(raw)!
  const text = raw.slice(m[0].length)
  return { n, service: m[1] ?? '', time: m[2] ?? '', text, plain: stripAnsi(text).toLowerCase() }
}

/** Short local time of a docker timestamp (date shown when it is not today). */
function shortTime(ts: string) {
  const d = new Date(ts)
  if (isNaN(+d)) return ts
  const time = d.toLocaleTimeString(undefined, { hour12: false })
  return d.toDateString() === new Date().toDateString() ? time : `${d.toLocaleDateString()} ${time}`
}

export function Logs(props: { id: string; services?: string[]; running: boolean }) {
  const [lines, setLines] = createSignal<Line[]>([])
  const [ended, setEnded] = createSignal(false)
  const [error, setError] = createSignal('')
  const [query, setQuery] = createSignal('')
  const [service, setService] = createSignal('')
  const [times, setTimes] = createSignal(false)
  const [follow, setFollow] = createSignal(true)
  let stream = ''
  let seq = 0
  let box!: HTMLDivElement

  const stop = () => {
    if (stream) request('docker.logsStop', { stream }).catch(() => {})
    stream = ''
  }
  const start = async () => {
    stop()
    setLines([])
    setEnded(false)
    setError('')
    const key = props.id
    try {
      const r = await request<{ stream: string }>('docker.logs', { id: key, profiles: key ? [] : profiles(), tail: 500 })
      if (key !== props.id) return void request('docker.logsStop', { stream: r.stream }).catch(() => {})
      stream = r.stream
    } catch (e) {
      setError((e as Error).message)
    }
  }
  const offLog = onPod('docker.log', (e: { stream: string; lines: string[] }) => {
    if (e.stream !== stream) return
    setLines((l) => {
      const next = l.concat(e.lines.map((x) => parse(x, ++seq)))
      return next.length > maxLines ? next.slice(next.length - maxLines) : next
    })
  })
  const offEnd = onPod('docker.logEnd', (e: { stream: string }) => {
    if (e.stream === stream) {
      stream = ''
      setEnded(true)
    }
  })
  onCleanup(() => {
    offLog()
    offEnd()
    stop()
  })
  createEffect(on(() => props.id, () => void start()))
  // A stopped container started again: follow it again.
  createEffect(on(() => props.running, (run, was) => run && !was && ended() && void start(), { defer: true }))

  const shown = createMemo(() => {
    const q = query().toLowerCase()
    const svc = service()
    return lines().filter((l) => (!svc || l.service === svc || l.service.replace(/-\d+$/, '') === svc) && (!q || l.plain.includes(q)))
  })
  createEffect(
    on(shown, () => {
      if (follow()) queueMicrotask(() => box && (box.scrollTop = box.scrollHeight))
    }),
  )
  const onScroll = () => setFollow(box.scrollHeight - box.scrollTop - box.clientHeight < 24)
  const toEnd = () => {
    setFollow(true)
    box.scrollTop = box.scrollHeight
  }

  return (
    <div class="dk-logs">
      <div class="dk-bar">
        <input class="input small dk-filter" placeholder={t('Filter')} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} data-testid="docker-log-filter" />
        <Show when={props.services?.length}>
          <select class="small" value={service()} onChange={(e) => setService(e.currentTarget.value)} data-testid="docker-log-service">
            <option value="">{t('All services')}</option>
            <For each={props.services}>{(s) => <option value={s}>{s}</option>}</For>
          </select>
        </Show>
        <button class="toggle" classList={{ on: times() }} title={t('Show timestamps')} onClick={() => setTimes(!times())} data-testid="docker-log-times">
          ⏱
        </button>
        <span class="grow" />
        <span class="muted small">{shown().length < lines().length ? t('{n} of {total} lines', { n: shown().length, total: lines().length }) : ''}</span>
        <button class="icon-btn small" title={t('Clear')} onClick={() => setLines([])}>
          <Icon name="close" size={12} />
        </button>
        <button class="icon-btn small" title={t('Reload the logs')} onClick={() => start()}>
          <Icon name="refresh" size={12} />
        </button>
      </div>
      <div class="dk-log-lines" ref={box} onScroll={onScroll} data-testid="docker-logs">
        <Show when={error()}>
          <div class="danger">{error()}</div>
        </Show>
        <For each={shown()}>
          {(l) => (
            <div class="dk-log-line">
              <Show when={times() && l.time}>
                <span class="dk-log-time">{shortTime(l.time)}</span>
              </Show>
              <Show when={l.service && !service()}>
                <span class="dk-log-svc">{l.service}</span>
              </Show>
              <For each={parseAnsi(l.text)}>{(s) => (s.style ? <span style={s.style}>{s.text}</span> : s.text)}</For>
            </div>
          )}
        </For>
        <Show when={ended()}>
          <div class="muted dk-log-end">{t('[end of the logs: no container running]')}</div>
        </Show>
      </div>
      <Show when={!follow()}>
        <button class="btn small dk-follow" onClick={toEnd}>
          {t('Following paused: go to the end')}
        </button>
      </Show>
    </div>
  )
}
