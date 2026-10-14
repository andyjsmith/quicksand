<script setup lang="ts">
import { ArrowDown, ArrowUp, RefreshCw, Search, Skull, X } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { api, errorMessage, type GuestProcess, type ProcessList, type VM } from '@/api'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { formatBytes, formatCpuTime, formatDateTime } from '@/lib/format'

const props = defineProps<{ vm: VM }>()

const REFRESH_MS = 3000

interface Row extends GuestProcess {
  /** Share of the whole guest's CPU since the previous refresh, 0..100. */
  cpu: number | null
}

const list = shallowRef<ProcessList | null>(null)
const rows = shallowRef<Row[]>([])
const error = ref<string>()
const loading = ref(false)
const live = ref(true)
const filter = ref('')
type SortKey = 'pid' | 'name' | 'user' | 'cpu' | 'memory'
const sortKey = ref<SortKey>('cpu')
const sortDesc = ref(true)

async function refresh() {
  if (loading.value) return
  loading.value = true
  try {
    const next = await api.processes(props.vm.id)
    const prev = list.value
    const prevTimes = new Map(prev?.processes.map((p) => [p.pid, p.cpuTimeMs]))
    const elapsed = prev ? new Date(next.time).getTime() - new Date(prev.time).getTime() : 0
    rows.value = next.processes.map((p) => {
      const before = prevTimes.get(p.pid)
      const cpu =
        elapsed > 0 && before !== undefined
          ? Math.max(0, ((p.cpuTimeMs - before) / (elapsed * next.numCPU)) * 100)
          : null
      return { ...p, cpu }
    })
    list.value = next
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
  timer = setInterval(() => live.value && !document.hidden && refresh(), REFRESH_MS)
})
onBeforeUnmount(() => clearInterval(timer))

const visible = computed(() => {
  const q = filter.value.trim().toLowerCase()
  const filtered = q
    ? rows.value.filter(
        (p) =>
          String(p.pid) === q ||
          p.name.toLowerCase().includes(q) ||
          p.command?.toLowerCase().includes(q) ||
          p.user?.toLowerCase().includes(q),
      )
    : rows.value
  const dir = sortDesc.value ? -1 : 1
  const key = sortKey.value
  return [...filtered].sort((a, b) => {
    const av = a[key] ?? -1
    const bv = b[key] ?? -1
    if (typeof av === 'string' || typeof bv === 'string') return String(av).localeCompare(String(bv)) * dir
    return (av - bv) * dir
  })
})

function sortBy(key: SortKey) {
  if (sortKey.value === key) sortDesc.value = !sortDesc.value
  else {
    sortKey.value = key
    sortDesc.value = key === 'cpu' || key === 'memory'
  }
}

const memoryTotal = computed(() => list.value?.memoryTotal ?? 0)

const killing = ref<Row | null>(null)
const force = ref(false)
const killError = ref<string>()
const isWindows = computed(() => props.vm.agent?.os === 'windows')

function askKill(p: Row) {
  killing.value = p
  force.value = false
  killError.value = undefined
}

async function kill() {
  const p = killing.value
  if (!p) return
  try {
    await api.kill(props.vm.id, p.pid, force.value || isWindows.value)
    killing.value = null
    refresh()
  } catch (e) {
    killError.value = errorMessage(e)
  }
}

const columns: { key: SortKey; label: string; class?: string }[] = [
  { key: 'pid', label: 'PID', class: 'w-20' },
  { key: 'name', label: 'Name' },
  { key: 'user', label: 'User' },
  { key: 'cpu', label: 'CPU', class: 'w-20 text-right' },
  { key: 'memory', label: 'Memory', class: 'w-24 text-right' },
]
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex flex-wrap items-center gap-2">
      <div class="relative min-w-48 flex-1">
        <Search class="text-muted-foreground absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
        <Input v-model="filter" placeholder="Filter by name, PID, user or command" class="pl-8" />
      </div>
      <div class="flex items-center gap-2">
        <Switch id="proc-live" v-model="live" />
        <Label for="proc-live" class="text-xs">Live</Label>
      </div>
      <Button variant="outline" size="sm" :disabled="loading" @click="refresh">
        <RefreshCw :class="{ 'animate-spin': loading }" /> Refresh
      </Button>
    </div>
    <p v-if="error" class="text-destructive text-sm">{{ error }}</p>
    <p v-if="list" class="text-muted-foreground text-xs">
      {{ visible.length }} of {{ rows.length }} processes · {{ list.numCPU }} CPUs
      <template v-if="memoryTotal"> · {{ formatBytes(memoryTotal) }} memory</template>
    </p>

    <div class="overflow-x-auto rounded-md border">
      <Table class="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead v-for="c in columns" :key="c.key" :class="c.class">
              <button type="button" class="inline-flex items-center gap-1" @click="sortBy(c.key)">
                {{ c.label }}
                <component :is="sortDesc ? ArrowDown : ArrowUp" v-if="sortKey === c.key" class="size-3" />
              </button>
            </TableHead>
            <TableHead>Command</TableHead>
            <TableHead class="w-10" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="p in visible" :key="p.pid">
            <TableCell class="font-mono tabular-nums">{{ p.pid }}</TableCell>
            <TableCell class="max-w-48 truncate font-medium" :title="`Parent PID ${p.ppid}${p.started ? ' · started ' + formatDateTime(p.started) : ''}${p.threads ? ' · ' + p.threads + ' threads' : ''} · CPU time ${formatCpuTime(p.cpuTimeMs)}`">
              {{ p.name }}
            </TableCell>
            <TableCell class="text-muted-foreground max-w-40 truncate">{{ p.user }}</TableCell>
            <TableCell class="text-right tabular-nums">{{ p.cpu === null ? '' : p.cpu.toFixed(1) + '%' }}</TableCell>
            <TableCell class="text-right tabular-nums" :title="memoryTotal ? ((p.memory / memoryTotal) * 100).toFixed(1) + '%' : undefined">
              {{ p.memory ? formatBytes(p.memory) : '' }}
            </TableCell>
            <TableCell class="text-muted-foreground max-w-96 truncate font-mono" :title="p.command">{{ p.command }}</TableCell>
            <TableCell>
              <Button variant="ghost" size="icon-sm" title="End process" @click="askKill(p)"><X /></Button>
            </TableCell>
          </TableRow>
          <TableEmpty v-if="list && !visible.length" :colspan="7">No matching processes.</TableEmpty>
        </TableBody>
      </Table>
    </div>

    <AlertDialog :open="!!killing" @update:open="(o) => !o && (killing = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>End {{ killing?.name }} ({{ killing?.pid }})?</AlertDialogTitle>
          <AlertDialogDescription>
            <template v-if="isWindows">The process is terminated immediately.</template>
            <template v-else>The process is asked to exit (SIGTERM), or killed outright (SIGKILL) if forced.</template>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div v-if="!isWindows" class="flex items-center gap-2">
          <Checkbox id="kill-force" v-model="force" />
          <Label for="kill-force">Force (SIGKILL)</Label>
        </div>
        <p v-if="killError" class="text-destructive text-sm">{{ killError }}</p>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button variant="destructive" @click="kill"><Skull /> End process</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
