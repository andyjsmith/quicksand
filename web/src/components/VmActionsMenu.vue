<script setup lang="ts">
import {
  EllipsisVertical,
  Info,
  Pause,
  Play,
  Power,
  RotateCcw,
  RotateCw,
  Square,
  Trash2,
} from '@lucide/vue'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

const props = defineProps<{ vm: VM; showDetails?: boolean }>()
const emit = defineEmits<{ error: [message: string | undefined]; destroyed: [] }>()

const router = useRouter()
const ready = computed(() => props.vm.state === 'ready')
const busy = computed(() => props.vm.state === 'deploying' || props.vm.state === 'destroying' || !!props.vm.action)
const confirmingDestroy = ref(false)

async function power(action: PowerAction) {
  emit('error', undefined)
  try {
    await api.power(props.vm.id, action)
  } catch (e) {
    emit('error', errorMessage(e))
  }
}

async function destroy() {
  emit('error', undefined)
  try {
    await api.destroy(props.vm.id)
    emit('destroyed')
  } catch (e) {
    emit('error', errorMessage(e))
  }
}

defineExpose({ power })
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon-sm" title="Actions" :disabled="vm.state === 'destroying'">
        <EllipsisVertical />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-44">
      <template v-if="showDetails">
        <DropdownMenuItem @select="router.push({ name: 'vm', params: { id: vm.id } })">
          <Info /> Details
        </DropdownMenuItem>
        <DropdownMenuSeparator />
      </template>
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
</template>
