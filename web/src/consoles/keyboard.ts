// Keyboard helpers shared by display consoles that inject key events
// (KeyboardEvent.code names) into the guest.

/** X11 keysyms for non-character keys, used by protocols that need them (RFB). */
const NAMED_KEYSYMS: Record<string, number> = {
  Backspace: 0xff08,
  Tab: 0xff09,
  Enter: 0xff0d,
  Escape: 0xff1b,
  Space: 0x0020,
  Delete: 0xffff,
  Insert: 0xff63,
  Home: 0xff50,
  End: 0xff57,
  PageUp: 0xff55,
  PageDown: 0xff56,
  ArrowLeft: 0xff51,
  ArrowUp: 0xff52,
  ArrowRight: 0xff53,
  ArrowDown: 0xff54,
  PrintScreen: 0xff61,
  ShiftLeft: 0xffe1,
  ShiftRight: 0xffe2,
  ControlLeft: 0xffe3,
  ControlRight: 0xffe4,
  AltLeft: 0xffe9,
  AltRight: 0xffea,
  MetaLeft: 0xffeb,
  MetaRight: 0xffec,
  ContextMenu: 0xff67,
}
for (let i = 1; i <= 12; i++) NAMED_KEYSYMS[`F${i}`] = 0xffbe + i - 1

/** Character → key on a US layout, and whether Shift is needed. */
const US_LAYOUT = new Map<string, { code: string; shift: boolean }>()
for (const ch of 'abcdefghijklmnopqrstuvwxyz') {
  const code = `Key${ch.toUpperCase()}`
  US_LAYOUT.set(ch, { code, shift: false })
  US_LAYOUT.set(ch.toUpperCase(), { code, shift: true })
}
'1234567890'.split('').forEach((d, i) => {
  US_LAYOUT.set(d, { code: `Digit${d}`, shift: false })
  US_LAYOUT.set('!@#$%^&*()'[i]!, { code: `Digit${d}`, shift: true })
})
for (const [plain, shifted, code] of [
  ['-', '_', 'Minus'],
  ['=', '+', 'Equal'],
  ['[', '{', 'BracketLeft'],
  [']', '}', 'BracketRight'],
  ['\\', '|', 'Backslash'],
  [';', ':', 'Semicolon'],
  ["'", '"', 'Quote'],
  [',', '<', 'Comma'],
  ['.', '>', 'Period'],
  ['/', '?', 'Slash'],
  ['`', '~', 'Backquote'],
] as const) {
  US_LAYOUT.set(plain, { code, shift: false })
  US_LAYOUT.set(shifted, { code, shift: true })
}
US_LAYOUT.set(' ', { code: 'Space', shift: false })
US_LAYOUT.set('\n', { code: 'Enter', shift: false })
US_LAYOUT.set('\t', { code: 'Tab', shift: false })

/** The keysym a key produces (unshifted), or undefined if unknown. */
export function keysymFor(code: string): number | undefined {
  const named = NAMED_KEYSYMS[code]
  if (named !== undefined) return named
  for (const [ch, key] of US_LAYOUT) {
    if (key.code === code && !key.shift) return ch.codePointAt(0)
  }
  return undefined
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

// Guests (Windows in particular) drop keys whose release arrives in the same
// instant as the press, so every key is held briefly.
const KEY_HOLD_MS = 15
const TYPE_DELAY_MS = 20

/**
 * Sends key events for a keyboard-backed console. `send` delivers a single
 * press or release and returns false once the connection is gone.
 */
export type KeySender = (code: string, down: boolean, keysym?: number) => boolean

export async function sendKeyCombo(send: KeySender, codes: string[]): Promise<void> {
  const pressed: string[] = []
  for (const code of codes) {
    if (!send(code, true, keysymFor(code))) break
    pressed.push(code)
    await sleep(KEY_HOLD_MS)
  }
  for (const code of pressed.reverse()) {
    send(code, false, keysymFor(code))
    await sleep(KEY_HOLD_MS)
  }
}

/**
 * Types text using a US keyboard layout. Characters that layout cannot
 * produce are skipped and reported.
 */
export async function typeWithKeys(send: KeySender, text: string): Promise<{ skipped: string[] }> {
  const skipped = new Set<string>()
  for (const ch of text.replace(/\r\n?/g, '\n')) {
    const key = US_LAYOUT.get(ch)
    if (!key) {
      skipped.add(ch)
      continue
    }
    const keysym = ch === '\n' ? NAMED_KEYSYMS.Enter : ch === '\t' ? NAMED_KEYSYMS.Tab : ch.codePointAt(0)
    if (key.shift) {
      if (!send('ShiftLeft', true, NAMED_KEYSYMS.ShiftLeft)) break
      await sleep(KEY_HOLD_MS)
    }
    const sent = send(key.code, true, keysym)
    if (sent) {
      await sleep(KEY_HOLD_MS)
      send(key.code, false, keysym)
    }
    if (key.shift) {
      await sleep(KEY_HOLD_MS)
      send('ShiftLeft', false, NAMED_KEYSYMS.ShiftLeft)
    }
    if (!sent) break
    await sleep(TYPE_DELAY_MS)
  }
  return { skipped: [...skipped] }
}
