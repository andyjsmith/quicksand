<script setup lang="ts">
import { useEventListener, useFullscreen, useTitle } from '@vueuse/core'
import { CircleAlert, PanelLeftOpen, RefreshCw, Unplug } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getVM, type VM } from '@/api'
import ConsoleSidebar from '@/components/console/ConsoleSidebar.vue'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { useConsole } from '@/composables/useConsole'
import type { ConsoleProtocol } from '@/consoles/types'

const props = defineProps<{ node: string; vmid: string }>()
const route = useRoute()
const router = useRouter()

const viewport = ref<HTMLElement | null>(null)
const root = ref<HTMLElement | null>(null)
const target = computed(() => ({ node: props.node, vmid: Number(props.vmid) }))
const ctl = useConsole(target, viewport)

const vm = ref<VM | null>(null)
const loadError = ref<string>()
const panelOpen = ref(false)

useTitle(computed(() => (vm.value ? `${vm.value.name} · Quicksand` : 'Console · Quicksand')))

onMounted(async () => {
  try {
    vm.value = await getVM(target.value.node, target.value.vmid)
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
    return
  }
  const requested = route.query.protocol as ConsoleProtocol | undefined
  const protocol = requested && vm.value.consoles.includes(requested) ? requested : vm.value.consoles[0]
  if (protocol) ctl.connect(protocol)
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

const { isFullscreen, toggle: toggleFullscreenRaw } = useFullscreen(root)
async function toggleFullscreen() {
  await toggleFullscreenRaw()
  // In fullscreen, capture keys like Escape and the Windows key for the guest
  // (Chromium only; elsewhere this is a no-op).
  const keyboard = (navigator as Navigator & { keyboard?: { lock(): Promise<void>; unlock(): void } }).keyboard
  if (isFullscreen.value) await keyboard?.lock().catch(() => {})
  else keyboard?.unlock()
  panelOpen.value = false
}
</script>

<template>
  <div ref="root" class="dark bg-background relative h-dvh w-full overflow-hidden">
    <!-- The driver renders the guest display in here. The display canvas
         takes focus on hover, which shouldn't draw a focus ring. -->
    <div ref="viewport" class="absolute inset-0 bg-black [&_canvas]:outline-none" />

    <div
      v-if="loadError || ctl.status.value !== 'connected'"
      class="pointer-events-none absolute inset-0 z-[5] flex items-center justify-center"
    >
      <div
        class="bg-background/90 text-foreground pointer-events-auto flex max-w-sm flex-col items-center gap-3 rounded-lg border p-6 text-center shadow-lg backdrop-blur"
      >
        <template v-if="loadError">
          <CircleAlert class="text-destructive size-6" />
          <p class="text-sm font-medium">Couldn't load this virtual machine</p>
          <p class="text-muted-foreground text-xs">{{ loadError }}</p>
          <Button variant="outline" @click="router.push('/')">Back to the list</Button>
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

    <ConsoleSidebar
      v-model:open="panelOpen"
      :console="ctl"
      :vm="vm"
      :fullscreen="isFullscreen"
      @toggle-fullscreen="toggleFullscreen"
    />
  </div>
</template>
