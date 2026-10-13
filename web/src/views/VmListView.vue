<script setup lang="ts">
import { useTitle } from '@vueuse/core'
import { CircleAlert, Monitor, RefreshCw, Server } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { listVMs, type VM } from '@/api'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { consoleDrivers } from '@/consoles/registry'

useTitle('Quicksand')

const vms = ref<VM[]>([])
const loading = ref(false)
const error = ref<string>()

async function load() {
  loading.value = true
  error.value = undefined
  try {
    vms.value = (await listVMs()).sort((a, b) => a.vmid - b.vmid)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function formatMemory(bytes: number) {
  return `${(bytes / 2 ** 30).toFixed(1)} GiB`
}

function formatUptime(seconds: number) {
  if (!seconds) return '—'
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`
}
</script>

<template>
  <div class="bg-background text-foreground min-h-dvh">
    <header class="border-b">
      <div class="mx-auto flex max-w-6xl items-center gap-2 px-4 py-3">
        <Server class="size-4" />
        <h1 class="text-sm font-semibold">Quicksand</h1>
      </div>
    </header>

    <main class="mx-auto flex max-w-6xl flex-col gap-4 px-4 py-6">
      <Alert v-if="error" variant="destructive">
        <CircleAlert />
        <AlertTitle>Couldn't load virtual machines</AlertTitle>
        <AlertDescription>{{ error }}</AlertDescription>
      </Alert>

      <Card>
        <CardHeader class="flex flex-row items-start justify-between gap-4">
          <div class="flex flex-col gap-1">
            <CardTitle>Virtual machines</CardTitle>
            <CardDescription>Open a console to a guest over SPICE or VNC.</CardDescription>
          </div>
          <Button variant="outline" :disabled="loading" @click="load">
            <Spinner v-if="loading" />
            <RefreshCw v-else />
            Refresh
          </Button>
        </CardHeader>
        <CardContent>
          <div class="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead class="w-20">ID</TableHead>
                  <TableHead>Name</TableHead>
                  <TableHead>Node</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead class="text-right">Memory</TableHead>
                  <TableHead class="text-right">Uptime</TableHead>
                  <TableHead class="text-right">Console</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="vm in vms" :key="`${vm.node}/${vm.vmid}`">
                  <TableCell class="font-mono">{{ vm.vmid }}</TableCell>
                  <TableCell class="font-medium">
                    {{ vm.name }}
                    <Badge v-if="vm.template" variant="secondary" class="ml-1">template</Badge>
                  </TableCell>
                  <TableCell>{{ vm.node }}</TableCell>
                  <TableCell>
                    <span class="inline-flex items-center gap-1.5">
                      <span
                        class="inline-block size-2 rounded-full"
                        :class="vm.status === 'running' ? 'bg-emerald-500' : 'bg-muted-foreground'"
                      />
                      {{ vm.status }}
                    </span>
                  </TableCell>
                  <TableCell class="text-right tabular-nums">
                    {{ formatMemory(vm.mem) }} / {{ formatMemory(vm.maxmem) }}
                  </TableCell>
                  <TableCell class="text-right tabular-nums">{{ formatUptime(vm.uptime) }}</TableCell>
                  <TableCell class="text-right">
                    <div class="inline-flex gap-1.5">
                      <Button
                        v-for="(protocol, i) in vm.consoles"
                        :key="protocol"
                        :variant="i === 0 ? 'default' : 'outline'"
                        size="sm"
                        :disabled="vm.status !== 'running'"
                        :title="consoleDrivers[protocol].description"
                        as-child
                      >
                        <RouterLink
                          :to="{ name: 'console', params: { node: vm.node, vmid: vm.vmid }, query: { protocol } }"
                        >
                          <Monitor v-if="i === 0" />
                          {{ consoleDrivers[protocol].label }}
                        </RouterLink>
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
                <TableEmpty v-if="!loading && !vms.length" :colspan="7">No virtual machines found.</TableEmpty>
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
    </main>
  </div>
</template>
