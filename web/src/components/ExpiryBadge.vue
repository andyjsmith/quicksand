<script setup lang="ts">
import { Hourglass, TimerReset } from '@lucide/vue'
import { computed, ref } from 'vue'
import { api, errorMessage, type VM } from '@/api'
import { Button } from '@/components/ui/button'
import { useAuth } from '@/composables/useAuth'
import { useMinuteClock } from '@/composables/useNow'
import { expiryState, formatRemaining } from '@/lib/format'

const props = defineProps<{
  vm: VM
  /** Show an Extend button (when an extension is allowed). */
  extendable?: boolean
}>()
const emit = defineEmits<{ error: [message: string | undefined] }>()

const now = useMinuteClock()
const auth = useAuth()
const state = computed(() => expiryState(props.vm, now.value.getTime()))
const canExtend = computed(() => !!state.value && (state.value.canExtend || !!auth.user.value?.isAdmin))
const extendLabel = computed(() =>
  props.vm.extendSeconds ? `Extend ${formatRemaining(props.vm.extendSeconds * 1000)}` : 'Extend',
)
const title = computed(() => {
  if (!props.vm.expiresAt) return ''
  const left =
    props.vm.extendsLeft === undefined
      ? 'It can be extended any number of times.'
      : props.vm.extendsLeft === 0
        ? "It can't be extended any further."
        : `It can be extended ${props.vm.extendsLeft} more time${props.vm.extendsLeft === 1 ? '' : 's'}.`
  return `Destroyed on ${new Date(props.vm.expiresAt).toLocaleString()} unless extended. ${left}`
})

const busy = ref(false)
async function extend() {
  busy.value = true
  emit('error', undefined)
  try {
    // The new expiry also arrives as a VM event.
    await api.extend(props.vm.id)
  } catch (e) {
    emit('error', errorMessage(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <span v-if="state" class="inline-flex items-center gap-1.5" :title="title">
    <span
      class="inline-flex items-center gap-1 rounded-full text-xs"
      :class="state.soon ? 'bg-destructive/10 text-destructive px-2 py-0.5 font-medium' : 'text-muted-foreground'"
    >
      <span v-if="state.soon" class="bg-destructive inline-block size-1.5 animate-pulse rounded-full" />
      <Hourglass v-else class="size-3" />
      {{ state.remaining ? `Expires in ${formatRemaining(state.remaining)}` : 'Expiring now' }}
    </span>
    <Button
      v-if="extendable && canExtend"
      size="sm"
      variant="ghost"
      class="h-6 px-1.5 text-xs"
      :disabled="busy"
      :title="vm.extendSeconds ? `Extend by ${formatRemaining(vm.extendSeconds * 1000)}` : 'Extend'"
      @click.stop.prevent="extend"
    >
      <TimerReset class="size-3" /> {{ extendLabel }}
    </Button>
  </span>
</template>
