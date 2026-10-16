import { code_to_scancode } from '@/vendor/spice-html5/code_to_scancode.js'
import { Constants } from '@/vendor/spice-html5/enums.js'
import { SpiceMainConn } from '@/vendor/spice-html5/main.js'
import {
  SpiceMiniData,
  SpiceMsgcKeyDown,
  SpiceMsgcKeyUp,
  SpiceMsgClipboardReceive,
  SpiceMsgClipboardSend,
} from '@/vendor/spice-html5/spicemsg.js'
import { watchClipboardRead } from '../clipboard'
import { interceptPaste } from '../paste'
import { Emitter } from '../emitter'
import { sendKeyCombo, typeWithKeys } from '../keyboard'
import type {
  AudioSettings,
  AudioStatus,
  ConnectOptions,
  ConsoleCapabilities,
  ConsoleDriver,
  ConsoleEvents,
  ConsoleSession,
  DisplaySize,
  ScaleMode,
} from '../types'
import { DisplayLayout, websocketURL } from '../layout'
import { OpusPlayer, opusPlayerSupported } from './OpusPlayer'
import { GuestResizer } from '../guestResize'

const RESIZE_DEBOUNCE_MS = 250
// Lets the clipboard grab (main channel) reach the agent before the V
// keystroke (inputs channel) makes the guest application paste.
const PASTE_KEY_DELAY_MS = 50

let nextScreenId = 0

/** A SPICE console using the vendored spice-html5 client. */
export class SpiceDriver implements ConsoleDriver {
  readonly protocol = 'spice' as const
  capabilities: ConsoleCapabilities = {
    scaleModes: ['fit', 'none'],
    clipboardSync: false,
    clipboardSend: false,
    audio: opusPlayerSupported(),
    keyCombos: true,
    typeText: true,
  }

  private events = new Emitter<ConsoleEvents>()
  private sc?: SpiceMainConn
  private connected = false
  private layout?: DisplayLayout
  private screen?: HTMLElement
  private scaleMode: ScaleMode = 'fit'
  private audio: AudioSettings = { muted: false, volume: 1 }
  private player?: OpusPlayer
  private audioStatus: AudioStatus = 'idle'
  private observers: { disconnect(): void }[] = []
  private resizeTimer?: ReturnType<typeof setTimeout>
  private lastRequested = ''
  /**
   * Resizes the guest through the Quicksand agent: while the SPICE agent
   * isn't connected, or always with preferResizer.
   */
  private resizer?: GuestResizer
  private preferResizer = false
  /**
   * The newest clipboard text we know of: copied in the guest, pasted with
   * Ctrl+V, or sent from the sidebar. Given to the guest when it asks for
   * the local clipboard and the browser can't be read silently.
   */
  private clipboardText: string | null = null
  /** The browser lets us read the clipboard without prompting (Chromium, once granted). */
  private canReadClipboard = false
  /** Automatic clipboard sharing (see setClipboardSharing). */
  private clipboardSharing = true
  /** spice-html5's clipboard grab, before it is gated on canReadClipboard. */
  private grabClipboard?: () => void
  private cleanups: (() => void)[] = []

  on<K extends keyof ConsoleEvents>(event: K, handler: (payload: ConsoleEvents[K]) => void) {
    return this.events.on(event, handler)
  }

