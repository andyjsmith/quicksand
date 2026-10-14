<script setup lang="ts">
import { Ban, CircleCheck, CircleX, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import type { UploadQueue } from '@/composables/useUploadQueue'
import { formatBytes } from '@/lib/format'

const props = defineProps<{ queue: UploadQueue }>()
// Top-level refs, so the template unwraps them.
const { uploads, active, cancel, cancelAll, clearFinished } = props.queue
</script>

<template>
<div v-if="uploads.length" class="flex flex-col gap-1.5 rounded-md border p-3 text-xs">
    <div class="flex items-center gap-2">
      <span class="font-medium">
        {{ active ? `Uploading ${active} file${active === 1 ? '' : 's'}` : 'Uploads' }}
      </span>
      <div class="ml-auto flex gap-1">
        <Button v-if="active > 1" variant="ghost" size="sm" class="h-6" @click="cancelAll">Cancel all</Button>
        <Button v-if="uploads.length > active" variant="ghost" size="sm" class="h-6" @click="clearFinished">
          Clear finished
        </Button>
      </div>
    </div>
    <div v-for="(u, i) in uploads" :key="i" class="grid grid-cols-[minmax(0,1fr)_8rem_5rem_1.5rem] items-center gap-3">
      <span class="truncate" :title="u.name">{{ u.name }}</span>
      <span v-if="u.state === 'done'" class="flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
        <CircleCheck class="size-3.5" /> Uploaded
      </span>
      <span v-else-if="u.state === 'failed'" class="text-destructive flex items-center gap-1">
        <CircleX class="size-3.5" /> Failed
      </span>
      <span v-else-if="u.state === 'cancelled'" class="text-muted-foreground flex items-center gap-1">
        <Ban class="size-3.5" /> Cancelled
      </span>
      <Progress v-else :model-value="u.progress" class="h-1.5" />
      <span class="text-right tabular-nums" :class="u.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'" :title="u.error">
        {{
          u.state === 'failed' || u.state === 'cancelled'
            ? ''
            : u.state === 'done'
              ? formatBytes(u.size)
              : u.state === 'queued'
                ? 'Waiting'
                : `${Math.floor(u.progress)}%`
        }}
      </span>
      <Button v-if="u.state === 'queued' || u.state === 'uploading'" variant="ghost" size="icon-sm" class="size-6" title="Cancel upload" @click="cancel(u)">
        <X />
      </Button>
      <span v-else />
      <p v-if="u.error" class="text-destructive col-span-4 -mt-1">{{ u.error }}</p>
    </div>
  </div>
</template>
