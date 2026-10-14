import type { DisplaySize } from './types'

const DEBOUNCE_MS = 400

/**
 * Asks the guest to change its resolution to the viewport's size through
 * the Quicksand agent, for consoles that can't themselves (VNC, or SPICE
 * without its agent). Requests are debounced and never overlap; after one
 * fails (say, a guest the agent can't resize), it stops trying.
 */
export class GuestResizer {
  private timer?: ReturnType<typeof setTimeout>
  private last = ''
  private busy = false
  private pending?: DisplaySize
  failed = false

  constructor(
    private resize: (size: DisplaySize) => Promise<unknown>,
    private onFailure: () => void,
  ) {}

  /** Requests size soon, replacing any request not yet sent. */
  request(size: DisplaySize, delay = DEBOUNCE_MS): void {
    if (this.failed) return
    clearTimeout(this.timer)
    this.timer = setTimeout(() => this.send(size), delay)
  }

  /** Forgets the last size sent, so the next request goes out even if equal. */
  reset(): void {
    this.last = ''
  }

  stop(): void {
    clearTimeout(this.timer)
    this.failed = true
  }

  private async send(size: DisplaySize): Promise<void> {
    // Guests generally want widths that are a multiple of 8.
    const width = size.width - (size.width % 8)
    const height = size.height
    const key = `${width}x${height}`
    if (width < 320 || height < 200 || key === this.last || this.failed) return
    if (this.busy) {
      this.pending = size
      return
    }
    this.busy = true
    this.last = key
    try {
      await this.resize({ width, height })
    } catch {
      this.failed = true
      this.onFailure()
    } finally {
      this.busy = false
    }
    const next = this.pending
    this.pending = undefined
    if (next) this.send(next)
  }
}
