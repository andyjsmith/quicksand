<script setup lang="ts">
import type { ActionProgress } from '@/api'
import { Progress } from '@/components/ui/progress'
import { Spinner } from '@/components/ui/spinner'
import { formatBytes } from '@/lib/format'

// A running action's step, with a bar when its size is known (a snapshot
// saving memory).
defineProps<{ progress: ActionProgress }>()
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <div class="flex items-center justify-between gap-2 text-xs">
      <span class="flex items-center gap-1.5"><Spinner class="size-3" /> {{ progress.step }}…</span>
      <span v-if="progress.total" class="text-muted-foreground tabular-nums">
        {{ formatBytes(progress.done ?? 0) }} of {{ formatBytes(progress.total) }}
      </span>
    </div>
    <Progress v-if="progress.total" :model-value="((progress.done ?? 0) / progress.total) * 100" class="h-1.5" />
  </div>
</template>
