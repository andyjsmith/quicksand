<script setup lang="ts">
import { RefreshCw, Search } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { api, errorMessage, type GuestConnection, type VM } from '@/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

const props = defineProps<{ vm: VM }>()

const connections = shallowRef<GuestConnection[] | null>(null)
const error = ref<string>()
const loading = ref(false)
const live = ref(false)
const filter = ref('')
const kind = ref<'all' | 'listening' | 'established' | 'udp'>('all')

async function refresh() {
  if (loading.value) return
  loading.value = true
  try {
    connections.value = (await api.connections(props.vm.id)).connections
    error.value = undefined
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  refresh()
  timer = setInterval(() => live.value && !document.hidden && refresh(), 3000)
})
onBeforeUnmount(() => clearInterval(timer))

const visible = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return (connections.value ?? []).filter((c) => {
    if (kind.value === 'listening' && c.state !== 'LISTEN') return false
    if (kind.value === 'established' && c.state !== 'ESTABLISHED') return false
    if (kind.value === 'udp' && !c.proto.startsWith('udp')) return false
    if (!q) return true
    return [c.local, c.remote, c.state, c.processName, String(c.pid ?? '')].some((v) => v?.toLowerCase().includes(q))
  })
})

const stateClass = (state?: string) =>
  state === 'ESTABLISHED' ? 'text-emerald-600 dark:text-emerald-400' : state === 'LISTEN' ? 'text-sky-600 dark:text-sky-400' : 'text-muted-foreground'
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex flex-wrap items-center gap-2">
      <div class="relative min-w-48 flex-1">
        <Search class="text-muted-foreground absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
        <Input v-model="filter" placeholder="Filter by address, port, process or PID" class="pl-8" />
      </div>
      <ToggleGroup v-model="kind" type="single" variant="outline" size="sm">
        <ToggleGroupItem value="all">All</ToggleGroupItem>
        <ToggleGroupItem value="listening">Listening</ToggleGroupItem>
        <ToggleGroupItem value="established">Established</ToggleGroupItem>
        <ToggleGroupItem value="udp">UDP</ToggleGroupItem>
      </ToggleGroup>
      <div class="flex items-center gap-2">
        <Switch id="conn-live" v-model="live" />
        <Label for="conn-live" class="text-xs">Live</Label>
      </div>
      <Button variant="outline" size="sm" :disabled="loading" @click="refresh">
        <RefreshCw :class="{ 'animate-spin': loading }" /> Refresh
      </Button>
    </div>
    <p v-if="error" class="text-destructive text-sm">{{ error }}</p>

    <div class="overflow-x-auto rounded-md border">
      <Table class="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead class="w-16">Proto</TableHead>
            <TableHead>Local address</TableHead>
            <TableHead>Remote address</TableHead>
            <TableHead class="w-28">State</TableHead>
            <TableHead class="w-20">PID</TableHead>
            <TableHead>Process</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(c, i) in visible" :key="i">
            <TableCell class="font-mono">{{ c.proto }}</TableCell>
            <TableCell class="font-mono">{{ c.local }}</TableCell>
            <TableCell class="font-mono">{{ c.remote }}</TableCell>
            <TableCell :class="stateClass(c.state)">{{ c.state }}</TableCell>
            <TableCell class="font-mono tabular-nums">{{ c.pid || '' }}</TableCell>
            <TableCell>{{ c.processName }}</TableCell>
          </TableRow>
          <TableEmpty v-if="connections && !visible.length" :colspan="6">No matching connections.</TableEmpty>
        </TableBody>
      </Table>
    </div>
  </div>
</template>
