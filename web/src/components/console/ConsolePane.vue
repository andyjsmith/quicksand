<script setup lang="ts">
import { useIntervalFn } from '@vueuse/core'
import { CircleAlert, PanelLeftOpen, Play, RefreshCw, Unplug, Volume2 } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, toRef, useTemplateRef, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, errorMessage } from '@/api'
import ConsoleDropUpload from '@/components/console/ConsoleDropUpload.vue'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { Spinner } from '@/components/ui/spinner'
import { useConsole } from '@/composables/useConsole'
import { useMachines } from '@/composables/useMachines'
import type { ConsoleProtocol } from '@/consoles/types'
import { vmStatus } from '@/lib/format'
import { savePreview } from '@/lib/previews'

// One VM's console in the console workspace: its connection, status and
// file drops. Panes stay mounted while their tab is hidden, so switching
// back is instant.

const PREVIEW_INTERVAL_MS = 30_000

const props = defineProps<{
  vmId: string
  /** The protocol to connect with first, if the VM supports it. */
  protocol?: ConsoleProtocol
}>()
const emit = defineEmits<{ protocol: [protocol: ConsoleProtocol]; openPanel: [] }>()

const router = useRouter()
const viewport = useTemplateRef('viewport')
const pane = useTemplateRef('pane')

const machines = useMachines()
onBeforeUnmount(machines.stop)
const vm = machines.get(props.vmId)
const loadError = ref<string>()

// Registered before useConsole so it runs before the console is torn down.
onBeforeUnmount(capturePreview)
// The Quicksand agent can resize the guest: preferred on Windows, whose
// SPICE agent only manages custom sizes with QXL displays (and VNC can't);
// on Linux the protocols resize exactly themselves, so it's only for SPICE
// without spice-vdagent.
const ctl = useConsole(toRef(props, 'vmId'), viewport, (protocol) => {
  const agent = vm.value?.agent
  if (agent?.status !== 'connected') return false
  if (agent.os === 'windows') return 'prefer'
  return agent.os === 'linux' && protocol === 'spice' ? 'fallback' : false
}, () => vm.value?.agent?.status === 'connected')

onMounted(async () => {
  try {
    machines.upsert(await api.vm(props.vmId))
  } catch (e) {
    loadError.value = errorMessage(e)
  }
})

const running = computed(() => vm.value?.state === 'ready' && vm.value.power === 'running')

// Connect as soon as the VM is running: on arrival, and again after it is
// started from here. A manual disconnect isn't undone automatically.
let autoConnect = true
watch(
  running,
  (isRunning, wasRunning) => {
    if (isRunning && wasRunning === false) autoConnect = true
    if (!isRunning || !autoConnect || !vm.value) return
    autoConnect = false
    const protocol =
      props.protocol && vm.value.consoles.includes(props.protocol) ? props.protocol : vm.value.consoles[0]
    if (protocol) ctl.connect(protocol)
  },
  { immediate: true },
)

// A revert restarts the VM's QEMU, which the console would report as a
// broken connection: disconnect cleanly first, and connect again when the
// revert is done (the VM comes back up, from wherever it was reverted).
// (Until it reconnects, the pane says it's reverting.)
const reverting = ref(false)
watch(
  () => vm.value?.action,
  (action, prev) => {
    if (action === 'revert') {
      if (ctl.status.value === 'connected' || ctl.status.value === 'connecting') {
        reverting.value = true
        ctl.disconnect()
      }
      return
    }
    if (prev !== 'revert' || !reverting.value) return
    // If the VM is seen coming back up, the running watch connects; this
    // covers it staying up throughout (a snapshot with memory).
    autoConnect = true
    setTimeout(() => {
      reverting.value = false
      if (running.value && ctl.status.value !== 'connected' && ctl.status.value !== 'connecting') {
        autoConnect = false
        ctl.reconnect()
      }
    }, 1500)
  },
)

watch(ctl.protocol, (protocol) => protocol && emit('protocol', protocol))

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
  if (canvas) savePreview(props.vmId, canvas)
}
useIntervalFn(() => ctl.status.value === 'connected' && capturePreview(), PREVIEW_INTERVAL_MS)
// A first preview once the display has had a moment to draw.
watch(ctl.status, (status) => {
  if (status === 'connected') setTimeout(capturePreview, 3000)
})

const dropUpload = useTemplateRef('dropUpload')
defineExpose({ ctl, vm, upload: () => dropUpload.value?.pickFiles() })
</script>

<template>
  <div ref="pane" class="absolute overflow-hidden">
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
        <template v-else-if="vm.action === 'revert' || reverting">
          <Spinner class="size-5" />
          <p class="text-sm">Reverting to the snapshot…</p>
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
            <Button variant="outline" @click="emit('openPanel')"><PanelLeftOpen /> Options</Button>
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

    <ConsoleDropUpload ref="dropUpload" :vm="vm ?? null" :target="pane" @done="ctl.focus()" />
  </div>
</template>
