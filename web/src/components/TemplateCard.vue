<script setup lang="ts">
import { ExternalLink, Rocket } from '@lucide/vue'
import { computed } from 'vue'
import type { Template } from '@/api'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { formatMemory, osIcon } from '@/lib/format'

const props = defineProps<{ template: Template }>()
defineEmits<{ deploy: [] }>()

const icon = computed(() => osIcon(props.template.os))
</script>

<template>
  <Card class="flex flex-col gap-3 p-4">
    <div class="flex items-start gap-3">
      <div class="bg-muted flex size-10 shrink-0 items-center justify-center rounded-md">
        <component :is="icon" class="size-5" />
      </div>
      <div class="min-w-0 flex-1">
        <h3 class="flex min-w-0 items-baseline gap-2 text-sm font-semibold">
          <span class="truncate">{{ template.name }}</span>
          <code
            class="text-muted-foreground shrink-0 truncate font-mono text-xs font-normal"
            title="Template ID, for qs and the Open in Quicksand browser extension"
          >{{ template.id }}</code>
        </h3>
        <p class="text-muted-foreground text-xs">
          <template v-if="template.error">{{ template.error }}</template>
          <template v-else>{{ template.cores }} CPUs · {{ formatMemory(template.memoryMiB) }}</template>
        </p>
      </div>
    </div>
    <p v-if="template.description" class="text-muted-foreground line-clamp-2 text-xs">{{ template.description }}</p>
    <p
      v-if="template.opensURLs"
      class="text-muted-foreground flex items-center gap-1 text-xs"
      title="Deployments can open a link, e.g. from the Open in Quicksand browser extension"
    >
      <ExternalLink class="size-3" /> Opens links
    </p>
    <div class="flex-1" />
    <p v-if="template.quotaBlocked" class="text-destructive text-xs">{{ template.quotaBlocked }}</p>
    <Button :disabled="!!template.error || !!template.quotaBlocked" @click="$emit('deploy')"><Rocket /> Deploy</Button>
  </Card>
</template>
