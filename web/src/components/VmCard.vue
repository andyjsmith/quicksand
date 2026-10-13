<script setup lang="ts">
import {
  CircleAlert,
  EllipsisVertical,
  Monitor,
  Pause,
  Play,
  Power,
  RotateCcw,
  RotateCw,
  Square,
  Trash2,
} from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api, errorMessage, type PowerAction, type VM } from '@/api'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Progress } from '@/components/ui/progress'
import { Spinner } from '@/components/ui/spinner'
import { formatMemory, formatUptime, osIcon, vmStatus } from '@/lib/format'
import { loadPreview, onPreviewChange } from '@/lib/previews'

const props = defineProps<{ vm: VM }>()

const status = computed(() => vmStatus(props.vm))
const icon = computed(() => osIcon(props.vm.os))
const ready = computed(() => props.vm.state === 'ready')
const busy = computed(() => props.vm.state === 'deploying' || props.vm.state === 'destroying' || !!props.vm.action)
const canConnect = computed(() => ready.value && props.vm.power === 'running' && props.vm.consoles.length > 0)

const preview = ref(loadPreview(props.vm.id))
onBeforeUnmount(onPreviewChange((id) => id === props.vm.id && (preview.value = loadPreview(id))))

const error = ref<string>()
const confirmingDestroy = ref(false)

async function power(action: PowerAction) {
  error.value = undefined
  try {
    await api.power(props.vm.id, action)
  } catch (e) {
    error.value = errorMessage(e)
  }
}

async function destroy() {
  error.value = undefined
  try {
    await api.destroy(props.vm.id)
  } catch (e) {
    error.value = errorMessage(e)
  }
}

// What the primary button does when the VM isn't running.
const primaryPower = computed<{ action: PowerAction; label: string } | null>(() => {
  if (!ready.value || busy.value) return null
  if (props.vm.power === 'stopped') return { action: 'start', label: 'Start' }
  if (props.vm.power === 'paused' || props.vm.power === 'suspended') return { action: 'resume', label: 'Resume' }
  return null
})
</script>

<template>
  <Card class="flex flex-col gap-0 overflow-hidden p-0">
    <!-- Preview -->
    <component
      :is="canConnect ? RouterLink : 'div'"
      :to="canConnect ? { name: 'console', params: { id: vm.id } } : undefined"
      class="group bg-muted relative block aspect-video overflow-hidden"
      :title="canConnect ? 'Open console' : undefined"
    >
      <img
        v-if="preview"
        :src="preview"
        alt=""
        class="size-full object-cover transition"
        :class="{ 'opacity-40 grayscale': !canConnect }"
      />
      <div v-else class="text-muted-foreground flex size-full items-center justify-center">
        <component :is="icon" class="size-10 opacity-40" />
      </div>
      <div
        v-if="canConnect"
        class="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition group-hover:opacity-100"
      >
        <span class="flex items-center gap-1.5 rounded-md bg-black/60 px-3 py-1.5 text-xs font-medium text-white">
          <Monitor class="size-3.5" /> Open console
        </span>
      </div>
      <!-- Deployment / teardown progress -->
      <div
        v-if="vm.state === 'deploying' || vm.state === 'destroying'"
        class="bg-background/85 absolute inset-x-0 bottom-0 flex flex-col gap-1.5 px-3 py-2.5 backdrop-blur"
      >
        <div class="flex items-center justify-between text-xs">
          <span class="flex items-center gap-1.5"><Spinner class="size-3" /> {{ vm.step || 'Working' }}…</span>
          <span class="text-muted-foreground tabular-nums">{{ Math.round(vm.progress * 100) }}%</span>
        </div>
        <Progress :model-value="vm.progress * 100" class="h-1.5" />
      </div>
    </component>

    <!-- Details -->
    <div class="flex flex-col gap-2 p-3">
      <div class="flex items-start gap-2">
        <div class="min-w-0 flex-1">
          <h3 class="truncate text-sm font-semibold">{{ vm.name }}</h3>
          <p class="text-muted-foreground truncate text-xs">
            {{ vm.templateName }} · {{ vm.cores }} CPUs · {{ formatMemory(vm.memoryMiB) }} · {{ vm.networkLabel }}
          </p>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="icon-sm" title="Actions" :disabled="vm.state === 'destroying'">
              <EllipsisVertical />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="w-44">
            <template v-if="ready">
              <DropdownMenuItem :disabled="busy || vm.power !== 'stopped'" @select="power('start')">
                <Play /> Start
              </DropdownMenuItem>
              <DropdownMenuItem :disabled="busy || vm.power !== 'running'" @select="power('shutdown')">
                <Power /> Shut down
              </DropdownMenuItem>
              <DropdownMenuItem :disabled="busy || vm.power !== 'running'" @select="power('reboot')">
                <RotateCw /> Reboot
              </DropdownMenuItem>
              <DropdownMenuItem
                v-if="vm.power === 'paused' || vm.power === 'suspended'"
                :disabled="busy"
                @select="power('resume')"
              >
                <Play /> Resume
              </DropdownMenuItem>
              <DropdownMenuItem v-else :disabled="busy || vm.power !== 'running'" @select="power('suspend')">
                <Pause /> Suspend
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem :disabled="busy || vm.power === 'stopped'" @select="power('stop')">
                <Square /> Force stop
              </DropdownMenuItem>
              <DropdownMenuItem :disabled="busy || vm.power !== 'running'" @select="power('reset')">
                <RotateCcw /> Force reset
              </DropdownMenuItem>
              <DropdownMenuSeparator />
            </template>
            <DropdownMenuItem variant="destructive" @select="confirmingDestroy = true">
              <Trash2 /> Destroy
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div class="flex items-center justify-between gap-2">
        <span class="flex items-center gap-1.5 text-xs">
          <span class="inline-block size-2 rounded-full" :class="status.dot" />
          {{ status.label }}
          <span v-if="ready && vm.power === 'running' && vm.uptime" class="text-muted-foreground">
            · up {{ formatUptime(vm.uptime) }}
          </span>
        </span>
        <Button v-if="canConnect" size="sm" as-child>
          <RouterLink :to="{ name: 'console', params: { id: vm.id } }"><Monitor /> Connect</RouterLink>
        </Button>
        <Button v-else-if="primaryPower" size="sm" variant="outline" @click="power(primaryPower.action)">
          <Play /> {{ primaryPower.label }}
        </Button>
      </div>

      <p v-if="vm.state === 'failed' || vm.actionError || error" class="text-destructive flex gap-1.5 text-xs">
        <CircleAlert class="mt-0.5 size-3 shrink-0" />
        <span class="break-words">{{ error || vm.actionError || vm.error }}</span>
      </p>
    </div>

    <AlertDialog v-model:open="confirmingDestroy">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Destroy {{ vm.name }}?</AlertDialogTitle>
          <AlertDialogDescription>
            The VM is powered off and deleted along with its disks. This can't be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" @click="destroy">Destroy</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </Card>
</template>
