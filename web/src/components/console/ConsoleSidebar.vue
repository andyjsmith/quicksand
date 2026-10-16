<script setup lang="ts">
import {
  ArrowLeft,
  Camera,
  CircleAlert,
  ChevronLeft,
  ChevronRight,
  Clipboard,
  ClipboardX,
  Copy,
  History,
  Keyboard,
  Maximize,
  Minimize,
  Monitor,
  Plug,
  RefreshCw,
  RotateCcw,
  Save,
  Send,
  SquareTerminal,
  Upload,
  Unplug,
  Volume2,
  VolumeX,
  SquarePlus,
} from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api, errorMessage, type VM } from '@/api'
import ActionProgressBar from '@/components/ActionProgressBar.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Spinner } from '@/components/ui/spinner'
import { Kbd } from '@/components/ui/kbd'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import type { ConsoleController } from '@/composables/useConsole'
import { type ClipboardReadState, requestClipboardRead, watchClipboardRead } from '@/consoles/clipboard'
import { consoleDrivers } from '@/consoles/registry'
import { actionLabels, formatAgo } from '@/lib/format'
import type { ConsoleProtocol, ScaleMode } from '@/consoles/types'
import AgentPanel from './AgentPanel.vue'
import SidebarSection from './SidebarSection.vue'

const props = defineProps<{
  console: ConsoleController
  vm: VM | null
  fullscreen: boolean
}>()

const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ toggleFullscreen: []; openVm: []; upload: [] }>()

const c = props.console

const statusLabel = computed(() =>
  props.vm?.action === 'revert'
    ? 'Reverting to snapshot'
    : ({
      idle: 'Idle',
      connecting: 'Connecting',
      connected: 'Connected',
      disconnected: 'Disconnected',
      error: 'Error',
    })[c.status.value],
)
const statusColor = computed(() =>
  props.vm?.action === 'revert'
    ? 'bg-amber-500 animate-pulse'
    : ({
      idle: 'bg-muted-foreground',
      connecting: 'bg-amber-500 animate-pulse',
      connected: 'bg-emerald-500',
      disconnected: 'bg-muted-foreground',
      error: 'bg-destructive',
    })[c.status.value],
)

const protocols = computed(() => props.vm?.consoles ?? [])
const protocolModel = computed({
  get: () => c.protocol.value ?? undefined,
  set: (p: ConsoleProtocol | undefined) => {
    if (p && p !== c.protocol.value) c.connect(p)
  },
})

const scaleOptions: { value: ScaleMode; label: string; hint: string }[] = [
  { value: 'remote', label: 'Resize guest', hint: 'Change the guest resolution to fit the window' },
  { value: 'fit', label: 'Scale', hint: 'Scale the display to fit the window' },
  { value: 'none', label: '1:1', hint: 'Native size, scroll if larger than the window' },
]
const scaleModel = computed({
  get: () => c.scaleMode.value,
  set: (mode: ScaleMode | undefined) => {
    if (mode) c.scaleMode.value = mode
  },
})
const scaleAvailable = (mode: ScaleMode) => c.capabilities.value?.scaleModes.includes(mode) ?? false

const combos: { label: string; codes: string[] }[] = [
  { label: 'Ctrl+Alt+Del', codes: ['ControlLeft', 'AltLeft', 'Delete'] },
  { label: 'Ctrl+Shift+Esc', codes: ['ControlLeft', 'ShiftLeft', 'Escape'] },
  { label: 'Alt+Tab', codes: ['AltLeft', 'Tab'] },
  { label: 'Alt+F4', codes: ['AltLeft', 'F4'] },
  { label: 'Win', codes: ['MetaLeft'] },
  { label: 'Win+R', codes: ['MetaLeft', 'KeyR'] },
  { label: 'Win+L', codes: ['MetaLeft', 'KeyL'] },
  { label: 'PrtSc', codes: ['PrintScreen'] },
]

const connected = computed(() => c.status.value === 'connected')

// Without a USB tablet, VNC can only move the guest pointer relatively.
const relativeVncPointer = computed(() => c.protocol.value === 'vnc' && props.vm?.absolutePointer === false)

