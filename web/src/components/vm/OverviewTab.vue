<script setup lang="ts">
import {
  Clock,
  Cpu,
  Globe,
  HardDrive,
  History,
  MemoryStick,
  Power,
  Save,
  SquareTerminal,
  TriangleAlert,
} from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { VM } from '@/api'
import AgentPanel from '@/components/console/AgentPanel.vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { linkColor } from '@/composables/useLinks'
import { formatAgo, formatBytes, formatMemory, formatUptime, osIcon, vmStatus } from '@/lib/format'
import { loadPreview, onPreviewChange } from '@/lib/previews'

const props = defineProps<{ vm: VM }>()

const running = computed(() => props.vm.state === 'ready' && props.vm.power === 'running')
const status = computed(() => vmStatus(props.vm))
const canConnect = computed(() => running.value && props.vm.consoles.length > 0)

// Proxmox reports CPU as a fraction of all the VM's cores.
const cpuPercent = computed(() => Math.round(props.vm.cpu * 100))
const memPercent = computed(() => (props.vm.maxmem ? Math.round((props.vm.mem / props.vm.maxmem) * 100) : 0))

const preview = ref(loadPreview(props.vm.id))
onBeforeUnmount(onPreviewChange((id) => id === props.vm.id && (preview.value = loadPreview(id))))

