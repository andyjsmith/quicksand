import type { DisplaySize, ScaleMode } from './types'

/** Absolute websocket URL for a path on the current origin. */
export function websocketURL(path: string): string {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${location.host}${path}`
}

/**
 * Positions a guest display element inside a viewport container: centred,
 * and scaled according to the scale mode. Used by drivers whose client
 * library renders at native size.
 */
export class DisplayLayout {
  private size?: DisplaySize
  private mode: ScaleMode = 'fit'
  private observer: ResizeObserver

  constructor(
    private container: HTMLElement,
    private display: HTMLElement,
    private onResolution: (size: DisplaySize) => void,
  ) {
    display.style.position = 'absolute'
    display.style.transformOrigin = '0 0'
    container.appendChild(display)
    this.observer = new ResizeObserver(() => this.update())
    this.observer.observe(container)
  }

  setMode(mode: ScaleMode): void {
    this.mode = mode
    this.update()
  }

  setDisplaySize(size: DisplaySize): void {
    if (this.size?.width === size.width && this.size?.height === size.height) return
    this.size = size
    this.onResolution(size)
    this.update()
  }

  viewportSize(): DisplaySize {
    return { width: this.container.clientWidth, height: this.container.clientHeight }
  }

  destroy(): void {
    this.observer.disconnect()
    this.display.remove()
    this.container.style.overflow = ''
  }

  private update(): void {
    const { width: vw, height: vh } = this.viewportSize()
    this.container.style.overflow = this.mode === 'none' ? 'auto' : 'hidden'
    if (!this.size || !vw || !vh) return
    const { width, height } = this.size
    const fit = Math.min(vw / width, vh / height)
    // A guest resized to the viewport should fill it exactly; scale down only
    // while it catches up (or if it refuses the requested size).
    const scale = this.mode === 'fit' ? fit : this.mode === 'remote' ? Math.min(1, fit) : 1
    this.display.style.transform = scale === 1 ? '' : `scale(${scale})`
    this.display.style.left = `${Math.max(0, (vw - width * scale) / 2)}px`
    this.display.style.top = `${Math.max(0, (vh - height * scale) / 2)}px`
  }
}
