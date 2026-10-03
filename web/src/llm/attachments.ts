// Files joined to a message, converted to what the model accepts: images as data URLs,
// video as such when the model reads it (else frames), audio, PDF as text (or pages as
// images when it has no text), other files as text.
import type { Attachment, Caps, Part } from './state'

export interface Prepared {
  parts: Part[]
  attachment: Attachment
}

const MAX_TEXT = 200_000
const MAX_IMAGE_SIDE = 2048
const VIDEO_FRAMES = 8
const PDF_PAGES = 8

function readAs(file: Blob, kind: 'dataURL' | 'text' | 'buffer'): Promise<any> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(r.result)
    r.onerror = () => reject(r.error ?? new Error('lecture impossible'))
    if (kind === 'dataURL') r.readAsDataURL(file)
    else if (kind === 'text') r.readAsText(file)
    else r.readAsArrayBuffer(file)
  })
}

function loadImage(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('image illisible'))
    img.src = src
  })
}

function draw(src: CanvasImageSource, w: number, h: number, max: number, type = 'image/jpeg', quality = 0.85): string {
  const scale = Math.min(1, max / Math.max(w, h))
  const c = document.createElement('canvas')
  c.width = Math.max(1, Math.round(w * scale))
  c.height = Math.max(1, Math.round(h * scale))
  const ctx = c.getContext('2d')!
  if (type === 'image/jpeg') {
    ctx.fillStyle = '#fff'
    ctx.fillRect(0, 0, c.width, c.height)
  }
  ctx.drawImage(src, 0, 0, c.width, c.height)
  return c.toDataURL(type, quality)
}

async function image(file: File): Promise<Prepared> {
  let url: string = await readAs(file, 'dataURL')
  const img = await loadImage(url)
  // Big images are reduced: the model sees them at a few hundred pixels anyway.
  const big = Math.max(img.naturalWidth, img.naturalHeight) > MAX_IMAGE_SIDE || file.size > 4 << 20
  const supported = /^data:image\/(png|jpeg|webp|gif)/.test(url)
  if (big || !supported) url = draw(img, img.naturalWidth, img.naturalHeight, MAX_IMAGE_SIDE, file.type === 'image/png' ? 'image/png' : 'image/jpeg')
  return {
    parts: [{ type: 'image_url', image_url: { url } }],
    attachment: { name: file.name, kind: 'image', size: file.size, thumb: draw(img, img.naturalWidth, img.naturalHeight, 96) },
  }
}

function seek(video: HTMLVideoElement, t: number): Promise<void> {
  return new Promise((resolve) => {
    video.onseeked = () => resolve()
    video.currentTime = t
  })
}

async function videoFrames(file: File): Promise<{ frames: { t: number; url: string }[]; duration: number }> {
  const src = URL.createObjectURL(file)
  try {
    const video = document.createElement('video')
    video.muted = true
    video.preload = 'auto'
    video.src = src
    await new Promise<void>((resolve, reject) => {
      video.onloadeddata = () => resolve()
      video.onerror = () => reject(new Error('vidéo illisible par le navigateur'))
    })
    const duration = isFinite(video.duration) ? video.duration : 0
    const frames: { t: number; url: string }[] = []
    const n = duration > 0 ? VIDEO_FRAMES : 1
    for (let i = 0; i < n; i++) {
      const t = duration > 0 ? (duration * (i + 0.5)) / n : 0
      await seek(video, t)
      frames.push({ t, url: draw(video, video.videoWidth, video.videoHeight, 768) })
    }
    return { frames, duration }
  } finally {
    URL.revokeObjectURL(src)
  }
}

async function video(file: File, caps: Caps): Promise<Prepared> {
  const { frames, duration } = await videoFrames(file)
  const thumb = frames[0] ? draw(await loadImage(frames[0].url), 768, 432, 96) : undefined
  if (caps.video) {
    const url: string = await readAs(file, 'dataURL')
    return { parts: [{ type: 'input_video', input_video: { url } }], attachment: { name: file.name, kind: 'video', size: file.size, thumb } }
  }
  const parts: Part[] = [{ type: 'text', text: `Vidéo « ${file.name} » (${duration.toFixed(1)} s), ${frames.length} images extraites :` }]
  for (const f of frames) {
    parts.push({ type: 'text', text: `t = ${f.t.toFixed(1)} s` })
    parts.push({ type: 'image_url', image_url: { url: f.url } })
  }
  return { parts, attachment: { name: file.name, kind: 'video', size: file.size, thumb, note: `${frames.length} images` } }
}

