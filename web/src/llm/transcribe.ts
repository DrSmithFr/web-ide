// Speech to text in the browser (Whisper in a worker). Recording, decoding and recognition
// all happen in the page: the audio is never sent anywhere. The model files are downloaded
// once through the pod (cache in ~/.web-ide/models).
import { createStore } from 'solid-js/store'
// By path: the package exports do not list these files.
import ortMjs from '../../node_modules/onnxruntime-web/dist/ort-wasm-simd-threaded.asyncify.mjs?url'
import ortWasm from '../../node_modules/onnxruntime-web/dist/ort-wasm-simd-threaded.asyncify.wasm?url'
import { prefs } from './state'
import { t } from '../i18n'

export interface WhisperModel {
  id: string
  label: string
  repo: string
  /** Download size per device. */
  /** Approximate download size in MB (0: not available). */
  size: { webgpu: number; wasm: number }
  dtype: { webgpu: any; wasm: any }
  /** Needs WebGPU with 16-bit floats (too slow otherwise). */
  webgpuOnly?: boolean
}

export const whisperModels: WhisperModel[] = [
  { id: 'tiny', label: 'Tiny (fast, rough)', repo: 'onnx-community/whisper-tiny', size: { webgpu: 41, wasm: 41 }, dtype: { webgpu: 'q8', wasm: 'q8' } },
  { id: 'base', label: 'Base', repo: 'onnx-community/whisper-base', size: { webgpu: 77, wasm: 77 }, dtype: { webgpu: 'q8', wasm: 'q8' } },
  { id: 'small', label: 'Small (better, slower)', repo: 'onnx-community/whisper-small', size: { webgpu: 250, wasm: 250 }, dtype: { webgpu: 'q8', wasm: 'q8' } },
  {
    id: 'turbo',
    label: 'Large v3 Turbo (the best, WebGPU)',
    repo: 'onnx-community/whisper-large-v3-turbo',
    size: { webgpu: 560, wasm: 0 },
    dtype: { webgpu: { encoder_model: 'q4f16', decoder_model_merged: 'q4f16' }, wasm: null },
    webgpuOnly: true,
  },
]

export const languages: [string, string][] = [
  ['auto', 'Automatic detection'],
  ['fr', 'French'],
  ['en', 'English'],
  ['es', 'Spanish'],
  ['de', 'German'],
  ['it', 'Italian'],
  ['pt', 'Portuguese'],
]

export const [speech, setSpeech] = createStore({
  phase: 'idle' as 'idle' | 'recording' | 'loading' | 'transcribing',
  loaded: 0,
  total: 0,
  startedAt: 0,
  device: '' as '' | 'webgpu' | 'wasm',
  f16: false,
})

let gpuProbe: Promise<{ webgpu: boolean; f16: boolean }> | null = null
export function probeGpu() {
  gpuProbe ??= (async () => {
    try {
      const adapter = await (navigator as any).gpu?.requestAdapter()
      const r = { webgpu: !!adapter, f16: !!adapter?.features?.has('shader-f16') }
      setSpeech({ device: r.webgpu ? 'webgpu' : 'wasm', f16: r.f16 })
      return r
    } catch {
      setSpeech({ device: 'wasm', f16: false })
      return { webgpu: false, f16: false }
    }
  })()
  return gpuProbe
}

export function modelById(id: string) {
  return whisperModels.find((m) => m.id === id) ?? whisperModels[1]
}

let worker: Worker | null = null
let seq = 0
const waiting = new Map<number, { resolve: (t: string) => void; reject: (e: Error) => void }>()

