// Type declarations for the parts of noVNC's RFB API Quicksand uses.
// See node_modules/@novnc/novnc/docs/API.md.
declare module '@novnc/novnc' {
  export interface RFBOptions {
    shared?: boolean
    credentials?: { username?: string; password?: string; target?: string }
    repeaterID?: string
    wsProtocols?: string[]
  }

  export interface RFBEventMap {
    connect: CustomEvent<Record<string, never>>
    disconnect: CustomEvent<{ clean: boolean }>
    credentialsrequired: CustomEvent<{ types: string[] }>
    securityfailure: CustomEvent<{ status: number; reason?: string }>
    clipboard: CustomEvent<{ text: string }>
    bell: CustomEvent<Record<string, never>>
    desktopname: CustomEvent<{ name: string }>
    capabilities: CustomEvent<{ capabilities: { power: boolean } }>
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions)
    viewOnly: boolean
    focusOnClick: boolean
    clipViewport: boolean
    scaleViewport: boolean
    resizeSession: boolean
    showDotCursor: boolean
    background: string
    qualityLevel: number
    compressionLevel: number
    readonly capabilities: { power: boolean }

    disconnect(): void
    sendCredentials(credentials: { username?: string; password?: string; target?: string }): void
    sendKey(keysym: number, code: string | null, down?: boolean): void
    sendCtrlAltDel(): void
    focus(options?: FocusOptions): void
    blur(): void
    clipboardPasteFrom(text: string): void

    addEventListener<K extends keyof RFBEventMap>(type: K, listener: (event: RFBEventMap[K]) => void): void
    removeEventListener<K extends keyof RFBEventMap>(type: K, listener: (event: RFBEventMap[K]) => void): void
  }
}