async function audio(file: File): Promise<Prepared> {
  const url: string = await readAs(file, 'dataURL')
  const data = url.slice(url.indexOf(',') + 1)
  const ext = file.name.split('.').pop()?.toLowerCase() ?? ''
  const format = ext === 'mp3' || file.type === 'audio/mpeg' ? 'mp3' : ext || 'wav'
  return { parts: [{ type: 'input_audio', input_audio: { data, format } }], attachment: { name: file.name, kind: 'audio', size: file.size } }
}

let pdfjs: Promise<any> | null = null
function loadPdfjs() {
  pdfjs ??= Promise.all([import('pdfjs-dist'), import('pdfjs-dist/build/pdf.worker.min.mjs?url')]).then(([lib, worker]) => {
    lib.GlobalWorkerOptions.workerSrc = worker.default
    return lib
  })
  return pdfjs
}

async function pdf(file: File, caps: Caps): Promise<Prepared> {
  const lib = await loadPdfjs()
  const doc = await lib.getDocument({ data: new Uint8Array(await readAs(file, 'buffer')) }).promise
  let text = ''
  for (let i = 1; i <= doc.numPages && text.length < MAX_TEXT; i++) {
    const page = await doc.getPage(i)
    const content = await page.getTextContent()
    let line = ''
    for (const item of content.items as any[]) {
      line += item.str ?? ''
      if (item.hasEOL) line += '\n'
    }
    text += `\n--- page ${i} ---\n${line.trim()}\n`
  }
  const att: Attachment = { name: file.name, kind: 'pdf', size: file.size, note: `${doc.numPages} pages` }
  const letters = text.replace(/--- page \d+ ---|\s/g, '').length
  if (letters < 3 * doc.numPages && caps.vision) {
    // Scanned document (next to no text): its pages as images.
    const parts: Part[] = [{ type: 'text', text: `PDF « ${file.name} » (${doc.numPages} pages, sans texte) : pages en images.` }]
    for (let i = 1; i <= Math.min(doc.numPages, PDF_PAGES); i++) {
      const page = await doc.getPage(i)
      const vp = page.getViewport({ scale: 1.5 })
      const c = document.createElement('canvas')
      c.width = vp.width
      c.height = vp.height
      await page.render({ canvasContext: c.getContext('2d')!, viewport: vp, canvas: c }).promise
      parts.push({ type: 'image_url', image_url: { url: draw(c, c.width, c.height, 1600) } })
    }
    att.note = `${Math.min(doc.numPages, PDF_PAGES)} pages en images`
    return { parts, attachment: att }
  }
  if (text.length > MAX_TEXT) text = text.slice(0, MAX_TEXT) + '\n… (tronqué)'
  return { parts: [{ type: 'text', text: `PDF « ${file.name} » (${doc.numPages} pages) :\n${text}` }], attachment: att }
}

async function textFile(file: File): Promise<Prepared> {
  if (file.size > 4 << 20) throw new Error(`${file.name} : fichier trop gros pour être joint`)
  const head = new Uint8Array(await readAs(file.slice(0, 8000), 'buffer'))
  if (head.includes(0)) throw new Error(`${file.name} : type de fichier non pris en charge`)
  let text: string = await readAs(file, 'text')
  if (text.length > MAX_TEXT) text = text.slice(0, MAX_TEXT) + '\n… (tronqué)'
  const ext = file.name.includes('.') ? file.name.split('.').pop() : ''
  return {
    parts: [{ type: 'text', text: `Fichier joint « ${file.name} » :\n\`\`\`${ext}\n${text}\n\`\`\`` }],
    attachment: { name: file.name, kind: 'text', size: file.size },
  }
}

/** Converts a file; warnings say what the model will not see. */
export async function prepare(file: File, caps: Caps | undefined): Promise<Prepared & { warning?: string }> {
  const c: Caps = caps ?? { vision: false, video: false, audio: false, tools: true, thinking: false, known: false }
  const type = file.type
  const name = file.name.toLowerCase()
  if (type.startsWith('image/')) {
    const p = await image(file)
    return { ...p, warning: c.known && !c.vision ? 'Ce modèle ne lit pas les images' : undefined }
  }
  if (type.startsWith('video/')) {
    const p = await video(file, c)
    return { ...p, warning: c.known && !c.vision && !c.video ? 'Ce modèle ne lit pas les images ni la vidéo' : undefined }
  }
  if (type.startsWith('audio/')) {
    const p = await audio(file)
    return { ...p, warning: !c.audio ? 'Ce modèle ne semble pas accepter l’audio' : undefined }
  }
  if (type === 'application/pdf' || name.endsWith('.pdf')) return pdf(file, c)
  return textFile(file)
}