  connect(container: HTMLElement, session: ConsoleSession, options: ConnectOptions): void {
    this.scaleMode = options.scaleMode
    this.audio = options.audio
    if (options.resizeGuest) this.setGuestResize(options.resizeGuest, !!options.preferResizeGuest)

    const screen = document.createElement('div')
    screen.id = `quicksand-spice-screen-${nextScreenId++}`
    screen.className = 'spice-screen'
    screen.style.position = 'relative'
    screen.style.width = 'max-content'
    this.screen = screen
    this.layout = new DisplayLayout(container, screen, (size) => this.events.emit('resolution', size))
    this.layout.setMode(this.effectiveScaleMode())

    // spice-html5 adds the display canvas to the screen once the display
    // channel is up; track it to size the display.
    const mutations = new MutationObserver(() => {
      // The newest canvas: while the guest changes resolution, the old one
      // lingers until its surface is destroyed.
      const canvas = screen.querySelector<HTMLCanvasElement>('canvas:last-of-type')
      for (const c of screen.querySelectorAll('canvas')) c.style.display = c === canvas ? '' : 'none'
      if (canvas) {
        this.layout?.setDisplaySize({ width: canvas.width, height: canvas.height })
        // spice-html5 never calls its onsuccess callback, so the primary
        // surface appearing is what marks the session as connected.
        if (!this.connected) {
          this.connected = true
          this.events.emit('status', { status: 'connected' })
        }
      }
    })
    mutations.observe(screen, { childList: true })
    this.observers.push(mutations)

    const resize = new ResizeObserver(() => this.scheduleRemoteResize())
    resize.observe(container)
    this.observers.push(resize)

    // Browsers refuse to start audio before the user has interacted with
    // the page; retry on the first click or key press anywhere.
    const unblockAudio = () => {
      if (this.audioStatus === 'blocked') this.resumeAudio()
    }
    window.addEventListener('pointerdown', unblockAudio, { capture: true })
    window.addEventListener('keydown', unblockAudio, { capture: true })
    this.cleanups.push(() => {
      window.removeEventListener('pointerdown', unblockAudio, { capture: true })
      window.removeEventListener('keydown', unblockAudio, { capture: true })
      this.player?.close()
      this.player = undefined
    })

    this.cleanups.push(watchClipboardRead((state) => (this.canReadClipboard = state === 'granted')))
    this.interceptPaste(container)

    this.events.emit('status', { status: 'connecting' })
    try {
      this.sc = new SpiceMainConn({
        uri: websocketURL(session.path),
        password: session.password,
        screen_id: screen.id,
        onerror: (e) => this.handleError(e),
        onagent: () => this.handleAgent(),
      })
    } catch (e) {
      this.handleError(e as Error)
      return
    }
    // Guest audio goes to our WebCodecs player rather than spice-html5's
    // MediaSource one (see OpusPlayer). Without WebCodecs it is discarded.
    if (this.capabilities.audio) {
      this.player = new OpusPlayer(this.audio, (status, message) => this.setAudioStatus(status, message))
      this.sc.playback_sink = this.player
    } else {
      this.sc.playback_sink = { start() {}, data() {}, stop() {} }
    }
    this.hookClipboard(this.sc)
  }

  disconnect(): void {
    clearTimeout(this.resizeTimer)
    this.resizer?.stop()
    this.observers.forEach((o) => o.disconnect())
    this.observers = []
    this.cleanups.forEach((fn) => fn())
    this.cleanups = []
    const sc = this.sc
    this.sc = undefined
    sc?.stop()
    this.layout?.destroy()
    this.layout = undefined
    this.screen = undefined
    this.events.emit('status', { status: 'disconnected' })
    this.events.clear()
  }

  focus(): void {
    this.screen?.querySelector<HTMLCanvasElement>('canvas:last-of-type')?.focus()
  }

  snapshot(): HTMLCanvasElement | null {
    return this.connected ? (this.screen?.querySelector<HTMLCanvasElement>('canvas:last-of-type') ?? null) : null
  }

  sendKeys(codes: string[]): Promise<void> {
    return sendKeyCombo(this.sendKey, codes)
  }

  typeText(text: string): Promise<{ skipped: string[] }> {
    return typeWithKeys(this.sendKey, text)
  }

  async sendClipboard(text: string): Promise<void> {
    if (!this.sc?.agent_connected) throw new Error('The SPICE agent is not running in the guest')
    this.offerClipboard(text)
  }

  setScaleMode(mode: ScaleMode): void {
    this.scaleMode = mode
    this.layout?.setMode(this.effectiveScaleMode())
    this.lastRequested = ''
    this.scheduleRemoteResize()
  }

  setAudio(settings: AudioSettings): void {
    this.audio = settings
    this.player?.setSettings(settings)
  }

  resumeAudio(): void {
    this.player?.resume()
  }

  private sendKey = (code: string, down: boolean): boolean => {
    const inputs = this.sc?.inputs
    if (!inputs || inputs.state !== 'ready' || inputs.ws?.readyState !== WebSocket.OPEN) return false
    const scancode = code_to_scancode[code]
    if (!scancode) return true
    const key = down ? new SpiceMsgcKeyDown() : new SpiceMsgcKeyUp()
    // Releases carry the break bit on the last byte of the scancode.
    key.code = down ? scancode : scancode < 0x100 ? scancode | 0x80 : scancode | 0x8000
    const msg = new SpiceMiniData()
    msg.build_msg(down ? Constants.SPICE_MSGC_INPUTS_KEY_DOWN! : Constants.SPICE_MSGC_INPUTS_KEY_UP!, key)
    inputs.send_msg(msg)
    return true
  }

  setClipboardSharing(on: boolean): void {
    this.clipboardSharing = on
  }

  setGuestResize(resize: (size: DisplaySize) => Promise<unknown>, prefer: boolean): void {
    this.resizer?.stop()
    this.preferResizer = prefer
    this.resizer = new GuestResizer(resize, () => {
      if (!this.sc?.agent_connected) {
        this.capabilities = { ...this.capabilities, scaleModes: ['fit', 'none'] }
        this.events.emit('capabilities', this.capabilities)
      }
      this.layout?.setMode(this.effectiveScaleMode())
    })
    this.capabilities = { ...this.capabilities, scaleModes: ['remote', 'fit', 'none'] }
    this.events.emit('capabilities', this.capabilities)
    this.layout?.setMode(this.effectiveScaleMode())
    this.scheduleRemoteResize(0)
  }

