<script setup lang="ts">
import { ArrowLeft, CircleAlert, Monitor, SquareTerminal, TriangleAlert } from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import ActionProgressBar from '@/components/ActionProgressBar.vue'
import AppHeader from '@/components/AppHeader.vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AgentPanel from '@/components/console/AgentPanel.vue'
import ExpiryBadge from '@/components/ExpiryBadge.vue'
import VmActionsMenu from '@/components/VmActionsMenu.vue'
import ConnectionsTab from '@/components/vm/ConnectionsTab.vue'
import FilesTab from '@/components/vm/FilesTab.vue'
import ProcessesTab from '@/components/vm/ProcessesTab.vue'
import ShellTab from '@/components/vm/ShellTab.vue'
import { useMachines } from '@/composables/useMachines'
import { formatMemory, formatUptime, vmStatus } from '@/lib/format'

const props = defineProps<{ id: string }>()

const router = useRouter()
const machines = useMachines()
onBeforeUnmount(() => machines.stop())
const vm = machines.get(props.id)

const error = ref<string>()
const status = computed(() => (vm.value ? vmStatus(vm.value) : null))
const running = computed(() => vm.value?.state === 'ready' && vm.value.power === 'running')
const canConnect = computed(() => running.value && (vm.value?.consoles.length ?? 0) > 0)
const agentReady = computed(() => running.value && vm.value?.agent?.status === 'connected')
const shellReady = computed(() => agentReady.value || (running.value && !!vm.value?.serial))
const tab = ref('overview')

const details = computed(() => {
  const v = vm.value
  if (!v) return []
  return [
    ['Template', v.templateName],
    ['Proxmox VM ID', String(v.vmid)],
    ['CPUs', String(v.cores)],
    ['Memory', formatMemory(v.memoryMiB)],
    ['Network', v.networkLabel],
    ['Uptime', running.value ? formatUptime(v.uptime) : ''],
    ['Agent', v.agent ? `${v.agent.status} (${v.agent.ip})` : 'none'],
    ['Guest', v.agent?.hostname ? `${v.agent.hostname} · ${v.agent.os} · agent ${v.agent.version}` : ''],
    ['Created', new Date(v.createdAt).toLocaleString()],
    ['Link network', v.link ? `${v.link.name} · ${v.link.address}` : ''],
    ['Snapshot', v.snapshot ? `${new Date(v.snapshot.takenAt).toLocaleString()}${v.snapshot.memory ? ' (with memory)' : ' (disks only)'}` : ''],
  ].filter(([, value]) => value)
})
</script>

<template>
  <div class="flex min-h-svh flex-col">
    <AppHeader />
    <main class="mx-auto flex w-full max-w-6xl flex-col gap-4 px-4 py-6">
      <Button variant="ghost" size="sm" as-child class="self-start">
        <RouterLink to="/"><ArrowLeft /> Machines</RouterLink>
      </Button>

      <p v-if="machines.loaded.value && !vm" class="text-muted-foreground">This VM doesn't exist (any more).</p>

      <template v-if="vm">
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-0 flex-1">
            <h1 class="truncate text-xl font-semibold">{{ vm.name }}</h1>
            <p class="text-muted-foreground flex items-center gap-1.5 text-sm">
              <span class="inline-block size-2 rounded-full" :class="status?.dot" />
              {{ status?.label }}
              <ExpiryBadge v-if="vm.expiresAt" :vm="vm" extendable class="ml-2" @error="error = $event" />
            </p>
          </div>
          <Button v-if="canConnect" as-child>
            <RouterLink :to="{ name: 'console', params: { id: vm.id } }"><Monitor /> Open console</RouterLink>
          </Button>
          <Button v-if="shellReady" variant="outline" @click="tab = 'shell'"><SquareTerminal /> Shell</Button>
          <VmActionsMenu :vm="vm" @error="error = $event" @destroyed="router.push('/')" />
        </div>
        <ActionProgressBar v-if="vm.actionProgress" :progress="vm.actionProgress" class="max-w-md" />
        <p v-if="error || vm.actionError || vm.error" class="text-destructive flex gap-1.5 text-sm">
          <CircleAlert class="mt-0.5 size-4 shrink-0" /> {{ error || vm.actionError || vm.error }}
        </p>

        <Tabs v-model="tab">
          <TabsList>
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="processes" :disabled="!agentReady">Processes</TabsTrigger>
            <TabsTrigger value="network" :disabled="!agentReady">Network</TabsTrigger>
            <TabsTrigger value="files" :disabled="!agentReady">Files</TabsTrigger>
            <TabsTrigger value="shell" :disabled="!shellReady">Shell</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" class="flex flex-col gap-6 pt-2">
            <div
              v-if="vm.link?.status === 'manual'"
              class="flex max-w-2xl gap-2.5 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm"
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
            <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-sm">
              <template v-for="[label, value] in details" :key="label">
                <dt class="text-muted-foreground">{{ label }}</dt>
                <dd>{{ value }}</dd>
              </template>
            </dl>
            <section v-if="vm.agent && running" class="flex max-w-2xl flex-col gap-2">
              <h2 class="text-sm font-semibold">Run a command</h2>
              <AgentPanel :vm="vm" />
            </section>
            <p v-else-if="vm.agent" class="text-muted-foreground text-sm">
              Start the VM to use its agent.
            </p>
          </TabsContent>
          <TabsContent value="processes" class="pt-2">
            <ProcessesTab v-if="agentReady" :vm="vm" />
          </TabsContent>
          <TabsContent value="network" class="pt-2">
            <ConnectionsTab v-if="agentReady" :vm="vm" />
          </TabsContent>
          <TabsContent value="files" class="pt-2">
            <FilesTab v-if="agentReady" :vm="vm" />
          </TabsContent>
          <!-- Kept mounted so a session survives switching tabs. -->
          <TabsContent value="shell" force-mount class="pt-2 data-[state=inactive]:hidden">
            <ShellTab v-if="shellReady" :vm="vm" />
          </TabsContent>
        </Tabs>
      </template>
    </main>
  </div>
</template>