const created = computed(() => new Date(props.vm.createdAt))
const agentLine = computed(() => {
  const a = props.vm.agent
  if (!a) return 'Not installed'
  return running.value ? `Waiting for it at ${a.ip}` : `Not running (${a.ip})`
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <div
      v-if="vm.link?.status === 'manual'"
      class="flex gap-2.5 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm"
    >
      <TriangleAlert class="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
      <div class="flex flex-col gap-1">
        <p class="font-medium">Set this machine's link network address yourself</p>
        <p class="text-muted-foreground">
          {{ vm.link.error ? `${vm.link.error}, so it` : 'It has no agent to do it, so it' }} can't reach
          {{ vm.link.name }} until you do. In the guest, give the network adapter with MAC
          <code class="text-foreground">{{ vm.link.mac }}</code> the static address
          <code class="text-foreground">{{ vm.link.address }}</code>, with no gateway.
        </p>
      </div>
    </div>

    <!-- Live figures -->
    <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <div class="bg-card flex flex-col gap-1.5 rounded-xl border p-3">
        <span class="text-muted-foreground flex items-center gap-1.5 text-xs"><Power class="size-3.5" /> Status</span>
        <span class="flex items-center gap-2 text-sm font-medium">
          <span class="inline-block size-2 rounded-full" :class="status.dot" /> {{ status.label }}
        </span>
      </div>
      <div class="bg-card flex flex-col gap-1.5 rounded-xl border p-3">
        <span class="text-muted-foreground flex items-center gap-1.5 text-xs"><Cpu class="size-3.5" /> CPU</span>
        <span class="text-sm font-medium tabular-nums">
          {{ running ? `${cpuPercent}%` : '—' }}
          <span class="text-muted-foreground font-normal">of {{ vm.cores }} cores</span>
        </span>
        <Progress :model-value="running ? cpuPercent : 0" class="h-1" />
      </div>
      <div class="bg-card flex flex-col gap-1.5 rounded-xl border p-3">
        <span class="text-muted-foreground flex items-center gap-1.5 text-xs"><MemoryStick class="size-3.5" /> Memory</span>
        <span class="text-sm font-medium tabular-nums">
          <template v-if="running && vm.maxmem">{{ formatBytes(vm.mem) }}</template>
          <template v-else>—</template>
          <span class="text-muted-foreground font-normal"> of {{ formatMemory(vm.memoryMiB) }}</span>
        </span>
        <Progress :model-value="running ? memPercent : 0" class="h-1" />
      </div>
      <div class="bg-card flex flex-col gap-1.5 rounded-xl border p-3">
        <span class="text-muted-foreground flex items-center gap-1.5 text-xs"><Clock class="size-3.5" /> Uptime</span>
        <span class="text-sm font-medium tabular-nums">{{ running ? formatUptime(vm.uptime) : '—' }}</span>
      </div>
    </div>

    <div class="grid gap-4 lg:grid-cols-[1fr_20rem]">
      <div class="flex min-w-0 flex-col gap-4">
        <div class="grid gap-4 md:grid-cols-2">
          <Card class="gap-3 py-4">
            <CardHeader class="px-4"><CardTitle class="flex items-center gap-2 text-sm"><HardDrive class="size-4" /> Machine</CardTitle></CardHeader>
            <CardContent class="px-4">
              <dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 text-sm">
                <dt class="text-muted-foreground">Template</dt>
                <dd class="flex items-center gap-1.5"><component :is="osIcon(vm.os)" class="text-muted-foreground size-3.5" /> {{ vm.templateName }}</dd>
                <dt class="text-muted-foreground">Size</dt>
                <dd>{{ vm.cores }} CPUs · {{ formatMemory(vm.memoryMiB) }}</dd>
                <dt class="text-muted-foreground">Proxmox ID</dt>
                <dd class="font-mono text-xs leading-5">{{ vm.vmid }}</dd>
                <dt class="text-muted-foreground">Created</dt>
                <dd :title="created.toLocaleString()">{{ formatAgo(vm.createdAt) }}</dd>
                <template v-if="vm.owner">
                  <dt class="text-muted-foreground">Owner</dt>
                  <dd>{{ vm.owner }}</dd>
                </template>
              </dl>
            </CardContent>
          </Card>

          <Card class="gap-3 py-4">
            <CardHeader class="px-4"><CardTitle class="flex items-center gap-2 text-sm"><Globe class="size-4" /> Networking</CardTitle></CardHeader>
            <CardContent class="px-4">
              <dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 text-sm">
                <dt class="text-muted-foreground">Network</dt>
                <dd>{{ vm.networkLabel }}</dd>
                <dt class="text-muted-foreground">Link</dt>
                <dd>
                  <span v-if="vm.link" class="flex items-center gap-1.5">
                    <span class="size-1.5 rounded-full" :class="linkColor(vm.link.network).dot" />
                    {{ vm.link.name }}
                    <span class="text-muted-foreground font-mono text-xs">{{ vm.link.address }}</span>
                  </span>
                  <span v-else class="text-muted-foreground">Not linked</span>
                </dd>
                <dt class="text-muted-foreground">Agent</dt>
                <dd v-if="vm.agent?.status === 'connected'" class="flex min-w-0 items-start gap-1.5">
                  <span class="mt-1.5 inline-block size-1.5 shrink-0 rounded-full bg-emerald-500" />
                  <span class="min-w-0">
                    <span class="block truncate" :title="vm.agent.hostname">{{ vm.agent.hostname }}</span>
                    <span class="text-muted-foreground block text-xs">{{ vm.agent.os }} · {{ vm.agent.ip }}</span>
                  </span>
                </dd>
                <dd v-else class="text-muted-foreground">{{ agentLine }}</dd>
              </dl>
            </CardContent>
          </Card>
        </div>

        <Card class="gap-3 py-4">
          <CardHeader class="px-4"><CardTitle class="flex items-center gap-2 text-sm"><Save class="size-4" /> Snapshot</CardTitle></CardHeader>
          <CardContent class="px-4 text-sm">
            <p v-if="vm.snapshot" class="flex flex-wrap items-center gap-x-2">
              Taken {{ formatAgo(vm.snapshot.takenAt) }}
              <span class="text-muted-foreground">
                ({{ new Date(vm.snapshot.takenAt).toLocaleString() }}, {{ vm.snapshot.memory ? 'with memory' : 'disks only' }})
              </span>
            </p>
            <p v-else class="text-muted-foreground flex items-center gap-1.5">
              <History class="size-3.5" /> None yet. Take one from the actions menu to come back to this point later.
            </p>
          </CardContent>
        </Card>

        <Card v-if="vm.agent" class="gap-3 py-4">
          <CardHeader class="px-4"><CardTitle class="flex items-center gap-2 text-sm"><SquareTerminal class="size-4" /> Run a command</CardTitle></CardHeader>
          <CardContent class="px-4">
            <AgentPanel v-if="running" :vm="vm" />
            <p v-else class="text-muted-foreground text-sm">Start the VM to run commands through its agent.</p>
          </CardContent>
        </Card>
      </div>

      <!-- Console preview -->
      <Card class="h-fit gap-3 overflow-hidden py-0">
        <component
          :is="canConnect ? RouterLink : 'div'"
          :to="{ name: 'console', params: { id: vm.id } }"
          class="bg-muted relative block aspect-video"
        >
          <img v-if="preview" :src="preview" alt="" class="size-full object-cover" />
          <div v-else class="text-muted-foreground flex size-full items-center justify-center">
            <component :is="osIcon(vm.os)" class="size-10 opacity-40" />
          </div>
        </component>
        <CardContent class="px-4 pb-4">
          <p class="text-muted-foreground text-xs">
            <template v-if="!canConnect">Start the VM to use the console.</template>
            <template v-else-if="preview">The console as it last looked; click to open it.</template>
            <template v-else>Click to open the console.</template>
          </p>
        </CardContent>
      </Card>
    </div>
  </div>
</template>
