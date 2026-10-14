/**
 * The frames shell streams carry (see internal/agentproto/frames.go): a
 * type byte, a big-endian uint32 payload length, then the payload.
 */
export const Frame = {
  Stdin: 1,
  StdinEOF: 2,
  Resize: 3,
  Stdout: 16,
  Stderr: 17,
  Exit: 18,
} as const

export interface ShellExit {
  /** -1 if the command couldn't run or was killed. */
  code: number
  error?: string
}

const encoder = new TextEncoder()

export function encodeFrame(type: number, payload: Uint8Array | string = new Uint8Array()): Uint8Array<ArrayBuffer> {
  const body = typeof payload === 'string' ? encoder.encode(payload) : payload
  const out = new Uint8Array(5 + body.length)
  out[0] = type
  new DataView(out.buffer).setUint32(1, body.length)
  out.set(body, 5)
  return out
}

/** Splits a byte stream, arriving in arbitrary pieces, into frames. */
export class FrameDecoder {
  private buf = new Uint8Array(0)

  push(chunk: Uint8Array, onFrame: (type: number, payload: Uint8Array) => void) {
    const joined = new Uint8Array(this.buf.length + chunk.length)
    joined.set(this.buf)
    joined.set(chunk, this.buf.length)
    let off = 0
    while (joined.length - off >= 5) {
      const len = new DataView(joined.buffer, joined.byteOffset + off + 1, 4).getUint32(0)
      if (joined.length - off - 5 < len) break
      onFrame(joined[off]!, joined.subarray(off + 5, off + 5 + len))
      off += 5 + len
    }
    this.buf = joined.slice(off)
  }
}
