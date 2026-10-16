<script setup lang="ts">
import {
  ArrowDownLeft,
  ArrowUpRight,
  Ban,
  Check,
  ChevronDown,
  Globe,
  GripVertical,
  Network,
  Pencil,
  Plus,
  ShieldCheck,
  Trash2,
} from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { api, errorMessage, type VM } from '@/api'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import FirewallRuleDialog from '@/components/vm/FirewallRuleDialog.vue'
import {
  type FirewallAction,
  type FirewallNetwork,
  type FirewallRule,
  type NetworkFirewall,
  networkLabels,
  presets,
  protocolLabel,
  remoteLabel,
  type VMFirewall,
} from '@/lib/firewall'

// The VM's firewall, edited as a whole and applied with one click.

const props = defineProps<{ vm: VM }>()

const ruleCount = (n: NetworkFirewall) => {
  const on = n.rules.filter((r) => r.enabled).length
  const off = n.rules.length - on
  if (!n.rules.length) return 'No rules.'
  return `${on} rule${on === 1 ? '' : 's'}${off ? ` (${off} off)` : ''}.`
}
// Rules are plain data; a JSON copy also sheds Vue's reactive proxies.
const copy = (f: VMFirewall): VMFirewall => JSON.parse(JSON.stringify(f))
const saved = ref<VMFirewall | null>(null)
const fw = ref<VMFirewall | null>(null)
const dirty = computed(() => !!fw.value && JSON.stringify(fw.value) !== JSON.stringify(saved.value))
const loadError = ref<string>()
const error = ref<string>()
const applying = ref(false)
const applied = ref(false)

onMounted(async () => {
  try {
    saved.value = await api.firewall(props.vm.id)
    fw.value = copy(saved.value)
  } catch (e) {
    loadError.value = errorMessage(e)
  }
})

async function apply() {
  if (!fw.value) return
  applying.value = true
  error.value = undefined
  try {
    const res = await api.setFirewall(props.vm.id, fw.value)
    saved.value = res.firewall
    fw.value = copy(res.firewall)
    if (res.warning) {
      error.value = res.warning
    } else {
      applied.value = true
      setTimeout(() => (applied.value = false), 2500)
    }
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    applying.value = false
  }
}
function discard() {
  if (saved.value) fw.value = copy(saved.value)
  error.value = undefined
}

const networks = computed(() => {
  const isolated = props.vm.networkLabel.toLowerCase().includes('isolated')
  return [
    {
      id: 'egress' as const,
      icon: Globe,
      title: networkLabels.egress,
      subtitle: props.vm.networkLabel,
      unavailable: isolated ? 'This VM has no egress network.' : '',
      note: '',
    },
    {
      id: 'link' as const,
      icon: Network,
      title: networkLabels.link,
      subtitle: props.vm.link?.name ?? 'Not linked',
      unavailable: '',
      note: props.vm.link ? '' : 'These rules apply once the VM is linked.',
    },
  ]
})

// The rule dialog.
const editing = ref<{ network: FirewallNetwork; rule: FirewallRule | null } | null>(null)
const dialogOpen = ref(false)
function addRule(network: FirewallNetwork) {
  editing.value = { network, rule: null }
  dialogOpen.value = true
}
function editRule(network: FirewallNetwork, rule: FirewallRule) {
  editing.value = { network, rule }
  dialogOpen.value = true
}
function saveRule(rule: FirewallRule) {
  if (!editing.value) return
  if (!fw.value) return
  const rules = fw.value[editing.value.network].rules
  const i = rules.findIndex((r) => r.id === rule.id)
  if (i >= 0) rules[i] = rule
  else rules.push(rule)
}
function removeRule(network: FirewallNetwork, id: string) {
  if (!fw.value) return
  const n = fw.value[network]
  n.rules = n.rules.filter((r) => r.id !== id)
}
function applyPreset(network: FirewallNetwork, id: string) {
  const p = presets.find((x) => x.id === id)
  if (p && fw.value) fw.value[network] = p.apply()
}
function setDefault(network: FirewallNetwork, dir: 'outbound' | 'inbound', v: unknown) {
  if (fw.value && (v === 'allow' || v === 'block')) fw.value[network][dir] = v as FirewallAction
}

