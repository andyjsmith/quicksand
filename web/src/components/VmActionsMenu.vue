<script setup lang="ts">
import {
  Camera,
  EllipsisVertical,
  History,
  Info,
  Link2,
  Link2Off,
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
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import LinkDialog from '@/components/LinkDialog.vue'
import { useLinks } from '@/composables/useLinks'
import { formatAgo } from '@/lib/format'
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

// Link networks.
const links = useLinks()
const linking = ref(false)
const confirmingUnlink = ref(false)
async function unlink() {
  confirmingUnlink.value = false
  emit('error', undefined)
  try {
    await api.unlink(props.vm.id)
  } catch (e) {
    emit('error', errorMessage(e))
  }
}

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

// Snapshots: one per VM, replaced by a new one, kept by reverting.
// The kind outlives the dialog: its action closes it before the click
// handler runs, and the text shouldn't change while it fades out.
const snapshotOpen = ref(false)
const snapshotDialog = ref<'take' | 'revert' | 'delete'>('take')
const withMemory = ref(true)
const running = computed(() => props.vm.power === 'running')
function askSnapshot(kind: 'take' | 'revert' | 'delete') {
  withMemory.value = true
  snapshotDialog.value = kind
  snapshotOpen.value = true
}
async function snapshotAction() {
  const kind = snapshotDialog.value
  snapshotOpen.value = false
  emit('error', undefined)
  try {
    if (kind === 'take') await api.takeSnapshot(props.vm.id, withMemory.value && running.value)
    else if (kind === 'revert') await api.revertSnapshot(props.vm.id)
    else if (kind === 'delete') await api.deleteSnapshot(props.vm.id)
  } catch (e) {
    emit('error', errorMessage(e))
  }
}
const takenAgo = computed(() => (props.vm.snapshot ? formatAgo(props.vm.snapshot.takenAt) : ''))

defineExpose({ power })
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon-sm" title="Actions" :disabled="vm.state === 'destroying'">
        <EllipsisVertical />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-48">
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
        <DropdownMenuItem :disabled="busy" @select="askSnapshot('take')">
          <Camera /> {{ vm.snapshot ? 'Replace snapshot' : 'Take snapshot' }}
        </DropdownMenuItem>
        <template v-if="vm.snapshot">
          <DropdownMenuItem :disabled="busy" @select="askSnapshot('revert')">
            <History /> Revert to snapshot
          </DropdownMenuItem>
          <DropdownMenuItem :disabled="busy" @select="askSnapshot('delete')">
            <Trash2 /> Delete snapshot
          </DropdownMenuItem>
        </template>
        <DropdownMenuSeparator />
        <template v-if="links.enabled">
          <DropdownMenuItem @select="linking = true"><Link2 /> Link to…</DropdownMenuItem>
          <DropdownMenuItem v-if="vm.link" @select="confirmingUnlink = true">
            <Link2Off /> Unlink from {{ vm.link.name }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
        </template>
      </template>
      <DropdownMenuItem variant="destructive" @select="confirmingDestroy = true">
        <Trash2 /> Destroy
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>

  <LinkDialog v-if="links.enabled" v-model:open="linking" :vm="vm" @warning="emit('error', $event)" />
  <AlertDialog v-model:open="confirmingUnlink">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Unlink {{ vm.name }}?</AlertDialogTitle>
        <AlertDialogDescription>
          It leaves {{ vm.link?.name }} and can no longer reach the machines in it.
          A network left with a single machine is removed.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <Button variant="destructive" @click="unlink"><Link2Off /> Unlink</Button>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>

  <AlertDialog v-model:open="snapshotOpen">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>
          {{
            snapshotDialog === 'take'
              ? `${vm.snapshot ? 'Replace the snapshot of' : 'Snapshot'} ${vm.name}?`
              : snapshotDialog === 'revert'
                ? `Revert ${vm.name} to its snapshot?`
                : `Delete ${vm.name}'s snapshot?`
          }}
        </AlertDialogTitle>
        <AlertDialogDescription>
          <template v-if="snapshotDialog === 'take'">
            Saves the VM's disks{{ running ? ' and, if you like, its memory' : '' }} so you can return to this point
            later.
            <template v-if="vm.snapshot">The snapshot taken {{ takenAgo }} is replaced.</template>
          </template>
          <template v-else-if="snapshotDialog === 'revert'">
            Everything since the snapshot ({{ takenAgo }}) is lost.
            {{
              vm.snapshot?.memory
                ? 'It includes memory, so the VM resumes exactly as it was.'
                : "It doesn't include memory, so the VM is left stopped."
            }}
            The snapshot is kept, so you can revert to it again.
          </template>
          <template v-else>The snapshot taken {{ takenAgo }} is deleted. The VM itself isn't changed.</template>
        </AlertDialogDescription>
      </AlertDialogHeader>
      <div v-if="snapshotDialog === 'take' && running" class="flex items-start gap-2">
        <Checkbox id="snapshot-memory" v-model="withMemory" />
        <div class="grid gap-0.5">
          <Label for="snapshot-memory">Include memory</Label>
          <p class="text-muted-foreground text-xs">
            Reverting then resumes running programs as they were. Takes longer, and briefly pauses the VM.
          </p>
        </div>
      </div>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction :variant="snapshotDialog === 'take' ? 'default' : 'destructive'" @click="snapshotAction">
          {{ snapshotDialog === 'take' ? (vm.snapshot ? 'Replace snapshot' : 'Take snapshot') : snapshotDialog === 'revert' ? 'Revert' : 'Delete' }}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>

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
