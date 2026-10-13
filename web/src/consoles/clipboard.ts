// Whether the page may read the local clipboard without user interaction.
//
// Chromium exposes this as the "clipboard-read" permission; once granted,
// navigator.clipboard.readText() is silent. Firefox and Safari have no such
// permission and show a "Paste" prompt on every read, so consoles must not
// read the clipboard on their own there.

export type ClipboardReadState = 'granted' | 'prompt' | 'denied' | 'unsupported'

/** Calls onChange with the current state now and whenever it changes. Returns a stop function. */
export function watchClipboardRead(onChange: (state: ClipboardReadState) => void): () => void {
  let status: PermissionStatus | undefined
  let stopped = false
  const report = () => onChange((status?.state as ClipboardReadState | undefined) ?? 'unsupported')

  if (!navigator.clipboard?.readText || !navigator.permissions) {
    onChange('unsupported')
  } else {
    navigator.permissions
      .query({ name: 'clipboard-read' as PermissionName })
      .then((s) => {
        if (stopped) return
        status = s
        s.addEventListener('change', report)
        report()
      })
      // Browsers without the permission reject the query.
      .catch(() => !stopped && onChange('unsupported'))
  }

  return () => {
    stopped = true
    status?.removeEventListener('change', report)
  }
}

/** Ask for clipboard-read permission (Chromium shows a permission prompt). */
export async function requestClipboardRead(): Promise<boolean> {
  try {
    await navigator.clipboard.readText()
    return true
  } catch {
    return false
  }
}
