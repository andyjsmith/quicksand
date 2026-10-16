import { useLocalStorage } from '@vueuse/core'
import { computed, onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'
import { api, ApiError } from '@/api'
import { interceptPaste } from '@/consoles/paste'
import { consoleDrivers } from '@/consoles/registry'
import type {
  AudioSettings,
  AudioStatus,
  ConsoleCapabilities,
  ConsoleDriver,
  ConsoleProtocol,
  ConsoleStatus,
  DisplaySize,
  ScaleMode,
} from '@/consoles/types'

/**
 * Drives a console in `container` through the common ConsoleDriver
 * interface, whatever the protocol. Each connection uses a fresh session,
 * since Proxmox tickets are single-use and short-lived.
 */
/** vmId is the Quicksand VM to connect to. */
/**
 * canResizeGuest says whether the VM's agent can change the guest
 * resolution, for the remote scale mode where the protocol can't.
 * agentConnected says whether the VM's agent is up, to share the clipboard
 * through it when the protocol can't.
 */
export function useConsole(
  vmId: Ref<string>,
  container: Ref<HTMLElement | null>,
  canResizeGuest?: (protocol: ConsoleProtocol) => 'prefer' | 'fallback' | false,
  agentConnected?: () => boolean,
) {
  const protocol = ref<ConsoleProtocol | null>(null)
  const status = ref<ConsoleStatus>('idle')
  const message = ref<string>()
  const capabilities = ref<ConsoleCapabilities | null>(null)
  const resolution = ref<DisplaySize | null>(null)
  const guestClipboard = ref<string | null>(null)
  const audioStatus = ref<AudioStatus>('idle')
  const driver = shallowRef<ConsoleDriver | null>(null)

  const scaleMode = useLocalStorage<ScaleMode>('quicksand.scaleMode', 'remote')
  const audio = useLocalStorage<AudioSettings>('quicksand.audio', { muted: false, volume: 0.8 })
  /** Automatic clipboard sharing with the guest, whichever way it's done. */
  const clipboardSharing = useLocalStorage('quicksand.clipboardSharing', true)

  // Guards against a slower, superseded connect() finishing last.
  let generation = 0

  async function connect(to: ConsoleProtocol) {
    const gen = ++generation
    teardown()
    protocol.value = to
    status.value = 'connecting'
    message.value = undefined
    resolution.value = null
    guestClipboard.value = null
    agentClipboardError.value = undefined
    audioStatus.value = 'idle'
    try {
      const [session, d] = await Promise.all([
        api.createConsole(vmId.value, to),
        consoleDrivers[to].create(),
      ])
      if (gen !== generation || !container.value) return
      capabilities.value = d.capabilities
      d.on('status', (s) => {
        status.value = s.status
        message.value = s.message
      })
      d.on('capabilities', (c) => (capabilities.value = c))
      d.on('resolution', (r) => (resolution.value = r))
      d.on('clipboard', (text) => (guestClipboard.value = text))
      d.on('audio', (a) => (audioStatus.value = a.status))
      driver.value = d
      d.setClipboardSharing(clipboardSharing.value)
      const id = vmId.value
      const guestResize = canResizeGuest?.(to) ?? false
      d.connect(container.value, session, {
        scaleMode: scaleMode.value,
        audio: { ...audio.value },
        resizeGuest: guestResize ? (size) => api.setDisplay(id, size.width, size.height) : undefined,
        preferResizeGuest: guestResize === 'prefer',
      })
    } catch (e) {
      if (gen !== generation) return
      status.value = 'error'
      message.value = e instanceof Error ? e.message : String(e)
    }
  }

  function teardown() {
    driver.value?.disconnect()
    driver.value = null
  }

  function disconnect() {
    generation++
    teardown()
    status.value = 'disconnected'
    message.value = undefined
  }

  function reconnect() {
    if (protocol.value) connect(protocol.value)
  }

  // The clipboard through the Quicksand agent, when the protocol can't
  // share it (VNC, or SPICE without spice-vdagent). Copies in the guest
  // reach the local clipboard; the local clipboard reaches the guest only
  // when the user presses Ctrl+V in the console.
  const agentClipboardError = ref<string>()
  const agentClipboard = computed(
    () =>
      status.value === 'connected' &&
      !capabilities.value?.clipboardSync &&
      !!agentConnected?.() &&
      !agentClipboardError.value &&
      clipboardSharing.value,
  )
  // The text last pasted, which comes back as a guest clipboard change and
  // mustn't overwrite whatever is copied locally since.
  let lastPasted: string | null = null
  let stopAgentClipboard: (() => void) | undefined
  watch(agentClipboard, (on) => {
    stopAgentClipboard?.()
    stopAgentClipboard = undefined
    if (!on || !container.value) return
    const id = vmId.value
    const abort = new AbortController()
    watchGuestClipboard(id, abort.signal)
    const stopPaste = interceptPaste(container.value, {
      active: () => agentClipboard.value,
      deliver: async (text) => {
        lastPasted = text
        await api.setClipboard(id, text)
      },
      sendKeys: (codes) => driver.value?.sendKeys(codes) ?? Promise.resolve(),
    })
    stopAgentClipboard = () => {
      abort.abort()
      stopPaste()
    }
  })

  async function watchGuestClipboard(id: string, signal: AbortSignal) {
    let seq = 0
    let first = true
    while (!signal.aborted) {
      try {
        // The first answer is what's there already: only later copies count.
        const clip = await api.clipboard(id, seq, first ? 0 : 25, signal)
        if (!first && clip.seq !== seq && clip.text && clip.text !== lastPasted) {
          guestClipboard.value = clip.text
          // Chromium allows this while the page has focus; elsewhere the
          // sidebar offers a Copy button.
          if (document.hasFocus()) navigator.clipboard?.writeText(clip.text).catch(() => {})
        }
        seq = clip.seq
        first = false
      } catch (e) {
        if (signal.aborted) return
        if (e instanceof ApiError && (e.status === 404 || e.status === 501)) {
          // No clipboard to reach (no desktop session, or no tool for it on
          // Linux), or an agent from before clipboard sharing.
          agentClipboardError.value = e.status === 404 ? "This VM's agent is too old to share the clipboard." : e.message
          return
        }
        await new Promise((r) => setTimeout(r, 5000))
      }
    }
  }

  // Whenever the agent comes (back) up after connecting (the VM was still
  // booting, or the agent restarted), resize through it afresh: a resizer
  // that failed while the agent was down gives up.
  watch(
    () => !!agentConnected?.(),
    (up, wasUp) => {
      if (!up || wasUp || !driver.value || !protocol.value) return
      const mode = canResizeGuest?.(protocol.value)
      if (!mode) return
      const id = vmId.value
      driver.value.setGuestResize((size) => api.setDisplay(id, size.width, size.height), mode === 'prefer')
    },
  )

  watch(clipboardSharing, (on) => driver.value?.setClipboardSharing(on))
  watch(scaleMode, (mode) => driver.value?.setScaleMode(mode))
  watch(audio, (a) => driver.value?.setAudio({ ...a }), { deep: true })

  onBeforeUnmount(() => {
    generation++
    stopAgentClipboard?.()
    teardown()
  })

  return {
    protocol,
    status,
    message,
    capabilities,
    resolution,
    guestClipboard,
    /** The clipboard is shared through the Quicksand agent. */
    agentClipboard,
    /** Why the agent can't share the clipboard, when it tried. */
    agentClipboardError,
    audioStatus,
    clipboardSharing,
    scaleMode,
    audio,
    connect,
    disconnect,
    reconnect,
    focus: () => driver.value?.focus(),
    snapshot: () => driver.value?.snapshot() ?? null,
    resumeAudio: () => driver.value?.resumeAudio(),
    sendKeys: (codes: string[]) => driver.value?.sendKeys(codes) ?? Promise.resolve(),
    typeText: (text: string) => driver.value?.typeText(text) ?? Promise.resolve({ skipped: [] }),
    sendClipboard: (text: string) =>
      agentClipboard.value
        ? api.setClipboard(vmId.value, text)
        : (driver.value?.sendClipboard(text) ?? Promise.resolve()),
  }
}

export type ConsoleController = ReturnType<typeof useConsole>
