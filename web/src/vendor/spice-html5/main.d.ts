// Type declarations for the parts of spice-html5 Quicksand uses.

export interface SpiceMainConnOptions {
  uri: string
  password: string
  screen_id?: string
  message_id?: string
  dump_id?: string
  onerror?: (this: SpiceMainConn, e: Error | Event) => void
  onagent?: (this: SpiceMainConn, sc: SpiceMainConn) => void
  onsuccess?: (this: SpiceMainConn) => void
}

export interface SpiceChannel {
  state: string
  ws?: WebSocket
  send_msg(msg: unknown): void
}

export interface SpicePlaybackConn extends SpiceChannel {
  audio?: HTMLAudioElement
}

export class SpiceMainConn implements SpiceChannel {
  constructor(options: SpiceMainConnOptions)
  state: string
  ws?: WebSocket
  agent_connected?: boolean
  agent_caps: number[]
  inputs?: SpiceChannel
  display?: SpiceChannel
  playback?: SpicePlaybackConn
  send_msg(msg: unknown): void
  stop(): void
  resize_window(flags: number, width: number, height: number, depth: number, x: number, y: number): void
  send_agent_message(type: number, message: unknown): void
  send_clipboard_grab(): void
  handle_clipboard_receive(agentData: unknown): void
  handle_clipboard_send(): void
}
