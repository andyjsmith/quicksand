<script setup lang="ts">
import { CircleAlert, Link2, Monitor, Play, Save, SquareTerminal, TriangleAlert } from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { PowerAction, VM } from '@/api'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Spinner } from '@/components/ui/spinner'
import { useMinuteClock } from '@/composables/useNow'
import { expiryState, formatMemory, formatUptime, osIcon, vmStatus } from '@/lib/format'
import { loadPreview, onPreviewChange } from '@/lib/previews'
import { linkColor } from '@/composables/useLinks'
import ActionProgressBar from './ActionProgressBar.vue'
import ExpiryBadge from './ExpiryBadge.vue'
import VmActionsMenu from './VmActionsMenu.vue'

const props = defineProps<{ vm: VM }>()

const status = computed(() => vmStatus(props.vm))
const running = computed(() => props.vm.power === 'running')
const linkTitle = computed(() => {
  const l = props.vm.link
  if (!l) return ''
  if (l.status === 'manual') return `Set ${l.address} on the network adapter with MAC ${l.mac} in the guest (no agent to do it)`
  if (l.status === 'pending') return `${l.name}: setting up ${l.address}${l.error ? ` (${l.error})` : ''}`
  return `${l.name}: ${l.address}`
})
const now = useMinuteClock()
const expiringSoon = computed(() => !!expiryState(props.vm, now.value.getTime())?.soon)
const icon = computed(() => osIcon(props.vm.os))
const ready = computed(() => props.vm.state === 'ready')
const busy = computed(() => props.vm.state === 'deploying' || props.vm.state === 'destroying' || !!props.vm.action)
const canConnect = computed(() => ready.value && props.vm.power === 'running' && props.vm.consoles.length > 0)

const preview = ref(loadPreview(props.vm.id))
onBeforeUnmount(onPreviewChange((id) => id === props.vm.id && (preview.value = loadPreview(id))))

const error = ref<string>()
const actions = ref<InstanceType<typeof VmActionsMenu>>()
const power = (action: PowerAction) => actions.value?.power(action)

// What the primary button does when the VM isn't running.
const primaryPower = computed<{ action: PowerAction; label: string } | null>(() => {
  if (!ready.value || busy.value) return null
  if (props.vm.power === 'stopped') return { action: 'start', label: 'Start' }
  if (props.vm.power === 'paused' || props.vm.power === 'suspended') return { action: 'resume', label: 'Resume' }
  return null
})
</script>

