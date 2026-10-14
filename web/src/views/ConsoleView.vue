<script setup lang="ts">
import { useEventListener, useFullscreen, useIntervalFn, useTitle } from '@vueuse/core'
import { CircleAlert, PanelLeftOpen, Play, RefreshCw, Unplug, Volume2 } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, toRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, errorMessage } from '@/api'
import ConsoleSidebar from '@/components/console/ConsoleSidebar.vue'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { Spinner } from '@/components/ui/spinner'
import { useConsole } from '@/composables/useConsole'
import { useMachines } from '@/composables/useMachines'
import type { ConsoleProtocol } from '@/consoles/types'
import { vmStatus } from '@/lib/format'
import { savePreview } from '@/lib/previews'

const PREVIEW_INTERVAL_MS = 30_000

const props = defineProps<{ id: string }>()
const route = useRoute()
const router = useRouter()

const viewport = ref<HTMLElement | null>(null)
const root = ref<HTMLElement | null>(null)

// Registered before useConsole so it runs before the console is torn down.
onBeforeUnmount(capturePreview)
// Windows agents can resize the guest when the protocol can't (VNC, or
// SPICE without the SPICE agent).
const ctl = useConsole(
  toRef(props, 'id'),
  viewport,
  // Linux guests follow VNC resize requests natively, so only SPICE (without
  // spice-vdagent) needs the agent there.
  (protocol) =>
    vm.value?.agent?.status === 'connected' &&
    (vm.value.agent.os === 'windows' || (vm.value.agent.os === 'linux' && protocol === 'spice')),
)

const machines = useMachines()
onBeforeUnmount(machines.stop)
const vm = machines.get(props.id)
const loadError = ref<string>()
const panelOpen = ref(false)

useTitle(computed(() => (vm.value ? `${vm.value.name} · Quicksand` : 'Console · Quicksand')))

onMounted(async () => {
  try {
    machines.upsert(await api.vm(props.id))
  } catch (e) {
    loadError.value = errorMessage(e)
  }
})

const running = computed(() => vm.value?.state === 'ready' && vm.value.power === 'running')

// Connect as soon as the VM is running: on arrival, and again after it is
// started from this page. A manual disconnect isn't undone automatically.
let autoConnect = true
watch(
  running,
  (isRunning, wasRunning) => {
    if (isRunning && wasRunning === false) autoConnect = true
    if (!isRunning || !autoConnect || !vm.value) return
    autoConnect = false
    const requested = route.query.protocol as ConsoleProtocol | undefined
    const protocol =
      requested && vm.value.consoles.includes(requested) ? requested : vm.value.consoles[0]
    if (protocol) ctl.connect(protocol)
  },
  { immediate: true },
)

const powerError = ref<string>()
async function powerOn() {
  if (!vm.value) return
  powerError.value = undefined
  try {
    await api.power(vm.value.id, vm.value.power === 'stopped' ? 'start' : 'resume')
  } catch (e) {
    powerError.value = errorMessage(e)
  }
}

function capturePreview() {
  const canvas = ctl.snapshot()
  if (canvas) savePreview(props.id, canvas)
}
useIntervalFn(() => ctl.status.value === 'connected' && capturePreview(), PREVIEW_INTERVAL_MS)
// A first preview once the display has had a moment to draw.
watch(ctl.status, (status) => {
  if (status === 'connected') setTimeout(capturePreview, 3000)
})

// Keep the protocol in the URL so a reload reconnects the same way.
watch(ctl.protocol, (protocol) => {
  if (protocol && route.query.protocol !== protocol) router.replace({ query: { ...route.query, protocol } })
})

// Hand the keyboard back to the guest when the panel closes.
watch(panelOpen, (open) => {
  if (!open) ctl.focus()
})

// Ctrl+Alt+Shift toggles the panel, as in Guacamole.
useEventListener(
  window,
  'keydown',
  (e: KeyboardEvent) => {
    const isModifier = ['Control', 'Alt', 'Shift'].includes(e.key)
    if (isModifier && e.ctrlKey && e.altKey && e.shiftKey) panelOpen.value = !panelOpen.value
  },
  { capture: true },
)

const { isFullscreen, exit: exitFullscreen } = useFullscreen(root)

// In fullscreen, browser shortcuts (Ctrl+W, Ctrl+T, Ctrl+Tab, ...) go to the
// guest instead: Firefox (152+) with the fullscreen keyboardLock option,
// Chromium browsers with the Keyboard Lock API, which also captures Escape
// (hold it to leave fullscreen) and the Windows key. Other browsers still
// keep their own shortcuts.
const keyboard = (navigator as Navigator & { keyboard?: { lock(): Promise<void>; unlock(): void } }).keyboard

async function toggleFullscreen() {
  panelOpen.value = false
  if (document.fullscreenElement) {
    await exitFullscreen()
    return
  }
  try {
    await root.value?.requestFullscreen({ navigationUI: 'hide', keyboardLock: 'browser' } as FullscreenOptions)
  } catch {
    return
  }
  await keyboard?.lock().catch(() => {})
  ctl.focus()
}