  /**
   * Resizing the guest needs the SPICE agent or the Quicksand agent;
   * without either, fall back to scaling.
   */
  private effectiveScaleMode(): ScaleMode {
    const canResize = this.sc?.agent_connected || (this.resizer && !this.resizer.failed)
    return this.scaleMode === 'remote' && !canResize ? 'fit' : this.scaleMode
  }

  private handleAgent(): void {
    this.capabilities = {
      ...this.capabilities,
      scaleModes: ['remote', 'fit', 'none'],
      clipboardSync: true,
      clipboardSend: true,
    }
    this.events.emit('capabilities', this.capabilities)
    this.layout?.setMode(this.effectiveScaleMode())
    this.lastRequested = ''
    this.scheduleRemoteResize(0)
  }

  private handleError(e: Error | Event): void {
    this.connected = false
    const message = e instanceof Error ? e.message : 'Connection closed'
    this.events.emit('status', { status: 'error', message })
  }

  private scheduleRemoteResize(delay = RESIZE_DEBOUNCE_MS): void {
    if (this.scaleMode !== 'remote') return
    clearTimeout(this.resizeTimer)
    this.resizeTimer = setTimeout(() => {
      const viewport = this.layout?.viewportSize()
      if (!viewport) return
      // The Quicksand agent when preferred or the SPICE agent is missing;
      // the SPICE agent if the Quicksand one can't.
      const useResizer = this.resizer && !this.resizer.failed
      if (useResizer && (this.preferResizer || !this.sc?.agent_connected)) {
        this.resizer!.request(viewport, 0)
        return
      }
      if (!this.sc?.agent_connected) return
      // Guests generally want dimensions that are a multiple of 8.
      const width = viewport.width - (viewport.width % 8)
      const height = viewport.height - (viewport.height % 8)
      const key = `${width}x${height}`
      if (width < 320 || height < 200 || key === this.lastRequested) return
      this.lastRequested = key
      this.sc.resize_window(0, width, height, 32, 0, 0)
    }, delay)
  }

  private setAudioStatus(status: AudioStatus, message?: string): void {
    if (status === this.audioStatus && !message) return
    this.audioStatus = status
    this.events.emit('audio', { status, message })
  }

  /** Make text the guest's clipboard content, owned by the client. */
  private offerClipboard(text: string): void {
    this.clipboardText = text
    this.grabClipboard?.()
  }

  private hookClipboard(sc: SpiceMainConn): void {
    const driver = this
    const events = this.events

    const receive = sc.handle_clipboard_receive
    sc.handle_clipboard_receive = function (agentData) {
      if (!driver.clipboardSharing) return
      receive.call(this, agentData)
      const msg = new SpiceMsgClipboardReceive(agentData, this.agent_caps)
      if (msg.type !== Constants.VD_AGENT_CLIPBOARD_UTF8_TEXT) return
      driver.clipboardText = msg.get_text()
      events.emit('clipboard', driver.clipboardText)
    }

    // spice-html5 claims the guest clipboard whenever the canvas gains focus,
    // promising the local clipboard. That only works if we can read it
    // silently; otherwise the guest keeps its own clipboard, and local text
    // reaches it through Ctrl+V (see interceptPaste) or the sidebar.
    const grab = sc.send_clipboard_grab
    this.grabClipboard = () => grab.call(sc)
    sc.send_clipboard_grab = function () {
      if (driver.canReadClipboard && driver.clipboardSharing) grab.call(this)
    }

    // The guest asks for the clipboard contents it was promised.
    sc.handle_clipboard_send = function () {
      const sendText = (text: string) =>
        this.send_agent_message(
          Constants.VD_AGENT_CLIPBOARD!,
          new SpiceMsgClipboardSend(Constants.VD_AGENT_CLIPBOARD_UTF8_TEXT!, text, this.agent_caps),
        )
      const fallback = () => sendText(driver.clipboardText ?? '')
      if (!driver.clipboardSharing) {
        sendText('')
        return
      }
      // Never read unprompted where the browser would ask the user (Firefox
      // shows a "Paste" popup for every read).
      if (!driver.canReadClipboard) {
        fallback()
        return
      }
      navigator.clipboard.readText().then((text) => {
        driver.clipboardText = text
        sendText(text)
      }, fallback)
    }
  }

  /** Pastes the local clipboard on Ctrl+V (see interceptPaste). */
  private interceptPaste(container: HTMLElement): void {
    this.cleanups.push(
      interceptPaste(container, {
        active: () => !!this.sc?.agent_connected && this.clipboardSharing,
        deliver: (text) => {
          this.offerClipboard(text)
          return new Promise((resolve) => setTimeout(resolve, PASTE_KEY_DELAY_MS))
        },
        sendKeys: (codes) => this.sendKeys(codes),
      }),
    )
  }
}
