<script setup lang="ts">
import { Moon } from '@lucide/vue'
import { computed } from 'vue'
import type { VM } from '@/api'
import { useMinuteClock } from '@/composables/useNow'
import { formatRemaining, suspendSoon } from '@/lib/format'

// Warns that an idle VM is about to be hibernated (shown only when it's soon).
const props = defineProps<{ vm: VM }>()

const now = useMinuteClock()
const remaining = computed(() => suspendSoon(props.vm, now.value.getTime()))
const title = computed(() =>
  props.vm.suspendAt
    ? `Nobody is using this VM, so it will be hibernated at ${new Date(props.vm.suspendAt).toLocaleTimeString()} ` +
      'to free resources (open a console or shell to keep it running). Starting it again resumes it where it left off.'
    : '',
)
</script>

<template>
  <span
    v-if="remaining !== null"
    class="inline-flex items-center gap-1 text-xs text-amber-600 dark:text-amber-500"
    :title="title"
  >
    <Moon class="size-3" />
    {{ remaining >= 60_000 ? `Hibernates in ${formatRemaining(remaining)}` : 'Hibernating soon' }}
  </span>
</template>