async function sendCombo(codes: string[]) {
  await c.sendKeys(codes)
  c.focus()
}

const textToType = ref('')
const typing = ref(false)
const typeResult = ref<string>()
async function typeText() {
  if (!textToType.value) return
  typing.value = true
  typeResult.value = undefined
  try {
    const { skipped } = await c.typeText(textToType.value)
    if (skipped.length) typeResult.value = `Skipped characters not on a US keyboard: ${skipped.join(' ')}`
  } finally {
    typing.value = false
  }
}

const clipboardText = ref('')
const clipboardResult = ref<string>()
async function sendClipboard() {
  clipboardResult.value = undefined
  try {
    await c.sendClipboard(clipboardText.value)
    clipboardResult.value = 'Sent. Paste in the guest.'
  } catch (e) {
    clipboardResult.value = e instanceof Error ? e.message : String(e)
  }
}
const clipboardRead = ref<ClipboardReadState>('unsupported')
onBeforeUnmount(watchClipboardRead((state) => (clipboardRead.value = state)))

async function copyGuestClipboard() {
  if (c.guestClipboard.value !== null) await navigator.clipboard?.writeText(c.guestClipboard.value)
}

const volume = computed({
  get: () => [Math.round(c.audio.value.volume * 100)],
  set: (v: number[] | undefined) => {
    c.audio.value = { ...c.audio.value, volume: (v?.[0] ?? 0) / 100 }
  },
})
const audioStatusText = computed(
  () =>
    ({
      idle: 'Nothing playing in the guest',
      playing: 'Playing',
      blocked: 'Blocked by your browser until you click the page',
      error: "Playback failed (see the browser console)",
    })[c.audioStatus.value],
)

// Quick actions.
const canUpload = computed(() => props.vm?.agent?.status === 'connected')
const shot = ref(false)
function screenshot() {
  const canvas = c.snapshot()
  if (!canvas) return
  canvas.toBlob((blob) => {
    if (!blob) return
    const stamp = new Date().toISOString().slice(0, 19).replace(/[T:]/g, '-')
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `${props.vm?.name ?? 'console'}-${stamp}.png`
    a.click()
    setTimeout(() => URL.revokeObjectURL(a.href), 1000)
    shot.value = true
    setTimeout(() => (shot.value = false), 800)
  }, 'image/png')
}

// Snapshot, revert and force reset, confirmed in the panel.
const confirming = ref<'take' | 'revert' | 'reset' | null>(null)
const withMemory = ref(true)
const snapshotError = ref<string>()
const snapshotReady = computed(() => props.vm?.state === 'ready' && !props.vm.action)
function askSnapshot(kind: 'take' | 'revert' | 'reset') {
  withMemory.value = true
  snapshotError.value = undefined
  confirming.value = kind
}
async function runSnapshot() {
  const kind = confirming.value
  const vm = props.vm
  confirming.value = null
  if (!vm) return
  try {
    if (kind === 'take') await api.takeSnapshot(vm.id, withMemory.value && vm.power === 'running')
    // From the console, the VM always comes back up (booting, without memory).
    else if (kind === 'revert') await api.revertSnapshot(vm.id, true)
    else if (kind === 'reset') await api.power(vm.id, 'reset')
  } catch (e) {
    snapshotError.value = errorMessage(e)
  }
}

const soundOn = computed({
  get: () => !c.audio.value.muted,
  set: (on: boolean) => {
    c.audio.value = { ...c.audio.value, muted: !on }
  },
})
</script>

