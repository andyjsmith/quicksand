<script setup lang="ts">
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, ListTree, List as ListIcon, RefreshCw, Search, Skull, X } from '@lucide/vue'
import { useLocalStorage } from '@vueuse/core'
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
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
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

const view = useLocalStorage<'list' | 'tree'>('quicksand.processView', 'list')

const matches = (p: Row, q: string) =>
  String(p.pid) === q ||
  p.name.toLowerCase().includes(q) ||
  !!p.command?.toLowerCase().includes(q) ||
  !!p.user?.toLowerCase().includes(q) ||
  !!p.services?.some((s) => s.name.toLowerCase().includes(q) || s.displayName?.toLowerCase().includes(q))

function compare(a: Row, b: Row) {
  const dir = sortDesc.value ? -1 : 1
  const av = a[sortKey.value] ?? -1
  const bv = b[sortKey.value] ?? -1
  if (typeof av === 'string' || typeof bv === 'string') return String(av).localeCompare(String(bv)) * dir
  return (av - bv) * dir
}

interface Line {
  p: Row
  depth: number
  hasChildren: boolean
  /** Shown only as the ancestor of a filter match. */
  context: boolean
}

// Processes collapsed in the tree, by PID.
const collapsed = ref(new Set<number>())
function toggleCollapsed(pid: number) {
  const next = new Set(collapsed.value)
  if (!next.delete(pid)) next.add(pid)
  collapsed.value = next
}

const visible = computed<Line[]>(() => {
  const q = filter.value.trim().toLowerCase()
  if (view.value === 'list') {
    return rows.value
      .filter((p) => !q || matches(p, q))
      .sort(compare)
      .map((p) => ({ p, depth: 0, hasChildren: false, context: false }))
  }
  // A parent must have started before its child: Windows reuses PIDs, so
  // a recorded parent PID can belong to a newer, unrelated process.
  const byPid = new Map(rows.value.map((p) => [p.pid, p]))
  const parentOf = (p: Row) => {
    const parent = byPid.get(p.ppid)
    if (!parent || parent.pid === p.pid) return undefined
    if (parent.started && p.started && parent.started > p.started) return undefined
    return parent
  }
  const children = new Map<number, Row[]>()
  const roots: Row[] = []
  for (const p of rows.value) {
    const parent = parentOf(p)
    if (parent) children.set(parent.pid, [...(children.get(parent.pid) ?? []), p])
    else roots.push(p)
  }
  // With a filter, show matches and the processes leading to them.
  let shown: Set<number> | undefined
  if (q) {
    shown = new Set()
    for (const p of rows.value) {
      if (!matches(p, q)) continue
      for (let cur: Row | undefined = p; cur && !shown.has(cur.pid); cur = parentOf(cur)) shown.add(cur.pid)
    }
  }
  const out: Line[] = []
  const seen = new Set<number>()
  const walk = (list: Row[], depth: number) => {
    for (const p of [...list].sort(compare)) {
      if (seen.has(p.pid) || (shown && !shown.has(p.pid))) continue
      seen.add(p.pid)
      const kids = children.get(p.pid) ?? []
      out.push({ p, depth, hasChildren: kids.length > 0, context: !!q && !matches(p, q) })
      // A filter opens the branches it found matches in.
      if (!collapsed.value.has(p.pid) || q) walk(kids, depth + 1)
    }
  }
  walk(roots, 0)
  return out
})

const serviceSummary = (p: Row) => {
  const names = p.services?.map((s) => s.name) ?? []
  return names.length > 3 ? `${names.slice(0, 3).join(', ')} +${names.length - 3}` : names.join(', ')
}
const serviceTitle = (p: Row) =>
  p.services?.map((s) => (s.displayName && s.displayName !== s.name ? `${s.name} — ${s.displayName}` : s.name)).join('\n')

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
        <Input v-model="filter" placeholder="Filter by name, PID, user, command or service" class="pl-8" />
      </div>
      <!-- Clicking the selected item would deselect it: keep one chosen. -->
      <ToggleGroup
        :model-value="view"
        type="single"
        variant="outline"
        size="sm"
        @update:model-value="(v) => v && (view = v as 'list' | 'tree')"
      >
        <ToggleGroupItem value="list" title="List"><ListIcon /> List</ToggleGroupItem>
        <ToggleGroupItem value="tree" title="Tree"><ListTree /> Tree</ToggleGroupItem>
      </ToggleGroup>
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
          <TableRow v-for="{ p, depth, hasChildren, context } in visible" :key="p.pid" :class="{ 'opacity-50': context }">
            <TableCell class="font-mono tabular-nums">{{ p.pid }}</TableCell>
            <TableCell class="max-w-md">
              <div class="flex min-w-0 items-center gap-1" :style="{ paddingLeft: `${depth * 14}px` }">
                <button
                  v-if="view === 'tree' && hasChildren && !filter.trim()"
                  type="button"
                  class="text-muted-foreground hover:text-foreground -ml-1 shrink-0"
                  :title="collapsed.has(p.pid) ? 'Expand' : 'Collapse'"
                  @click="toggleCollapsed(p.pid)"
                >
                  <component :is="collapsed.has(p.pid) ? ChevronRight : ChevronDown" class="size-3.5" />
                </button>
                <span v-else-if="view === 'tree'" class="inline-block w-2.5 shrink-0" />
                <span
                  class="truncate font-medium"
                  :title="`Parent PID ${p.ppid}${p.started ? ' · started ' + formatDateTime(p.started) : ''}${p.threads ? ' · ' + p.threads + ' threads' : ''} · CPU time ${formatCpuTime(p.cpuTimeMs)}`"
                >
                  {{ p.name }}
                </span>
                <span v-if="p.services?.length" class="text-muted-foreground truncate text-[11px]" :title="serviceTitle(p)">
                  {{ serviceSummary(p) }}
                </span>
              </div>
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