<template>
  <Card
    class="flex flex-col gap-0 overflow-hidden p-0"
    :class="{ 'ring-destructive ring-2': expiringSoon }"
  >
    <!-- Preview -->
    <component
      :is="canConnect ? RouterLink : 'div'"
      :to="canConnect ? { name: 'console', params: { id: vm.id } } : undefined"
      class="group bg-muted relative block aspect-video overflow-hidden"
      :title="canConnect ? 'Open console' : undefined"
    >
      <img
        v-if="preview"
        :src="preview"
        alt=""
        class="size-full object-cover transition"
        :class="{ 'opacity-40 grayscale': !canConnect }"
      />
      <div v-else class="text-muted-foreground flex size-full items-center justify-center">
        <component :is="icon" class="size-10 opacity-40" />
      </div>
      <div
        v-if="canConnect"
        class="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition group-hover:opacity-100"
      >
        <span class="flex items-center gap-1.5 rounded-md bg-black/60 px-3 py-1.5 text-xs font-medium text-white">
          <Monitor class="size-3.5" /> Open console
        </span>
      </div>
      <!-- Deployment / teardown / snapshot progress -->
      <div
        v-if="vm.actionProgress && vm.state === 'ready'"
        class="bg-background/85 absolute inset-x-0 bottom-0 px-3 py-2.5 backdrop-blur"
      >
        <ActionProgressBar :progress="vm.actionProgress" />
      </div>
      <div
        v-if="vm.state === 'deploying' || vm.state === 'destroying'"
        class="bg-background/85 absolute inset-x-0 bottom-0 flex flex-col gap-1.5 px-3 py-2.5 backdrop-blur"
      >
        <div class="flex items-center justify-between text-xs">
          <span class="flex items-center gap-1.5"><Spinner class="size-3" /> {{ vm.step || 'Working' }}…</span>
          <span class="text-muted-foreground tabular-nums">{{ Math.round(vm.progress * 100) }}%</span>
        </div>
        <Progress :model-value="vm.progress * 100" class="h-1.5" />
      </div>
    </component>

    <!-- Details -->
    <div class="flex flex-col gap-2 p-3">
      <div class="flex items-start gap-2">
        <div class="min-w-0 flex-1">
          <h3 class="truncate text-sm font-semibold">
            <RouterLink :to="{ name: 'vm', params: { id: vm.id } }" class="hover:underline">{{ vm.name }}</RouterLink>
          </h3>
          <p class="text-muted-foreground truncate text-xs">
            {{ vm.templateName }} · {{ vm.cores }} CPUs · {{ formatMemory(vm.memoryMiB) }} · {{ vm.networkLabel }}
          </p>
        </div>
        <VmActionsMenu ref="actions" :vm="vm" show-details @error="error = $event" />
      </div>

      <div class="flex items-center justify-between gap-2">
        <span class="flex items-center gap-1.5 text-xs">
          <span class="inline-block size-2 rounded-full" :class="status.dot" />
          {{ status.label }}
          <span v-if="ready && vm.power === 'running' && vm.uptime" class="text-muted-foreground">
            · up {{ formatUptime(vm.uptime) }}
          </span>
          <span
            v-if="vm.agent && ready && vm.power === 'running'"
            class="text-muted-foreground flex items-center gap-1"
            :title="vm.agent.status === 'connected' ? `Agent connected (${vm.agent.hostname}, ${vm.agent.ip})` : `Agent not reachable at ${vm.agent.ip}`"
          >
            · <SquareTerminal class="size-3" :class="vm.agent.status === 'connected' ? 'text-emerald-500' : ''" /> agent
          </span>
          <span
            v-if="vm.snapshot"
            class="text-muted-foreground flex items-center gap-1"
            :title="`Snapshot taken ${new Date(vm.snapshot.takenAt).toLocaleString()}${vm.snapshot.memory ? ', with memory' : ''}`"
          >
            · <Save class="size-3" />
          </span>
        </span>
        <Button v-if="canConnect" size="sm" as-child>
          <RouterLink :to="{ name: 'console', params: { id: vm.id } }"><Monitor /> Connect</RouterLink>
        </Button>
        <Button v-else-if="primaryPower" size="sm" variant="outline" @click="power(primaryPower.action)">
          <Play /> {{ primaryPower.label }}
        </Button>
      </div>

      <div v-if="vm.link || vm.expiresAt" class="-mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
        <ExpiryBadge v-if="vm.expiresAt" :vm="vm" extendable @error="error = $event" />
        <span
          v-if="vm.link"
          class="text-muted-foreground flex items-center gap-1 text-xs"
          :title="linkTitle"
        >
          <TriangleAlert v-if="vm.link.status === 'manual'" class="size-3 text-amber-500" />
          <Spinner v-else-if="vm.link.status === 'pending' && running" class="size-3" />
          <Link2 v-else class="size-3" :class="vm.link.status === 'configured' ? linkColor(vm.link.network).text : ''" />
          <span class="font-mono">{{ vm.link.address.split('/')[0] }}</span>
        </span>
      </div>
      <p v-if="vm.state === 'failed' || vm.actionError || error" class="text-destructive flex gap-1.5 text-xs">
        <CircleAlert class="mt-0.5 size-3 shrink-0" />
        <span class="break-words">{{ error || vm.actionError || vm.error }}</span>
      </p>
    </div>

  </Card>
</template>
