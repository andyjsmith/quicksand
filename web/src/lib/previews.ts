// Screenshot previews of VMs, captured client-side while a console is open
// and kept only in this browser's localStorage.

const PREFIX = 'quicksand.preview.'
const WIDTH = 480
const QUALITY = 0.7

const listeners = new Set<(id: string) => void>()

function storage(): Storage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

/** Store a downscaled JPEG of the canvas as the VM's preview. */
export function savePreview(id: string, canvas: HTMLCanvasElement) {
  if (!canvas.width || !canvas.height) return
  const scale = Math.min(1, WIDTH / canvas.width)
  const thumb = document.createElement('canvas')
  thumb.width = Math.round(canvas.width * scale)
  thumb.height = Math.round(canvas.height * scale)
  const ctx = thumb.getContext('2d')
  if (!ctx) return
  ctx.drawImage(canvas, 0, 0, thumb.width, thumb.height)
  try {
    storage()?.setItem(PREFIX + id, thumb.toDataURL('image/jpeg', QUALITY))
  } catch {
    // Storage full or unavailable; previews are a convenience.
    return
  }
  listeners.forEach((fn) => fn(id))
}

export function loadPreview(id: string): string | null {
  try {
    return storage()?.getItem(PREFIX + id) ?? null
  } catch {
    return null
  }
}

export function forgetPreview(id: string) {
  try {
    storage()?.removeItem(PREFIX + id)
  } catch {
    // Ignore unavailable storage.
  }
  listeners.forEach((fn) => fn(id))
}

/** Calls fn with a VM ID whenever its preview changes. Returns a stop function. */
export function onPreviewChange(fn: (id: string) => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}
