<script setup lang="ts">
import { Link2, Merge, TriangleAlert } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api, errorMessage, type VM } from '@/api'
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Spinner } from '@/components/ui/spinner'
import { linkColor } from '@/composables/useLinks'
import { useMachines } from '@/composables/useMachines'

// Picks the machines to link a VM with. Linking joins them all into one
// network: the one some are in already, or a new one; machines from
// different networks merge those networks, after asking.

const props = defineProps<{ vm: VM }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ warning: [message: string] }>()

const machines = useMachines()
onBeforeUnmount(machines.stop)

const selected = ref<Set<string>>(new Set())
const error = ref<string>()
const busy = ref(false)
const confirmingMerge = ref(false)
watch(open, (o) => {
  if (o) {
    selected.value = new Set()
    error.value = undefined
  }
})

const sameNetwork = (v: VM) => !!props.vm.link && v.link?.network === props.vm.link.network
const candidates = computed(() =>
  machines.machines.value
    .filter((v) => v.id !== props.vm.id && v.state !== 'destroying')
    .map((v) => ({
      vm: v,
      already: sameNetwork(v),
      disabled: sameNetwork(v) || v.state !== 'ready',
    })),
)

function toggle(id: string, on: boolean) {
  const next = new Set(selected.value)
  if (on) next.add(id)
  else next.delete(id)
  selected.value = next
}

const chosen = computed(() => machines.machines.value.filter((v) => selected.value.has(v.id)))
/** The networks the link touches, oldest (the one kept) first. */
const networks = computed(() => {
  const byId = new Map<number, string>()
  for (const v of [props.vm, ...chosen.value]) if (v.link) byId.set(v.link.network, v.link.name)
  return [...byId].sort(([a], [b]) => a - b).map(([id, name]) => ({ id, name }))
})
const merging = computed(() => networks.value.length > 1)
/** Machines that will join a network and have no agent to set their address. */
const manual = computed(() =>
  [props.vm, ...chosen.value].filter((v) => !v.agent && v.link?.network !== networks.value[0]?.id),
)
const summary = computed(() => {
  const n = chosen.value.length
  if (!n) return ''
  const names = (vms: VM[]) => vms.map((v) => v.name).join(', ')
  if (merging.value) {
    const [keep, ...rest] = networks.value
    return `Merges ${rest.map((r) => r.name).join(' and ')} into ${keep!.name}, so every machine in them can reach each other.`
  }
  if (networks.value.length === 1) {
    const joining = [props.vm, ...chosen.value].filter((v) => !v.link)
    return `Adds ${names(joining)} to ${networks.value[0]!.name}.`
  }
  return `Creates a new network for ${props.vm.name} and ${names(chosen.value)}.`
})

async function submit(merge = false) {
  if (merging.value && !merge) {
    confirmingMerge.value = true
    return
  }
  confirmingMerge.value = false
  busy.value = true
  error.value = undefined
  try {
    const res = await api.link(props.vm.id, [...selected.value], merge)
    if (res.warning) emit('warning', res.warning)
    open.value = false
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>Link {{ vm.name }}</DialogTitle>
        <DialogDescription>
          Linked machines share a private network: they can reach each other freely, and nothing else.
        </DialogDescription>
      </DialogHeader>

      <div v-if="!candidates.length" class="text-muted-foreground rounded-lg border border-dashed px-4 py-6 text-center text-sm">
        You have no other machines to link with.
      </div>
      <ul v-else class="-mx-1 flex max-h-72 flex-col gap-0.5 overflow-y-auto px-1">
        <li v-for="c in candidates" :key="c.vm.id">
          <label
            class="hover:bg-muted/60 flex items-center gap-3 rounded-md px-2 py-2"
            :class="c.disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'"
          >
            <Checkbox
              :model-value="c.already || selected.has(c.vm.id)"
              :disabled="c.disabled"
              @update:model-value="(on) => toggle(c.vm.id, on === true)"
            />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm font-medium">{{ c.vm.name }}</span>
              <span class="text-muted-foreground block truncate text-xs">
                {{ c.vm.templateName }}
                <template v-if="c.vm.state !== 'ready'"> · {{ c.vm.state }}</template>
                <template v-else-if="!c.vm.agent"> · no agent</template>
              </span>
            </span>
            <span
              v-if="c.vm.link"
              class="flex shrink-0 items-center gap-1.5 text-xs font-medium"
              :class="linkColor(c.vm.link.network).text"
            >
              <span class="size-1.5 rounded-full" :class="linkColor(c.vm.link.network).dot" />
              {{ c.already ? 'Already linked' : c.vm.link.name }}
            </span>
          </label>
        </li>
      </ul>

      <p v-if="summary" class="text-sm">{{ summary }}</p>
      <div v-if="manual.length" class="flex gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs">
        <TriangleAlert class="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
        <span>
          {{ manual.map((v) => v.name).join(', ') }} {{ manual.length === 1 ? 'has' : 'have' }} no agent to configure
          the network. You'll need to set {{ manual.length === 1 ? 'its address' : 'their addresses' }} in the guest
          yourself; {{ manual.length === 1 ? "it's" : "they're" }} shown on the machine's page.
        </span>
      </div>
      <p v-if="error" class="text-destructive text-sm">{{ error }}</p>

      <DialogFooter>
        <Button variant="outline" @click="open = false">Cancel</Button>
        <Button :disabled="!selected.size || busy" @click="submit()">
          <Spinner v-if="busy" /> <Merge v-else-if="merging" /> <Link2 v-else />
          {{ merging ? 'Merge and link' : 'Link' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <AlertDialog v-model:open="confirmingMerge">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Merge networks?</AlertDialogTitle>
        <AlertDialogDescription>
          These machines are in different networks:
          <template v-for="(n, i) in networks" :key="n.id"
            >{{ i ? (i === networks.length - 1 ? ' and ' : ', ') : '' }}<strong>{{ n.name }}</strong></template
          >.
          Linking them joins those into one network, {{ networks[0]?.name }}, so every machine in them can reach every
          other.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <Button @click="submit(true)"><Merge /> Merge networks</Button>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
