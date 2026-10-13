import RFB from '@novnc/novnc'
import { Emitter } from '../emitter'
import { sendKeyCombo, typeWithKeys } from '../keyboard'
import { websocketURL } from '../layout'
import type {
  AudioSettings,
  ConnectOptions,
  ConsoleCapabilities,
  ConsoleDriver,
  ConsoleEvents,
  ConsoleSession,
  ScaleMode,
} from '../types'

// noVNC learns whether the server accepts resize requests from the first
// framebuffer update after connecting.
const RESIZE_PROBE_MS = 1500

/** A VNC console using noVNC. */
export class VncDriver implements ConsoleDriver {
  readonly protocol = 'vnc' as const
  capabilities: ConsoleCapabilities = {
    scaleModes: ['fit', 'none'],
    // QEMU only exchanges clipboard text over VNC with a qemu-vdagent
    // device, which Proxmox does not configure.
    clipboardSync: false,
    clipboardSend: false,
    audio: false,
    keyCombos: true,
    typeText: true,
  }

  private events = new Emitter<ConsoleEvents>()
  private rfb?: RFB
  private connected = false
  private scaleMode: ScaleMode = 'fit'
  private probeTimer?: ReturnType<typeof setTimeout>
  private canvasObserver?: MutationObserver

  on<K extends keyof ConsoleEvents>(event: K, handler: (payload: ConsoleEvents[K]) => void) {
    return this.events.on(event, handler)
  }

  connect(container: HTMLElement, session: ConsoleSession, options: ConnectOptions): void {
    this.scaleMode = options.scaleMode
    this.events.emit('status', { status: 'connecting' })

    const rfb = new RFB(container, websocketURL(session.path), {
      credentials: { password: session.password },
      wsProtocols: ['binary'],
    })
    this.rfb = rfb
    rfb.background = 'transparent'
    this.applyScaleMode()

    rfb.addEventListener('connect', () => {
      this.connected = true
      this.events.emit('status', { status: 'connected' })
      this.watchResolution(container)
      this.probeTimer = setTimeout(() => this.probeResize(), RESIZE_PROBE_MS)
    })
    rfb.addEventListener('disconnect', (e) => {
      this.connected = false
      this.events.emit(
        'status',
        e.detail.clean ? { status: 'disconnected' } : { status: 'error', message: 'Connection lost' },
      )
    })
    rfb.addEventListener('credentialsrequired', () => rfb.sendCredentials({ password: session.password }))
    rfb.addEventListener('securityfailure', (e) =>
      this.events.emit('status', { status: 'error', message: e.detail.reason ?? 'Authentication failed' }),
    )
    rfb.addEventListener('clipboard', (e) => this.events.emit('clipboard', e.detail.text))
  }

  disconnect(): void {
    clearTimeout(this.probeTimer)
    this.canvasObserver?.disconnect()
    const rfb = this.rfb
    this.rfb = undefined
    this.connected = false
    // Disconnecting removes noVNC's elements from the container.
    rfb?.disconnect()
    this.events.emit('status', { status: 'disconnected' })
    this.events.clear()
  }

  focus(): void {
    this.rfb?.focus()
  }

  sendKeys(codes: string[]): Promise<void> {
    return sendKeyCombo(this.sendKey, codes)
  }

  typeText(text: string): Promise<{ skipped: string[] }> {
    return typeWithKeys(this.sendKey, text)
  }

  async sendClipboard(text: string): Promise<void> {
    this.rfb?.clipboardPasteFrom(text)
  }

  setScaleMode(mode: ScaleMode): void {
    this.scaleMode = mode
    this.applyScaleMode()
  }

  setAudio(_settings: AudioSettings): void {}

  resumeAudio(): void {}

  private sendKey = (code: string, down: boolean, keysym?: number): boolean => {
    if (!this.rfb || !this.connected) return false
    if (keysym !== undefined) this.rfb.sendKey(keysym, code, down)
    return true
  }

  private applyScaleMode(): void {
    if (!this.rfb) return
    this.rfb.resizeSession = this.scaleMode === 'remote'
    // When resizing, still scale down while the guest catches up.
    this.rfb.scaleViewport = this.scaleMode !== 'none'
    this.rfb.clipViewport = false
  }

  private probeResize(): void {
    // Not part of noVNC's public API; set once the server announces
    // ExtendedDesktopSize support.
    const supported = (this.rfb as unknown as { _supportsSetDesktopSize?: boolean })?._supportsSetDesktopSize
    if (!supported) return
    this.capabilities = { ...this.capabilities, scaleModes: ['remote', 'fit', 'none'] }
    this.events.emit('capabilities', this.capabilities)
  }

  private watchResolution(container: HTMLElement): void {
    const canvas = container.querySelector('canvas')
    if (!canvas) return
    const report = () => this.events.emit('resolution', { width: canvas.width, height: canvas.height })
    report()
    this.canvasObserver = new MutationObserver(report)
    this.canvasObserver.observe(canvas, { attributes: true, attributeFilter: ['width', 'height'] })
  }
}
