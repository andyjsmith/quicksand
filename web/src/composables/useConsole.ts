import { useLocalStorage } from '@vueuse/core'
import { onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'
import { api } from '@/api'
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
 */
export function useConsole(vmId: Ref<string>, container: Ref<HTMLElement | null>, canResizeGuest?: (protocol: ConsoleProtocol) => boolean) {
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
      const id = vmId.value
      d.connect(container.value, session, {
        scaleMode: scaleMode.value,
        audio: { ...audio.value },
        resizeGuest: canResizeGuest?.(to) ? (size) => api.setDisplay(id, size.width, size.height) : undefined,
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

  watch(scaleMode, (mode) => driver.value?.setScaleMode(mode))
  watch(audio, (a) => driver.value?.setAudio({ ...a }), { deep: true })

  onBeforeUnmount(() => {
    generation++
    teardown()
  })

  return {
    protocol,
    status,
    message,
    capabilities,
    resolution,
    guestClipboard,
    audioStatus,
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
    sendClipboard: (text: string) => driver.value?.sendClipboard(text) ?? Promise.resolve(),
  }
}

export type ConsoleController = ReturnType<typeof useConsole>