function getWorker() {
  if (worker) return worker
  worker = new Worker(new URL('./whisper.worker.ts', import.meta.url), { type: 'module' })
  const abs = (u: string) => new URL(u, location.href).href
  worker.postMessage({ type: 'config', remoteHost: `${location.origin}/models/hf/`, wasm: { mjs: abs(ortMjs), wasm: abs(ortWasm) } })
  worker.onmessage = (e) => {
    const m = e.data
    if (m.type === 'progress') setSpeech({ loaded: m.loaded, total: m.total })
    else if (m.type === 'phase') setSpeech({ phase: m.phase, ...(m.phase === 'transcribing' ? { loaded: 0, total: 0 } : {}) })
    else if (m.type === 'done') {
      waiting.get(m.id)?.resolve(m.text)
      waiting.delete(m.id)
    } else if (m.type === 'error') {
      waiting.get(m.id)?.reject(new Error(m.message))
      waiting.delete(m.id)
    }
  }
  worker.onerror = (e) => {
    for (const w of waiting.values()) w.reject(new Error(e.message || t('transcription engine error')))
    waiting.clear()
    worker?.terminate()
    worker = null
  }
  return worker
}

/** Audio of any file the browser can decode, as 16 kHz mono samples. */
async function decode(blob: Blob): Promise<Float32Array> {
  const ctx = new AudioContext({ sampleRate: 16000 })
  try {
    const buf = await ctx.decodeAudioData(await blob.arrayBuffer())
    if (buf.numberOfChannels === 1) return buf.getChannelData(0).slice()
    const out = new Float32Array(buf.length)
    for (let c = 0; c < buf.numberOfChannels; c++) {
      const d = buf.getChannelData(c)
      for (let i = 0; i < d.length; i++) out[i] += d[i] / buf.numberOfChannels
    }
    return out
  } catch {
    throw new Error(t('audio the browser cannot read'))
  } finally {
    ctx.close()
  }
}

function run(audio: Float32Array, repo: string, dtype: any, device: 'webgpu' | 'wasm'): Promise<string> {
  const id = ++seq
  return new Promise((resolve, reject) => {
    waiting.set(id, { resolve, reject })
    getWorker().postMessage({ type: 'transcribe', id, repo, dtype, device, audio, language: prefs.whisperLang }, [audio.buffer])
  })
}

let queue: Promise<unknown> = Promise.resolve()

/** Transcribes an audio (or video) file. Requests run one after the other. */
export function transcribe(blob: Blob): Promise<string> {
  const job = queue.then(async () => {
    setSpeech({ phase: 'loading', loaded: 0, total: 0 })
    try {
      const audio = await decode(blob)
      if (!audio.length) return ''
      const gpu = await probeGpu()
      let model = modelById(prefs.whisperModel)
      if (model.webgpuOnly && !(gpu.webgpu && gpu.f16)) model = modelById('small')
      if (gpu.webgpu) {
        try {
          return await run(audio.slice(), model.repo, model.dtype.webgpu, 'webgpu')
        } catch (e) {
          if (model.webgpuOnly) throw e
          console.warn('WebGPU not available for the transcription, falling back to WebAssembly:', e)
        }
      }
      return await run(audio, model.repo, model.dtype.wasm, 'wasm')
    } finally {
      setSpeech({ phase: 'idle', loaded: 0, total: 0 })
    }
  })
  queue = job.catch(() => {})
  return job
}

// ---------- microphone ----------

let recorder: MediaRecorder | null = null
let chunks: Blob[] = []

export function canRecord() {
  return !!navigator.mediaDevices?.getUserMedia && typeof MediaRecorder !== 'undefined'
}

export async function startRecording() {
  if (recorder) return
  if (!canRecord()) throw new Error(window.isSecureContext ? t('microphone not available in this browser') : t('the microphone requires https or localhost'))
  const stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } })
  chunks = []
  recorder = new MediaRecorder(stream)
  recorder.ondataavailable = (e) => e.data.size && chunks.push(e.data)
  recorder.start(250)
  setSpeech({ phase: 'recording', startedAt: Date.now() })
}

/** Stops the recording and returns its audio (null when nothing was recorded). */
export function stopRecording(): Promise<Blob | null> {
  const r = recorder
  if (!r) return Promise.resolve(null)
  recorder = null
  return new Promise((resolve) => {
    r.onstop = () => {
      r.stream.getTracks().forEach((t) => t.stop())
      setSpeech({ phase: 'idle' })
      resolve(chunks.length ? new Blob(chunks, { type: r.mimeType }) : null)
    }
    r.stop()
  })
}

export function cancelRecording() {
  stopRecording().catch(() => {})
}
