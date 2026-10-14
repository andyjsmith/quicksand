// The common console interface. Every display method (SPICE, VNC, and later
// text consoles) implements ConsoleDriver; the console view and sidebar only
// talk to this interface and adapt to the advertised capabilities.

export type ConsoleProtocol = 'spice' | 'vnc'

export type ConsoleStatus = 'idle' | 'connecting' | 'connected' | 'disconnected' | 'error'

/**
 * How the guest display is fitted into the viewport.
 * - remote: ask the guest to change its resolution to match the viewport
 * - fit: scale the guest display to fit the viewport
 * - none: show the display at its native size, scrolling if needed
 */
export type ScaleMode = 'remote' | 'fit' | 'none'

export interface ConsoleCapabilities {
  /** Scale modes usable right now, in order of preference. */
  scaleModes: ScaleMode[]
  /** The guest clipboard follows the local clipboard automatically. */
  clipboardSync: boolean
  /** Text can be placed on the guest clipboard explicitly. */
  clipboardSend: boolean
  /** Guest audio can be played. */
  audio: boolean
  /** Key combinations can be sent. */
  keyCombos: boolean
  /** Text can be typed into the guest as keystrokes. */
  typeText: boolean
}

/** A console session as issued by the server. */
export interface ConsoleSession {
  id: string
  protocol: ConsoleProtocol
  password: string
  title: string
  /** Websocket path on the Quicksand server. */
  path: string
}

export interface DisplaySize {
  width: number
  height: number
}

/**
 * Guest audio playback.
 * - idle: the guest is not sending audio
 * - playing: audio is reaching the speakers
 * - blocked: the browser refused to start playback until the user interacts
 * - error: playback failed
 */
export type AudioStatus = 'idle' | 'playing' | 'blocked' | 'error'

export interface ConsoleEvents {
  status: { status: ConsoleStatus; message?: string }
  capabilities: ConsoleCapabilities
  /** Text the guest placed on its clipboard. */
  clipboard: string
  /** The guest display resolution changed. */
  resolution: DisplaySize
  audio: { status: AudioStatus; message?: string }
}

export interface AudioSettings {
  muted: boolean
  /** 0..1 */
  volume: number
}

export interface ConnectOptions {
  scaleMode: ScaleMode
  audio: AudioSettings
  /**
   * Changes the guest resolution another way (through the Quicksand agent),
   * for the remote scale mode when the protocol can't.
   */
  resizeGuest?: (size: DisplaySize) => Promise<unknown>
}

export interface ConsoleDriver {
  readonly protocol: ConsoleProtocol
  readonly capabilities: ConsoleCapabilities

  on<K extends keyof ConsoleEvents>(event: K, handler: (payload: ConsoleEvents[K]) => void): () => void

  /** Attach to a container element and start connecting. */
  connect(container: HTMLElement, session: ConsoleSession, options: ConnectOptions): void
  /** Close the connection and remove everything added to the container. */
  disconnect(): void
  /** Give keyboard focus to the console. */
  focus(): void

  /** Press keys in order, then release them in reverse (KeyboardEvent.code names). */
  sendKeys(codes: string[]): Promise<void>
  /** Type text as keystrokes. Resolves with characters that could not be typed. */
  typeText(text: string): Promise<{ skipped: string[] }>
  /** Put text on the guest clipboard. */
  sendClipboard(text: string): Promise<void>
  setScaleMode(mode: ScaleMode): void
  setAudio(settings: AudioSettings): void
  /** Retry audio playback the browser blocked. Call from a user gesture. */
  resumeAudio(): void
  /** The canvas showing the guest display, for preview screenshots. */
  snapshot(): HTMLCanvasElement | null
}
