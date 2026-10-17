<script setup lang="ts">
import { ArrowLeft, CircleAlert, Monitor, SquareTerminal } from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import ActionProgressBar from '@/components/ActionProgressBar.vue'
import AppHeader from '@/components/AppHeader.vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import ExpiryBadge from '@/components/ExpiryBadge.vue'
import SuspendBadge from '@/components/SuspendBadge.vue'
import VmActionsMenu from '@/components/VmActionsMenu.vue'
import ConnectionsTab from '@/components/vm/ConnectionsTab.vue'
import FilesTab from '@/components/vm/FilesTab.vue'
import FirewallTab from '@/components/vm/FirewallTab.vue'
import OverviewTab from '@/components/vm/OverviewTab.vue'
import ProcessesTab from '@/components/vm/ProcessesTab.vue'
import ShellTab from '@/components/vm/ShellTab.vue'
import { useMachines } from '@/composables/useMachines'
import { vmStatus } from '@/lib/format'

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
              <SuspendBadge :vm="vm" class="ml-2" />
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
            <TabsTrigger value="firewall" :disabled="vm.state !== 'ready'">Firewall</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" class="pt-2">
            <OverviewTab :vm="vm" />
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
          <TabsContent value="firewall" class="pt-2">
            <FirewallTab :vm="vm" />
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