<template>
  <!-- Collapsed handle -->
  <button
    v-if="!open"
    type="button"
    class="bg-background/80 text-foreground hover:bg-background absolute top-1/2 left-0 z-20 flex h-16 w-4 -translate-y-1/2 items-center justify-center rounded-r-md border border-l-0 shadow-md backdrop-blur transition-[width] hover:w-6"
    title="Open the control panel (Ctrl+Alt+Shift)"
    @click="open = true"
  >
    <ChevronRight class="size-3.5" />
  </button>

  <!-- Clicking outside the open panel closes it rather than reaching the guest. -->
  <div v-if="open" class="absolute inset-0 z-10" @pointerdown="open = false" />

  <Transition
    enter-from-class="-translate-x-full"
    leave-to-class="-translate-x-full"
    enter-active-class="transition-transform duration-200 ease-out"
    leave-active-class="transition-transform duration-150 ease-in"
  >
    <aside
      v-if="open"
      class="bg-background/95 text-foreground absolute inset-y-0 left-0 z-20 flex w-80 max-w-[90vw] flex-col border-r shadow-xl backdrop-blur"
    >
      <div class="flex items-center justify-between px-2 pt-2">
        <Button variant="ghost" size="sm" as-child>
          <RouterLink to="/"><ArrowLeft /> Back to machines</RouterLink>
        </Button>
        <Button variant="ghost" size="icon-sm" title="Close panel" @click="open = false">
          <ChevronLeft />
        </Button>
      </div>
      <header class="flex items-start gap-2 px-4 pt-2 pb-3">
        <div class="min-w-0 flex-1">
          <h2 class="truncate text-sm font-semibold">{{ vm?.name ?? 'Console' }}</h2>
          <p class="text-muted-foreground flex items-center gap-1.5 text-xs">
            <span class="inline-block size-2 rounded-full" :class="statusColor" />
            {{ statusLabel }}
            <template v-if="vm"> · {{ vm.templateName }}</template>
          </p>
        </div>
        <Button variant="outline" size="sm" title="Open another VM in a new tab" @click="emit('openVm')">
          <SquarePlus /> New tab
        </Button>
      </header>
      <div class="grid grid-cols-5 gap-1.5 px-4 pb-3" role="toolbar" aria-label="Quick actions">
        <Button
          variant="outline"
          class="h-10"
          :class="{
            'border-destructive/60 bg-destructive/15 text-destructive hover:bg-destructive/25 hover:text-destructive':
              !soundOn && c.capabilities.value?.audio,
          }"
          :disabled="!c.capabilities.value?.audio"
          :title="!c.capabilities.value?.audio ? 'No audio over this connection' : soundOn ? 'Mute' : 'Guest audio is muted: click to unmute'"
          @click="soundOn = !soundOn"
        >
          <Volume2 v-if="soundOn" class="size-5" /><VolumeX v-else class="size-5" />
        </Button>
        <Button variant="outline" class="h-10" :title="fullscreen ? 'Exit fullscreen' : 'Fullscreen'" @click="emit('toggleFullscreen')">
          <Minimize v-if="fullscreen" class="size-5" /><Maximize v-else class="size-5" />
        </Button>
        <Button
          variant="outline"
          class="h-10"
          title="Send Ctrl+Alt+Del"
          :disabled="!connected"
          @click="sendCombo(['ControlLeft', 'AltLeft', 'Delete'])"
        >
          <Keyboard class="size-5" />
        </Button>
        <Button
          variant="outline"
          class="h-10"
          :title="c.clipboardSharing.value ? 'Clipboard sharing on (click to turn off)' : 'Clipboard sharing off (click to turn on)'"
          :class="{ 'text-muted-foreground': !c.clipboardSharing.value }"
          @click="c.clipboardSharing.value = !c.clipboardSharing.value"
        >
          <Clipboard v-if="c.clipboardSharing.value" class="size-5" /><ClipboardX v-else class="size-5" />
        </Button>
        <Button
          variant="outline"
          class="h-10"
          title="Screenshot (downloads a PNG)"
          :disabled="!connected"
          :class="{ 'text-emerald-500': shot }"
          @click="screenshot"
        >
          <Camera class="size-5" />
        </Button>
        <Button
          variant="outline"
          class="h-10"
          :title="canUpload ? 'Upload files to the guest' : 'Uploading needs the Quicksand agent'"
          :disabled="!canUpload"
          @click="emit('upload')"
        >
          <Upload class="size-5" />
        </Button>
        <Button
          variant="outline"
          class="h-10"
          :title="vm?.snapshot ? 'Replace the snapshot' : 'Take a snapshot'"
          :disabled="!snapshotReady"
          @click="askSnapshot('take')"
        >
          <Save class="size-5" />
        </Button>
        <Button
          variant="outline"
          class="h-10"
          :title="vm?.snapshot ? `Revert to the snapshot from ${formatAgo(vm.snapshot.takenAt)}` : 'No snapshot to revert to'"
          :disabled="!snapshotReady || !vm?.snapshot"
          @click="askSnapshot('revert')"
        >
          <History class="size-5" />
        </Button>
        <Button
          variant="outline"
          class="hover:text-destructive h-10"
          title="Force reset (like pressing the reset button)"
          :disabled="!snapshotReady || vm?.power !== 'running'"
          @click="askSnapshot('reset')"
        >
          <RotateCcw class="size-5" />
        </Button>
      </div>

      <!-- Confirmations, in the panel so they also work in fullscreen. -->
      <div v-if="confirming" class="bg-muted/50 mx-4 mb-3 flex flex-col gap-3 rounded-lg border p-3">
        <template v-if="confirming === 'take'">
          <p class="text-sm font-medium">{{ vm?.snapshot ? 'Replace the snapshot?' : 'Take a snapshot?' }}</p>
          <p class="text-muted-foreground text-xs">
            Saves the disks{{ vm?.power === 'running' ? ' and, if you like, the memory' : '' }}.
            <template v-if="vm?.snapshot"> The one from {{ formatAgo(vm.snapshot.takenAt) }} is replaced.</template>
          </p>
          <div v-if="vm?.power === 'running'" class="flex items-center gap-2">
            <Checkbox id="quick-snapshot-memory" v-model="withMemory" />
            <Label for="quick-snapshot-memory" class="text-xs">Include memory</Label>
          </div>
        </template>
        <template v-else-if="confirming === 'reset'">
          <p class="text-sm font-medium">Force reset {{ vm?.name }}?</p>
          <p class="text-muted-foreground text-xs">
            Restarts the VM at once, like pressing its reset button: unsaved work in the guest is lost.
          </p>
        </template>
        <template v-else>
          <p class="text-sm font-medium">Revert to the snapshot?</p>
          <p class="text-muted-foreground text-xs">
            Everything since it was taken ({{ vm?.snapshot ? formatAgo(vm.snapshot.takenAt) : '' }}) is lost.
            {{ vm?.snapshot?.memory ? 'The VM resumes as it was.' : "It doesn't include memory, so the VM boots afresh." }}
            The snapshot is kept.
          </p>
        </template>
        <div class="flex justify-end gap-2">
          <Button variant="outline" size="sm" @click="confirming = null">Cancel</Button>
          <Button size="sm" :variant="confirming === 'take' ? 'default' : 'destructive'" @click="runSnapshot">
            {{ confirming === 'take' ? (vm?.snapshot ? 'Replace' : 'Take snapshot') : confirming === 'reset' ? 'Reset' : 'Revert' }}
          </Button>
        </div>
      </div>
      <div v-if="vm?.action === 'snapshot' || vm?.action === 'revert' || vm?.action === 'reset'" class="mx-4 mb-3">
        <ActionProgressBar v-if="vm.actionProgress" :progress="vm.actionProgress" />
        <p v-else class="flex items-center gap-1.5 text-xs"><Spinner class="size-3" /> {{ actionLabels[vm.action] }}…</p>
      </div>
      <p v-if="snapshotError || vm?.actionError" class="text-destructive mx-4 mb-3 text-xs">
        {{ snapshotError || vm?.actionError }}
      </p>
      <Separator />

      <div class="flex-1 overflow-y-auto">
        <SidebarSection title="Connection" :icon="Plug">
          <ToggleGroup v-model="protocolModel" type="single" variant="outline" class="w-full">
            <ToggleGroupItem
              v-for="p in (['spice', 'vnc'] as const)"
              :key="p"
              :value="p"
              :disabled="!protocols.includes(p)"
              :title="protocols.includes(p) ? consoleDrivers[p].description : 'Not supported by this VM\'s display'"
              class="flex-1"
            >
              {{ consoleDrivers[p].label }}
            </ToggleGroupItem>
          </ToggleGroup>
          <p v-if="c.message.value" class="text-destructive text-xs">{{ c.message.value }}</p>
          <Alert v-if="relativeVncPointer" variant="destructive">
            <CircleAlert />
            <AlertTitle>The mouse won't line up over VNC</AlertTitle>
            <AlertDescription>
              This VM has no USB tablet, so VNC can only move the guest pointer relatively. Proxmox leaves it
              out on SPICE VMs unless <code>tablet: 1</code> is set explicitly, which its web UI can't do: run
              <code>qm set {{ vm?.vmid }} --tablet 1</code> and restart the VM, or use SPICE.
            </AlertDescription>
          </Alert>
          <div class="flex gap-2">
            <Button variant="outline" class="flex-1" @click="c.reconnect()">
              <RefreshCw /> Reconnect
            </Button>
            <Button variant="outline" class="flex-1" :disabled="!connected" @click="c.disconnect()">
              <Unplug /> Disconnect
            </Button>
          </div>
        </SidebarSection>
        <Separator />

        <SidebarSection title="Display" :icon="Monitor">
          <ToggleGroup v-model="scaleModel" type="single" variant="outline" class="w-full">
            <ToggleGroupItem
              v-for="opt in scaleOptions"
              :key="opt.value"
              :value="opt.value"
              :title="opt.hint"
              class="flex-1"
              :class="{ 'opacity-60': connected && !scaleAvailable(opt.value) }"
            >
              {{ opt.label }}
            </ToggleGroupItem>
          </ToggleGroup>
          <p
            v-if="connected && c.scaleMode.value === 'remote' && !scaleAvailable('remote')"
            class="text-muted-foreground text-xs"
          >
            The guest can't be resized
            <template v-if="vm?.agent">until the Quicksand agent{{ c.protocol.value === 'spice' ? ' or SPICE agent' : '' }} connects</template>
            <template v-else-if="c.protocol.value === 'spice'">until the SPICE agent connects</template>
            <template v-else>over this connection</template>; scaling instead.
          </p>
          <p v-if="c.resolution.value" class="text-muted-foreground text-xs">
            Guest resolution {{ c.resolution.value.width }}×{{ c.resolution.value.height }}
          </p>
        </SidebarSection>
        <Separator />

        <SidebarSection title="Keyboard" :icon="Keyboard">
          <div class="grid grid-cols-2 gap-1.5">
            <Button
              v-for="combo in combos"
              :key="combo.label"
              variant="secondary"
              size="sm"
              :disabled="!connected"
              @click="sendCombo(combo.codes)"
            >
              {{ combo.label }}
            </Button>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="type-text" class="text-xs">Type text into the guest</Label>
            <Textarea
              id="type-text"
              v-model="textToType"
              rows="3"
              placeholder="Typed as keystrokes using a US layout"
              class="font-mono text-xs"
            />
            <Button :disabled="!connected || !textToType || typing" @click="typeText">
              <Keyboard /> {{ typing ? 'Typing…' : 'Type' }}
            </Button>
            <p v-if="typeResult" class="text-muted-foreground text-xs">{{ typeResult }}</p>
          </div>
        </SidebarSection>
        <Separator />

        <SidebarSection title="Clipboard" :icon="Clipboard">
          <div v-if="!c.clipboardSharing.value" class="flex flex-col gap-2">
            <p class="text-muted-foreground text-xs">
              Clipboard sharing is off: copies in the guest stay there, and Ctrl+V pastes the guest's own clipboard.
            </p>
            <Button variant="outline" size="sm" @click="c.clipboardSharing.value = true"><Clipboard /> Turn on</Button>
          </div>
          <template
            v-else-if="c.capabilities.value?.clipboardSync || c.capabilities.value?.clipboardSend || c.agentClipboard.value"
          >
            <p v-if="c.agentClipboard.value" class="text-muted-foreground text-xs">
              Shared through the Quicksand agent: copying in the guest updates your clipboard, and
              <Kbd>Ctrl+V</Kbd> in the console pastes yours into the guest. The guest gets your clipboard only
              when you paste.
            </p>
            <template v-else-if="c.capabilities.value?.clipboardSync">
              <p v-if="clipboardRead === 'granted'" class="text-muted-foreground text-xs">
                Synced with your clipboard in both directions.
              </p>
              <template v-else>
                <p class="text-muted-foreground text-xs">
                  Copying in the guest updates your clipboard. Press <Kbd>Ctrl+V</Kbd> in the console to paste
                  your clipboard into the guest.
                </p>
                <Button v-if="clipboardRead === 'prompt'" variant="outline" @click="requestClipboardRead">
                  <Clipboard /> Allow automatic clipboard sync
                </Button>
              </template>
            </template>
            <div v-if="c.guestClipboard.value !== null" class="flex flex-col gap-1.5">
              <div class="flex items-center justify-between">
                <Label class="text-xs">Last copied in the guest</Label>
                <Button variant="ghost" size="icon-sm" title="Copy" @click="copyGuestClipboard">
                  <Copy />
                </Button>
              </div>
              <pre class="bg-muted max-h-24 overflow-auto rounded-md p-2 text-xs whitespace-pre-wrap">{{
                c.guestClipboard.value
              }}</pre>
            </div>
            <div class="flex flex-col gap-1.5">
              <Label for="clipboard-send" class="text-xs">Send to the guest clipboard</Label>
              <Textarea id="clipboard-send" v-model="clipboardText" rows="3" class="font-mono text-xs" />
              <Button :disabled="!connected || !clipboardText" @click="sendClipboard">
                <Send /> Send
              </Button>
              <p v-if="clipboardResult" class="text-muted-foreground text-xs">{{ clipboardResult }}</p>
            </div>
          </template>
          <p v-else class="text-muted-foreground text-xs">
            <template v-if="c.agentClipboardError.value">
              The Quicksand agent can't share the clipboard: {{ c.agentClipboardError.value }}
            </template>
            <template v-else-if="c.protocol.value === 'spice'">
              Waiting for the SPICE agent. Install the SPICE guest tools to share the clipboard.
            </template>
            <template v-else>
              Clipboard sharing over VNC needs the Quicksand agent. Use “Type text” instead.
            </template>
          </p>
        </SidebarSection>
        <Separator />

        <template v-if="vm?.agent">
          <SidebarSection title="Agent" :icon="SquareTerminal">
            <AgentPanel :vm="vm" />
          </SidebarSection>
          <Separator />
        </template>

        <SidebarSection title="Audio" :icon="Volume2">
          <template v-if="c.capabilities.value?.audio">
            <div class="flex items-center justify-between">
              <Label for="audio-on" class="text-xs">Play guest audio</Label>
              <Switch id="audio-on" v-model="soundOn" />
            </div>
            <div class="flex items-center justify-between gap-2">
              <p class="text-xs" :class="c.audioStatus.value === 'idle' || c.audioStatus.value === 'playing' ? 'text-muted-foreground' : 'text-destructive'">
                {{ audioStatusText }}
              </p>
              <Button v-if="c.audioStatus.value === 'blocked'" variant="outline" size="sm" @click="c.resumeAudio()">
                <Volume2 /> Enable audio
              </Button>
            </div>
            <div class="flex items-center gap-3">
              <VolumeX class="text-muted-foreground size-3.5" />
              <Slider v-model="volume" :max="100" :step="1" :disabled="!soundOn" />
              <Volume2 class="text-muted-foreground size-3.5" />
            </div>
          </template>
          <p v-else class="text-muted-foreground text-xs">
            Audio isn't available over {{ c.protocol.value?.toUpperCase() ?? 'this connection' }}.
          </p>
        </SidebarSection>
      </div>

      <Separator />
      <footer class="text-muted-foreground flex items-center justify-between px-4 py-2 text-[0.7rem]">
        <span>Quicksand</span>
        <span class="flex items-center gap-1">
          Toggle panel <Kbd>Ctrl+Alt+Shift</Kbd>
        </span>
      </footer>
    </aside>
  </Transition>
</template>
