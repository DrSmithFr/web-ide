// Speech recognition worker: Whisper run by transformers.js (WebGPU, else WebAssembly).
// The audio never leaves the browser; the model files come from the pod cache.
import { env, pipeline, type AutomaticSpeechRecognitionPipeline } from '@huggingface/transformers'

type Msg =
  | { type: 'config'; remoteHost: string; wasm: { mjs: string; wasm: string } }
  | { type: 'transcribe'; id: number; repo: string; dtype: any; device: 'webgpu' | 'wasm'; audio: Float32Array; language: string }

let asr: AutomaticSpeechRecognitionPipeline | null = null
let loadedKey = ''

async function load(repo: string, dtype: any, device: 'webgpu' | 'wasm') {
  const key = `${repo}|${JSON.stringify(dtype)}|${device}`
  if (asr && loadedKey === key) return
  await asr?.dispose()
  asr = null
  asr = (await pipeline('automatic-speech-recognition', repo, {
    dtype,
    device,
    progress_callback: (p: any) => {
      if (p.status === 'progress_total') postMessage({ type: 'progress', loaded: p.loaded, total: p.total })
    },
  })) as AutomaticSpeechRecognitionPipeline
  loadedKey = key
}

self.onmessage = async (e: MessageEvent<Msg>) => {
  const m = e.data
  if (m.type === 'config') {
    env.allowLocalModels = false
    env.remoteHost = m.remoteHost
    // The ONNX runtime comes with the app (no CDN).
    const onnx = env.backends.onnx as any
    onnx.wasm.wasmPaths = m.wasm
    return
  }
  try {
    postMessage({ type: 'phase', id: m.id, phase: 'loading' })
    await load(m.repo, m.dtype, m.device)
    postMessage({ type: 'phase', id: m.id, phase: 'transcribing' })
    const out: any = await asr!(m.audio, {
      language: m.language === 'auto' ? null : m.language,
      task: 'transcribe',
      chunk_length_s: 30,
      stride_length_s: 5,
    } as any)
    const text = (Array.isArray(out) ? out.map((o) => o.text).join(' ') : out.text) as string
    postMessage({ type: 'done', id: m.id, text: text.trim() })
  } catch (err) {
    postMessage({ type: 'error', id: m.id, message: (err as Error)?.message ?? String(err) })
  }
}
