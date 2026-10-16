<script setup lang="ts">
import { ArrowDownLeft, ArrowUpRight, Ban, Check } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { describeRule, type FirewallRule, newRule, privateRanges, validateRule } from '@/lib/firewall'

// Adds or edits one firewall rule.

const props = defineProps<{ rule: FirewallRule | null; networkLabel: string }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ save: [rule: FirewallRule] }>()

const draft = ref<FirewallRule>(newRule())
// "Remote" is a choice plus, for "specific", the addresses typed.
const remoteKind = ref<'any' | 'private' | 'specific'>('any')
const addresses = ref('')
const touched = ref(false)

watch(open, (o) => {
  if (!o) return
  draft.value = { ...(props.rule ?? newRule()) }
  const r = draft.value.remote
  remoteKind.value = r === 'any' || r === 'private' ? r : 'specific'
  addresses.value = remoteKind.value === 'specific' ? r : ''
  touched.value = false
})

const rule = computed<FirewallRule>(() => ({
  ...draft.value,
  remote: remoteKind.value === 'specific' ? addresses.value.trim() : remoteKind.value,
  ports: draft.value.protocol === 'tcp' || draft.value.protocol === 'udp' ? draft.value.ports.trim() : '',
}))
const errors = computed(() => validateRule(rule.value))
const valid = computed(() => !errors.value.remote && !errors.value.ports)
const hasPorts = computed(() => draft.value.protocol === 'tcp' || draft.value.protocol === 'udp')

function save() {
  touched.value = true
  if (!valid.value) return
  emit('save', rule.value)
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ props.rule ? 'Edit rule' : 'Add rule' }} · {{ networkLabel }}</DialogTitle>
        <DialogDescription>Rules are checked top to bottom; the first that matches decides.</DialogDescription>
      </DialogHeader>

      <form class="flex flex-col gap-4" @submit.prevent="save">
        <div class="grid grid-cols-2 gap-4">
          <div class="flex flex-col gap-1.5">
            <Label>Action</Label>
            <ToggleGroup
              :model-value="draft.action"
              type="single"
              variant="outline"
              class="w-full"
              @update:model-value="(v) => v && (draft.action = v as FirewallRule['action'])"
            >
              <ToggleGroupItem value="allow" class="flex-1 data-[state=on]:text-emerald-600"><Check /> Allow</ToggleGroupItem>
              <ToggleGroupItem value="block" class="data-[state=on]:text-destructive flex-1"><Ban /> Block</ToggleGroupItem>
            </ToggleGroup>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label>Direction</Label>
            <ToggleGroup
              :model-value="draft.direction"
              type="single"
              variant="outline"
              class="w-full"
              @update:model-value="(v) => v && (draft.direction = v as FirewallRule['direction'])"
            >
              <ToggleGroupItem value="out" class="flex-1" title="Traffic the VM sends"><ArrowUpRight /> Outbound</ToggleGroupItem>
              <ToggleGroupItem value="in" class="flex-1" title="Traffic sent to the VM"><ArrowDownLeft /> Inbound</ToggleGroupItem>
            </ToggleGroup>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div class="flex flex-col gap-1.5">
            <Label for="fw-protocol">Protocol</Label>
            <Select v-model="draft.protocol">
              <SelectTrigger id="fw-protocol" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="any">Any</SelectItem>
                <SelectItem value="tcp">TCP</SelectItem>
                <SelectItem value="udp">UDP</SelectItem>
                <SelectItem value="icmp">ICMP (ping)</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="fw-ports">{{ draft.direction === 'out' ? 'Destination ports' : 'Ports on the VM' }}</Label>
            <Input
              id="fw-ports"
              v-model="draft.ports"
              :disabled="!hasPorts"
              :placeholder="hasPorts ? 'All ports' : 'Not applicable'"
              class="font-mono"
              :aria-invalid="touched && !!errors.ports"
            />
          </div>
        </div>
        <p v-if="touched && errors.ports" class="text-destructive -mt-2 text-xs">{{ errors.ports }}</p>

        <div class="flex flex-col gap-1.5">
          <Label for="fw-remote">{{ draft.direction === 'out' ? 'Destination' : 'Source' }}</Label>
          <Select v-model="remoteKind">
            <SelectTrigger id="fw-remote" class="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="any">Anywhere</SelectItem>
              <SelectItem value="private">Private networks (LAN)</SelectItem>
              <SelectItem value="specific">Specific addresses…</SelectItem>
            </SelectContent>
          </Select>
          <Input
            v-if="remoteKind === 'specific'"
            v-model="addresses"
            placeholder="203.0.113.5, 10.1.0.0/16"
            class="font-mono"
            :aria-invalid="touched && !!errors.remote"
          />
          <p v-if="touched && errors.remote" class="text-destructive text-xs">{{ errors.remote }}</p>
          <p v-else-if="remoteKind === 'private'" class="text-muted-foreground text-xs">
            {{ privateRanges.join(', ') }}
          </p>
        </div>

        <div class="flex flex-col gap-1.5">
          <Label for="fw-comment">Note <span class="text-muted-foreground font-normal">(optional)</span></Label>
          <Input id="fw-comment" v-model="draft.comment" maxlength="60" placeholder="What it's for" />
        </div>

        <p class="bg-muted/60 rounded-md px-3 py-2 text-sm">{{ describeRule(rule) }}.</p>

        <DialogFooter>
          <Button type="button" variant="outline" @click="open = false">Cancel</Button>
          <Button type="submit">{{ props.rule ? 'Save rule' : 'Add rule' }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
