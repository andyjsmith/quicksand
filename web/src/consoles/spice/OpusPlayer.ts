import type { PlaybackSink } from '@/vendor/spice-html5/main.js'
import type { AudioSettings, AudioStatus } from '../types'

// How far ahead of the audio clock playback starts (and restarts after an
// underrun): enough to absorb network jitter between 10 ms packets.
const START_LEAD_S = 0.08
// If queued audio runs further ahead than this, skip forward rather than
// let latency grow.
const MAX_LEAD_S = 0.4

/** Whether this browser can decode SPICE's Opus audio with WebCodecs. */
export const opusPlayerSupported = () => typeof AudioDecoder !== 'undefined' && typeof AudioContext !== 'undefined'

/**
 * Plays SPICE Opus audio by decoding packets with WebCodecs and scheduling
 * them on a Web Audio clock. This replaces spice-html5's WebM/MediaSource
 * player, which Firefox cannot play live.
 */
export class OpusPlayer implements PlaybackSink {
  private ctx = new AudioContext({ latencyHint: 'interactive' })
  private gain = this.ctx.createGain()
  private decoder?: AudioDecoder
  private streaming = false
  private nextTime = 0
  private status: AudioStatus = 'idle'

  constructor(
    settings: AudioSettings,
    private onStatus: (status: AudioStatus, message?: string) => void,
  ) {
    this.gain.connect(this.ctx.destination)
    this.setSettings(settings)
    this.ctx.addEventListener('statechange', () => this.updateStatus())
  }

  start(frequency: number, channels: number): void {
    this.decoder?.close()
    this.decoder = new AudioDecoder({
      output: (data) => this.schedule(data),
      error: (e) => this.setStatus('error', e.message),
    })
    this.decoder.configure({ codec: 'opus', sampleRate: frequency, numberOfChannels: channels })
    this.streaming = true
    this.nextTime = 0
    // Succeeds without a gesture if the user has already interacted with the page.
    this.ctx.resume().catch(() => {})
    this.updateStatus()
  }

  data(time: number, packet: ArrayBuffer): void {
    if (this.decoder?.state !== 'configured') return
    this.decoder.decode(new EncodedAudioChunk({ type: 'key', timestamp: time * 1000, data: packet }))
  }

  stop(): void {
    this.decoder?.close()
    this.decoder = undefined
    this.streaming = false
    this.updateStatus()
  }

  /** Start the audio clock if the browser blocked it. Call from a user gesture. */
  resume(): void {
    this.ctx.resume().catch(() => {})
  }

  setSettings(settings: AudioSettings): void {
    this.gain.gain.value = settings.muted ? 0 : settings.volume
  }

  close(): void {
    this.decoder?.close()
    this.decoder = undefined
    this.streaming = false
    this.ctx.close().catch(() => {})
  }

  private schedule(data: AudioData): void {
    try {
      // Audio decoded while the clock is suspended would only pile up.
      if (this.ctx.state !== 'running') return
      const buffer = this.ctx.createBuffer(data.numberOfChannels, data.numberOfFrames, data.sampleRate)
      for (let ch = 0; ch < data.numberOfChannels; ch++) {
        const plane = new Float32Array(data.numberOfFrames)
        data.copyTo(plane, { planeIndex: ch, format: 'f32-planar' })
        buffer.copyToChannel(plane, ch)
      }
      const now = this.ctx.currentTime
      // Resync after an underrun (or at the start), and skip ahead if we
      // have fallen too far behind the guest.
      if (this.nextTime < now || this.nextTime > now + MAX_LEAD_S) this.nextTime = now + START_LEAD_S
      const source = this.ctx.createBufferSource()
      source.buffer = buffer
      source.connect(this.gain)
      source.start(this.nextTime)
      this.nextTime += buffer.duration
    } finally {
      data.close()
    }
  }

  private updateStatus(): void {
    if (!this.streaming) this.setStatus('idle')
    else this.setStatus(this.ctx.state === 'running' ? 'playing' : 'blocked')
  }

  private setStatus(status: AudioStatus, message?: string): void {
    if (status === this.status && !message) return
    this.status = status
    this.onStatus(status, message)
  }
}
