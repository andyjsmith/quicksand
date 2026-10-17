<script setup lang="ts">
import { CircleAlert, Rocket } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { api, errorMessage, type Network, type Template, type VM } from '@/api'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  NumberField,
  NumberFieldContent,
  NumberFieldDecrement,
  NumberFieldIncrement,
  NumberFieldInput,
} from '@/components/ui/number-field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { formatMemory } from '@/lib/format'

const props = defineProps<{ template: Template | null; networks: Network[] }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ deployed: [vm: VM] }>()

const name = ref('')
const cores = ref(1)
const memory = ref('1024')
const networkId = ref('')
const withAgent = ref(true)
const url = ref('')
const error = ref<string>()
const busy = ref(false)

// Common sizes up to the template's limit, plus its default.
const memoryOptions = computed(() => {
  const t = props.template
  if (!t) return []
  const sizes = new Set([512, 1024, 2048, 4096, 6144, 8192, 12288, 16384, 24576, 32768, 49152, 65536])
  sizes.add(t.memoryMiB)
  return [...sizes].filter((m) => m <= t.maxMemoryMiB).sort((a, b) => a - b)
})

watch(open, (o) => {
  if (!o || !props.template) return
  name.value = ''
  cores.value = props.template.cores
  memory.value = String(props.template.memoryMiB)
  withAgent.value = true
  url.value = ''
  networkId.value = (props.networks.find((n) => n.default) ?? props.networks[0])?.id ?? ''
  error.value = undefined
})

async function submit() {
  if (!props.template) return
  busy.value = true
  error.value = undefined
  try {
    const vm = await api.deploy({
      templateId: props.template.id,
      name: name.value.trim(),
      cores: cores.value,
      memoryMiB: Number(memory.value),
      networkId: networkId.value,
      agent: props.template.agent ? withAgent.value || !!url.value.trim() : undefined,
      url: props.template.opensURLs && url.value.trim() ? url.value.trim() : undefined,
    })
    emit('deployed', vm)
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
    <DialogContent v-if="template" class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>Deploy {{ template.name }}</DialogTitle>
        <DialogDescription>A new VM is cloned from this template and powered on.</DialogDescription>
      </DialogHeader>
      <form id="deploy" class="flex flex-col gap-4" @submit.prevent="submit">
        <Alert v-if="error" variant="destructive">
          <CircleAlert />
          <AlertDescription>{{ error }}</AlertDescription>
        </Alert>
        <div class="flex flex-col gap-1.5">
          <Label for="deploy-name">Name</Label>
          <Input id="deploy-name" v-model="name" :placeholder="`${template.id}-…`" maxlength="63" />
          <p class="text-muted-foreground text-xs">Optional. Letters, digits and dashes.</p>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div class="flex flex-col gap-1.5">
            <Label for="deploy-cores">CPUs</Label>
            <NumberField id="deploy-cores" v-model="cores" :min="1" :max="template.maxCores">
              <NumberFieldContent>
                <NumberFieldDecrement />
                <NumberFieldInput />
                <NumberFieldIncrement />
              </NumberFieldContent>
            </NumberField>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="deploy-memory">Memory</Label>
            <Select v-model="memory">
              <SelectTrigger id="deploy-memory" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="m in memoryOptions" :key="m" :value="String(m)">{{ formatMemory(m) }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="deploy-network">Network</Label>
          <Select v-model="networkId">
            <SelectTrigger id="deploy-network" class="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="n in networks" :key="n.id" :value="n.id">{{ n.label }}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div v-if="template?.opensURLs" class="flex flex-col gap-1.5">
          <Label for="deploy-url">Open link <span class="text-muted-foreground font-normal">(optional)</span></Label>
          <Input id="deploy-url" v-model="url" type="url" placeholder="https://" />
        </div>
        <div v-if="template?.agent" class="flex items-start gap-2.5">
          <Checkbox
            id="deploy-agent"
            :model-value="withAgent || !!url.trim()"
            :disabled="!!url.trim()"
            class="mt-0.5"
            @update:model-value="withAgent = !!$event"
          />
          <div class="flex flex-col gap-1">
            <Label for="deploy-agent">Enable agent</Label>
            <p class="text-muted-foreground text-xs">
              Connects the VM to Quicksand's agent network for processes, files and commands. Turn it off to keep
              the VM completely isolated from Quicksand.{{ template?.launch ? ' The agent also launches the app.' : '' }}
            </p>
          </div>
        </div>
      </form>
      <DialogFooter>
        <Button variant="outline" @click="open = false">Cancel</Button>
        <Button type="submit" form="deploy" :disabled="busy">
          <Spinner v-if="busy" />
          <Rocket v-else />
          Deploy
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
