// Pastes the local clipboard into the guest on Ctrl+V, and only then: the
// keydown is held back from the console so the browser runs its native
// paste (which never prompts, unlike the Clipboard API), the text goes to
// the guest clipboard, and then the V keystroke follows so the guest
// application pastes it. The guest never sees the local clipboard
// otherwise.

// How long to wait for the browser's paste event before passing the
// keystroke on regardless.
const PASTE_EVENT_TIMEOUT_MS = 150

export interface PasteTarget {
  /** Whether to intercept Ctrl+V now. */
  active(): boolean
  /** Put the text on the guest clipboard; the keystroke follows when it settles. */
  deliver(text: string): Promise<void>
  /** Press keys in order, then release them (KeyboardEvent.code names). */
  sendKeys(codes: string[]): Promise<void>
}

/** Intercepts Ctrl+V in container. Returns a function that stops it. */
export function interceptPaste(container: HTMLElement, target: PasteTarget): () => void {
  let pending: ReturnType<typeof setTimeout> | undefined
  // Whether the user is still holding Ctrl, which the guest also sees held.
  let ctrlHeld = false
  // A quick Ctrl+V can be over before the keystroke is sent; the guest then
  // needs Ctrl pressed for it, or it would type a plain "v".
  const pressV = () => void target.sendKeys(ctrlHeld ? ['KeyV'] : ['ControlLeft', 'KeyV'])
  const trackCtrl = (e: KeyboardEvent) => (ctrlHeld = e.ctrlKey)

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.code !== 'KeyV' || !e.ctrlKey || e.altKey || e.metaKey || e.repeat) return
    if (!target.active()) return
    // Keep the console from seeing (and cancelling) this keydown, so the
    // browser fires a paste event. Ctrl itself has already reached the guest.
    e.stopPropagation()
    clearTimeout(pending)
    pending = setTimeout(() => {
      pending = undefined
      pressV()
    }, PASTE_EVENT_TIMEOUT_MS)
  }

  const onPaste = (e: ClipboardEvent) => {
    if (pending === undefined) return
    clearTimeout(pending)
    pending = undefined
    e.preventDefault()
    const text = e.clipboardData?.getData('text/plain') ?? ''
    if (!text) {
      pressV()
      return
    }
    target.deliver(text).then(pressV, pressV)
  }

  window.addEventListener('keydown', trackCtrl, { capture: true })
  window.addEventListener('keyup', trackCtrl, { capture: true })
  container.addEventListener('keydown', onKeyDown, { capture: true })
  // Firefox fires the paste event at the document body rather than at the
  // focused canvas, so it has to be caught at the document.
  document.addEventListener('paste', onPaste, { capture: true })
  return () => {
    clearTimeout(pending)
    window.removeEventListener('keydown', trackCtrl, { capture: true })
    window.removeEventListener('keyup', trackCtrl, { capture: true })
    container.removeEventListener('keydown', onKeyDown, { capture: true })
    document.removeEventListener('paste', onPaste, { capture: true })
  }
}
