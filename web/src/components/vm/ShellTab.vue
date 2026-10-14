<script setup lang="ts">
import '@xterm/xterm/css/xterm.css'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import { Plug, Unplug } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import { api, type VM } from '@/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { Frame, FrameDecoder, encodeFrame, type ShellExit } from '@/lib/frames'

const props = defineProps<{ vm: VM }>()

type Source = 'agent' | 'serial'

const running = computed(() => props.vm.state === 'ready' && props.vm.power === 'running')
const available = computed(() => {
  const out: Source[] = []
  if (running.value && props.vm.agent?.status === 'connected') out.push('agent')
  if (running.value && props.vm.serial) out.push('serial')
  return out
})
const state = ref<'idle' | 'connecting' | 'open' | 'closed'>('idle')
const source = ref<Source>(available.value[0] ?? 'agent')
// Follow availability (the agent connecting, say) while not connected.
watch(available, (a) => {
  if (state.value !== 'open' && state.value !== 'connecting' && !a.includes(source.value) && a[0]) source.value = a[0]
})

const isWindows = computed(() => props.vm.agent?.os === 'windows')
const agentLabel = computed(() => (isWindows.value ? 'PowerShell' : 'Shell'))

// The agent shell's program; empty for the guest's default.
const shellProgram = ref('')
const shellSuggestions = computed(() => (isWindows.value ? ['powershell', 'pwsh', 'cmd'] : ['bash', 'sh', 'zsh', 'fish']))
const defaultShell = computed(() => (isWindows.value ? 'PowerShell' : 'login shell'))
const openLabel = computed(() => {
  if (source.value === 'serial') return 'the serial console'
  if (shellProgram.value.trim()) return shellProgram.value.trim()
  return agentLabel.value === 'Shell' ? 'a shell' : agentLabel.value
})

const status = ref<string>()
const failed = ref(false)

const container = useTemplateRef('container')
let term: Terminal | undefined
let fit: FitAddon | undefined
let ws: WebSocket | undefined
let observer: ResizeObserver | undefined

function send(frame: Uint8Array<ArrayBuffer>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(frame)
}

function refit() {
  // A hidden tab has no size to fit to.
  if (container.value && container.value.clientWidth > 0) fit?.fit()
}

onMounted(() => {
  term = new Terminal({
    cursorBlink: true,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: 13,
    scrollback: 5000,
    theme: { background: '#0a0a0a', foreground: '#e5e5e5' },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(container.value!)
  refit()
  term.onData((d) => send(encodeFrame(Frame.Stdin, d)))
  term.onBinary((d) => send(encodeFrame(Frame.Stdin, Uint8Array.from(d, (c) => c.charCodeAt(0)))))
  term.onResize(({ cols, rows }) => send(encodeFrame(Frame.Resize, JSON.stringify({ cols, rows }))))
  observer = new ResizeObserver(refit)
  observer.observe(container.value!)
})

onBeforeUnmount(() => {
  ws?.close()
  observer?.disconnect()
  term?.dispose()
})

function connect() {
  if (!term || !available.value.includes(source.value)) return
  ws?.close()
  term.reset()
  refit()
  state.value = 'connecting'
  status.value = undefined
  failed.value = false
  const using = source.value
  const socket = new WebSocket(api.shellUrl(props.vm.id, using, term.cols, term.rows, shellProgram.value.trim()))
  socket.binaryType = 'arraybuffer'
  ws = socket
  const decoder = new FrameDecoder()
  let exit: ShellExit | undefined
  socket.onopen = () => {
    if (ws !== socket) return
    state.value = 'open'
    term?.focus()
    // A serial console shows nothing until it's sent something.
    if (using === 'serial') send(encodeFrame(Frame.Stdin, '\r'))
  }
  socket.onmessage = (e) =>
    decoder.push(new Uint8Array(e.data as ArrayBuffer), (type, payload) => {
      if (type === Frame.Stdout || type === Frame.Stderr) term?.write(payload)
      else if (type === Frame.Exit) exit = JSON.parse(new TextDecoder().decode(payload)) as ShellExit
    })
  socket.onclose = () => {
    if (ws !== socket) return
    ws = undefined
    state.value = 'closed'
    const byUser = disconnecting === socket
    failed.value = !!exit?.error || (!exit && !byUser)
    status.value = exit?.error
      ? exit.error
      : using === 'serial' && (exit || byUser)
        ? 'Disconnected from the serial console.'
        : byUser && !exit
          ? 'Disconnected; the shell was ended.'
          : !exit
            ? 'The connection closed unexpectedly.'
            : `The shell exited${exit.code ? ` with code ${exit.code}` : ''}.`
  }
}

// The socket the Disconnect button closed, so its closing isn't an error.
let disconnecting: WebSocket | undefined

function disconnect() {
  disconnecting = ws
  ws?.close()
}

const connected = computed(() => state.value === 'open' || state.value === 'connecting')
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex flex-wrap items-center gap-2">
      <ToggleGroup
        v-if="available.length > 1"
        :model-value="source"
        type="single"
        variant="outline"
        size="sm"
        :disabled="connected"
        @update:model-value="(v) => v && (source = v as Source)"
      >
        <ToggleGroupItem value="agent">Agent shell</ToggleGroupItem>
        <ToggleGroupItem value="serial">Serial console</ToggleGroupItem>
      </ToggleGroup>
      <span v-else-if="available.length" class="text-muted-foreground text-sm">
        {{ source === 'serial' ? 'Serial console' : 'Through the Quicksand agent' }}
      </span>
      <div v-if="source === 'agent' && available.includes('agent')" class="flex items-center gap-1.5">
        <Label for="shell-program" class="text-muted-foreground text-xs">Shell</Label>
        <Input
          id="shell-program"
          v-model="shellProgram"
          list="shell-programs"
          :placeholder="`Default (${defaultShell})`"
          class="h-8 w-44 text-xs"
          :disabled="connected"
          @keydown.enter="connect"
        />
        <datalist id="shell-programs">
          <option v-for="p in shellSuggestions" :key="p" :value="p" />
        </datalist>
      </div>
      <span v-if="!available.length" class="text-muted-foreground text-sm">No shell is available: the VM has no connected agent or serial console.</span>
      <span
        v-if="status"
        class="text-sm"
        :class="failed ? 'text-destructive' : 'text-muted-foreground'"
      >
        {{ status }}
      </span>
      <span v-else-if="state === 'connecting'" class="text-muted-foreground text-sm">Connecting…</span>
      <span v-if="source === 'serial' && state === 'open'" class="text-muted-foreground text-xs">
        The console stays logged in after you disconnect; log out first.
      </span>
      <div class="ml-auto flex gap-2">
        <Button v-if="connected" variant="outline" size="sm" @click="disconnect"><Unplug /> Disconnect</Button>
        <Button v-else size="sm" :disabled="!available.length" @click="connect">
          <Plug /> {{ state === 'closed' ? 'Reconnect' : 'Connect' }}
        </Button>
      </div>
    </div>
    <div class="relative overflow-hidden rounded-md border bg-[#0a0a0a] p-2">
      <div ref="container" class="h-[60vh] min-h-72" />
      <div
        v-if="state === 'idle'"
        class="absolute inset-0 flex items-center justify-center text-sm text-neutral-400"
      >
        <Button v-if="available.length" variant="secondary" @click="connect">
          <Plug /> Open {{ openLabel }}
        </Button>
      </div>
    </div>
  </div>
</template>
