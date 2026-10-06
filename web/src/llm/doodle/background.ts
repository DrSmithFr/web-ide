// Images under a doodle: a file, a pasted image or a screenshot, reduced like the attachments.
import { t } from '../../i18n'

const MAX_SIDE = 2048

export interface Picture {
  src: string
  w: number
  h: number
}

/** JPEG only for a photo or a capture (png false); a GIF, a WebP or a PNG stays a PNG to keep its transparency. */
function draw(src: CanvasImageSource, w: number, h: number, png: boolean): Picture {
  const scale = Math.min(1, MAX_SIDE / Math.max(w, h))
  const c = document.createElement('canvas')
  c.width = Math.max(1, Math.round(w * scale))
  c.height = Math.max(1, Math.round(h * scale))
  const g = c.getContext('2d')!
  if (!png) {
    // JPEG has no transparency: white rather than black.
    g.fillStyle = '#fff'
    g.fillRect(0, 0, c.width, c.height)
  }
  g.drawImage(src, 0, 0, c.width, c.height)
  return { src: png ? c.toDataURL('image/png') : c.toDataURL('image/jpeg', 0.85), w: c.width, h: c.height }
}

export async function pictureOf(file: Blob): Promise<Picture> {
  const url = URL.createObjectURL(file)
  try {
    const img = new Image()
    await new Promise<void>((resolve, reject) => {
      img.onload = () => resolve()
      img.onerror = () => reject(new Error(t('unreadable image')))
      img.src = url
    })
    return draw(img, img.naturalWidth, img.naturalHeight, file.type !== 'image/jpeg')
  } finally {
    URL.revokeObjectURL(url)
  }
}

export const canCapture = () => !!navigator.mediaDevices?.getDisplayMedia

/** One frame of a screen, a window or a tab, chosen by the user in the browser. */
export async function captureScreen(): Promise<Picture> {
  const stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false })
  try {
    const video = document.createElement('video')
    video.muted = true
    video.srcObject = stream
    await video.play()
    if (!video.videoWidth) await new Promise((r) => video.addEventListener('resize', r, { once: true }))
    // The first frames of a capture can be black: wait for one more.
    await new Promise((r) => setTimeout(r, 150))
    return draw(video, video.videoWidth, video.videoHeight, true)
  } finally {
    stream.getTracks().forEach((tr) => tr.stop())
  }
}