// Reordering by dragging a row's handle.
const drag = ref<{ network: FirewallNetwork; index: number } | null>(null)
function onDragStart(network: FirewallNetwork, index: number, e: DragEvent) {
  drag.value = { network, index }
  e.dataTransfer?.setData('text/plain', '')
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function onDragOver(network: FirewallNetwork, index: number, e: DragEvent) {
  const d = drag.value
  if (!d || d.network !== network || !fw.value) return
  e.preventDefault()
  if (d.index === index) return
  const rules = fw.value[network].rules
  const [moved] = rules.splice(d.index, 1)
  rules.splice(index, 0, moved!)
  d.index = index
}
</script>

<template>
  <div class="flex max-w-5xl flex-col gap-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <h2 class="flex items-center gap-2 text-sm font-semibold"><ShieldCheck class="size-4" /> Firewall</h2>
      <div class="flex items-center gap-2" :class="{ invisible: !dirty && !applied && !applying }">
        <span v-if="applied" class="flex items-center gap-1 text-sm text-emerald-600"><Check class="size-4" /> Applied</span>
        <template v-else>
          <span class="text-muted-foreground text-sm">Unsaved changes</span>
          <Button variant="outline" size="sm" :disabled="applying" @click="discard">Discard</Button>
          <Button size="sm" :disabled="applying" @click="apply"><Spinner v-if="applying" /> Apply</Button>
        </template>
      </div>
    </div>
    <p v-if="error" class="text-destructive text-sm">{{ error }}</p>
    <p v-if="loadError" class="text-destructive text-sm">Couldn't load the firewall: {{ loadError }}</p>
    <div v-else-if="!fw" class="flex justify-center py-10"><Spinner class="size-5" /></div>

    <template v-if="fw">

    <section v-for="n in networks" :key="n.id" class="bg-card rounded-xl border">
      <header class="flex flex-wrap items-start gap-3 border-b px-4 py-3">
        <component :is="n.icon" class="text-muted-foreground mt-0.5 size-4" />
        <div class="min-w-0 flex-1">
          <h3 class="text-sm font-semibold">
            {{ n.title }} <span class="text-muted-foreground font-normal">· {{ n.subtitle }}</span>
          </h3>
          <p class="text-muted-foreground text-xs">
            {{ n.unavailable || ruleCount(fw[n.id]) }}
            <template v-if="n.note"> {{ n.note }}</template>
          </p>
        </div>
        <template v-if="!n.unavailable">
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="outline" size="sm">Presets <ChevronDown /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" class="w-72">
              <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">Replaces this network's rules</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                v-for="p in presets.filter((p) => p.networks.includes(n.id))"
                :key="p.id"
                class="flex-col items-start gap-0.5"
                @select="applyPreset(n.id, p.id)"
              >
                <span class="font-medium">{{ p.label }}</span>
                <span class="text-muted-foreground text-xs">{{ p.description }}</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <Button size="sm" @click="addRule(n.id)"><Plus /> Add rule</Button>
        </template>
      </header>

      <template v-if="!n.unavailable">
        <div class="flex flex-wrap items-center gap-x-6 gap-y-2 border-b px-4 py-2.5 text-sm">
          <span class="text-muted-foreground">If no rule matches:</span>
          <span class="flex items-center gap-2">
            <ArrowUpRight class="text-muted-foreground size-4" /> Outbound
            <ToggleGroup
              :model-value="fw[n.id].outbound"
              type="single"
              variant="outline"
              size="sm"
              @update:model-value="(v) => setDefault(n.id, 'outbound', v)"
            >
              <ToggleGroupItem value="allow" class="data-[state=on]:text-emerald-600">Allow</ToggleGroupItem>
              <ToggleGroupItem value="block" class="data-[state=on]:text-destructive">Block</ToggleGroupItem>
            </ToggleGroup>
          </span>
          <span class="flex items-center gap-2">
            <ArrowDownLeft class="text-muted-foreground size-4" /> Inbound
            <ToggleGroup
              :model-value="fw[n.id].inbound"
              type="single"
              variant="outline"
              size="sm"
              @update:model-value="(v) => setDefault(n.id, 'inbound', v)"
            >
              <ToggleGroupItem value="allow" class="data-[state=on]:text-emerald-600">Allow</ToggleGroupItem>
              <ToggleGroupItem value="block" class="data-[state=on]:text-destructive">Block</ToggleGroupItem>
            </ToggleGroup>
          </span>
        </div>

        <div v-if="!fw[n.id].rules.length" class="text-muted-foreground px-4 py-6 text-center text-sm">
          No rules. Add one, or start from a preset.
        </div>
        <table v-else class="w-full text-sm">
          <thead class="text-muted-foreground text-xs">
            <tr class="border-b">
              <th class="w-8"></th>
              <th class="w-10 py-2 text-left font-medium">On</th>
              <th class="py-2 text-left font-medium">Action</th>
              <th class="py-2 text-left font-medium">Direction</th>
              <th class="py-2 text-left font-medium">Protocol</th>
              <th class="py-2 text-left font-medium">Remote</th>
              <th class="py-2 text-left font-medium">Ports</th>
              <th class="py-2 text-left font-medium">Note</th>
              <th class="w-20"></th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(r, i) in fw[n.id].rules"
              :key="r.id"
              class="hover:bg-muted/40 border-b last:border-b-0"
              :class="{ 'opacity-50': !r.enabled, 'bg-muted/60': drag?.network === n.id && drag.index === i }"
              @dragover="onDragOver(n.id, i, $event)"
              @drop.prevent
            >
              <td class="text-muted-foreground pl-2">
                <span
                  draggable="true"
                  class="flex cursor-grab items-center justify-center active:cursor-grabbing"
                  title="Drag to reorder"
                  @dragstart="onDragStart(n.id, i, $event)"
                  @dragend="drag = null"
                >
                  <GripVertical class="size-4" />
                </span>
              </td>
              <td class="py-2"><Switch v-model="r.enabled" :aria-label="r.enabled ? 'Disable rule' : 'Enable rule'" /></td>
              <td class="py-2">
                <span
                  class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
                  :class="r.action === 'allow' ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400' : 'bg-destructive/10 text-destructive'"
                >
                  <Check v-if="r.action === 'allow'" class="size-3" /><Ban v-else class="size-3" />
                  {{ r.action === 'allow' ? 'Allow' : 'Block' }}
                </span>
              </td>
              <td class="py-2">
                <span class="inline-flex items-center gap-1">
                  <ArrowUpRight v-if="r.direction === 'out'" class="text-muted-foreground size-3.5" />
                  <ArrowDownLeft v-else class="text-muted-foreground size-3.5" />
                  {{ r.direction === 'out' ? 'Outbound' : 'Inbound' }}
                </span>
              </td>
              <td class="py-2">{{ r.protocol === 'any' ? 'Any' : protocolLabel(r.protocol) }}</td>
              <td class="py-2" :class="{ 'font-mono text-xs': r.remote !== 'any' && r.remote !== 'private' }">
                {{ remoteLabel(r.remote) }}
              </td>
              <td class="py-2 font-mono text-xs">{{ r.ports || (r.protocol === 'tcp' || r.protocol === 'udp' ? 'All' : '—') }}</td>
              <td class="text-muted-foreground max-w-48 truncate py-2">{{ r.comment }}</td>
              <td class="pr-2 text-right whitespace-nowrap">
                <Button variant="ghost" size="icon-sm" title="Edit" @click="editRule(n.id, r)"><Pencil /></Button>
                <Button variant="ghost" size="icon-sm" title="Delete" @click="removeRule(n.id, r.id)"><Trash2 /></Button>
              </td>
            </tr>
          </tbody>
        </table>
      </template>
    </section>

    </template>

    <FirewallRuleDialog
      v-model:open="dialogOpen"
      :rule="editing?.rule ?? null"
      :network-label="editing ? networkLabels[editing.network] : ''"
      @save="saveRule"
    />
  </div>
</template>
