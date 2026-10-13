<script setup lang="ts">
import { Rocket } from '@lucide/vue'
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
        <h3 class="truncate text-sm font-semibold">{{ template.name }}</h3>
        <p class="text-muted-foreground text-xs">
          <template v-if="template.error">{{ template.error }}</template>
          <template v-else>{{ template.cores }} CPUs · {{ formatMemory(template.memoryMiB) }}</template>
        </p>
      </div>
    </div>
    <p v-if="template.description" class="text-muted-foreground line-clamp-2 text-xs">{{ template.description }}</p>
    <div class="flex-1" />
    <Button :disabled="!!template.error" @click="$emit('deploy')"><Rocket /> Deploy</Button>
  </Card>
</template>