// However fullscreen ends (Escape, the browser), give the keys back.
watch(isFullscreen, (full) => {
  if (!full) keyboard?.unlock()
})
</script>

<template>
  <div ref="root" class="dark bg-background relative h-dvh w-full overflow-hidden">
    <!-- The driver renders the guest display in here. The display canvas
         takes focus on hover, which shouldn't draw a focus ring. -->
    <div ref="viewport" class="absolute inset-0 bg-black [&_canvas]:outline-none" />

    <div
      v-if="loadError || !running || ctl.status.value !== 'connected'"
      class="pointer-events-none absolute inset-0 z-[5] flex items-center justify-center"
    >
      <div
        class="bg-background/90 text-foreground pointer-events-auto flex max-w-sm flex-col items-center gap-3 rounded-lg border p-6 text-center shadow-lg backdrop-blur"
      >
        <template v-if="loadError">
          <CircleAlert class="text-destructive size-6" />
          <p class="text-sm font-medium">Couldn't load this virtual machine</p>
          <p class="text-muted-foreground text-xs">{{ loadError }}</p>
          <Button variant="outline" @click="router.push('/')">Back to machines</Button>
        </template>
        <template v-else-if="!vm && machines.loaded.value">
          <Unplug class="text-muted-foreground size-6" />
          <p class="text-sm font-medium">This machine no longer exists</p>
          <Button variant="outline" @click="router.push('/')">Back to machines</Button>
        </template>
        <template v-else-if="!vm">
          <Spinner class="size-5" />
        </template>
        <template v-else-if="vm.state === 'deploying' || vm.state === 'destroying'">
          <p class="text-sm font-medium">{{ vm.state === 'deploying' ? 'Deploying' : 'Destroying' }} {{ vm.name }}</p>
          <p class="text-muted-foreground text-xs">{{ vm.step }}…</p>
          <Progress :model-value="vm.progress * 100" class="h-1.5 w-56" />
        </template>
        <template v-else-if="vm.state === 'failed'">
          <CircleAlert class="text-destructive size-6" />
          <p class="text-sm font-medium">This machine failed</p>
          <p class="text-muted-foreground text-xs">{{ vm.error }}</p>
          <Button variant="outline" @click="router.push('/')">Back to machines</Button>
        </template>
        <template v-else-if="!running">
          <Spinner v-if="vm.action" class="size-5" />
          <p class="text-sm font-medium">{{ vm.name }} is {{ vmStatus(vm).label.toLowerCase() }}</p>
          <p v-if="powerError || vm.actionError" class="text-destructive text-xs">{{ powerError || vm.actionError }}</p>
          <div v-if="!vm.action" class="flex gap-2">
            <Button v-if="vm.power !== 'unknown'" @click="powerOn">
              <Play /> {{ vm.power === 'stopped' ? 'Start' : 'Resume' }}
            </Button>
            <Button variant="outline" @click="router.push('/')">Back to machines</Button>
          </div>
        </template>
        <template v-else-if="ctl.status.value === 'connecting' || ctl.status.value === 'idle'">
          <Spinner class="size-5" />
          <p class="text-sm">
            Connecting{{ ctl.protocol.value ? ` over ${ctl.protocol.value.toUpperCase()}` : '' }}…
          </p>
        </template>
        <template v-else>
          <component
            :is="ctl.status.value === 'error' ? CircleAlert : Unplug"
            class="size-6"
            :class="ctl.status.value === 'error' ? 'text-destructive' : 'text-muted-foreground'"
          />
          <p class="text-sm font-medium">
            {{ ctl.status.value === 'error' ? 'Connection failed' : 'Disconnected' }}
          </p>
          <p v-if="ctl.message.value" class="text-muted-foreground text-xs">{{ ctl.message.value }}</p>
          <div class="flex gap-2">
            <Button @click="ctl.reconnect()"><RefreshCw /> Reconnect</Button>
            <Button variant="outline" @click="panelOpen = true"><PanelLeftOpen /> Options</Button>
          </div>
        </template>
      </div>
    </div>

    <button
      v-if="ctl.status.value === 'connected' && ctl.audioStatus.value === 'blocked' && !ctl.audio.value.muted"
      type="button"
      class="bg-background/90 text-foreground hover:bg-background absolute top-3 left-1/2 z-[5] flex -translate-x-1/2 items-center gap-2 rounded-full border px-3 py-1.5 text-xs shadow-lg backdrop-blur"
      @click="ctl.resumeAudio(); ctl.focus()"
    >
      <Volume2 class="size-3.5" /> Your browser blocked the guest's audio. Click to enable.
    </button>

    <ConsoleSidebar
      v-model:open="panelOpen"
      :console="ctl"
      :vm="vm ?? null"
      :fullscreen="isFullscreen"
      @toggle-fullscreen="toggleFullscreen"
    />
  </div>
</template>
