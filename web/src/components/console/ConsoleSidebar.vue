<script setup lang="ts">
import {
  ArrowLeft,
  CircleAlert,
  ChevronLeft,
  ChevronRight,
  Clipboard,
  Copy,
  Keyboard,
  Maximize,
  Minimize,
  Monitor,
  Plug,
  RefreshCw,
  Send,
  SquareTerminal,
  Unplug,
  Volume2,
  VolumeX,
} from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { VM } from '@/api'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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
import type { ConsoleProtocol, ScaleMode } from '@/consoles/types'
import AgentPanel from './AgentPanel.vue'
import SidebarSection from './SidebarSection.vue'

const props = defineProps<{
  console: ConsoleController
  vm: VM | null
  fullscreen: boolean
}>()

const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ toggleFullscreen: [] }>()

const c = props.console

const statusLabel = computed(
  () =>
    ({
      idle: 'Idle',
      connecting: 'Connecting',
      connected: 'Connected',
      disconnected: 'Disconnected',
      error: 'Error',
    })[c.status.value],
)
const statusColor = computed(
  () =>
    ({
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
      <header class="flex items-start gap-2 px-4 pt-4 pb-3">
        <div class="min-w-0 flex-1">
          <h2 class="truncate text-sm font-semibold">{{ vm?.name ?? 'Console' }}</h2>
          <p class="text-muted-foreground flex items-center gap-1.5 text-xs">
            <span class="inline-block size-2 rounded-full" :class="statusColor" />
            {{ statusLabel }}
            <template v-if="vm"> · {{ vm.templateName }}</template>
          </p>
        </div>
        <Button variant="ghost" size="icon-sm" title="Close panel" @click="open = false">
          <ChevronLeft />
        </Button>
      </header>
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
          <Button variant="ghost" as-child class="justify-start">
            <RouterLink to="/"><ArrowLeft /> Back to machines</RouterLink>
          </Button>
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
            {{ c.protocol.value === 'spice' ? 'until the SPICE agent connects' : 'over this connection' }}; scaling instead.
          </p>
          <div class="flex items-center justify-between gap-2">
            <span class="text-muted-foreground text-xs">
              <template v-if="c.resolution.value">
                Guest resolution {{ c.resolution.value.width }}×{{ c.resolution.value.height }}
              </template>
            </span>
            <Button variant="outline" @click="emit('toggleFullscreen')">
              <component :is="fullscreen ? Minimize : Maximize" />
              {{ fullscreen ? 'Exit fullscreen' : 'Fullscreen' }}
            </Button>
          </div>
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
          <template v-if="c.capabilities.value?.clipboardSync || c.capabilities.value?.clipboardSend">
            <template v-if="c.capabilities.value?.clipboardSync">
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
            <template v-if="c.protocol.value === 'spice'">
              Waiting for the SPICE agent. Install the SPICE guest tools to share the clipboard.
            </template>
            <template v-else>Clipboard sharing isn't available over VNC. Use “Type text” instead.</template>
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
