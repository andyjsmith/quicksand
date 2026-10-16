<script setup lang="ts">
import { useLocalStorage } from '@vueuse/core'
import { ChevronRight } from '@lucide/vue'
import type { Component } from 'vue'

// A collapsible section of the console sidebar, collapsed until opened;
// whether it's open is remembered.

const props = defineProps<{
  title: string
  icon: Component
}>()

const open = useLocalStorage(`quicksand.consoleSection.${props.title}`, false)
</script>

<template>
  <section>
    <button
      type="button"
      class="hover:bg-muted/60 flex w-full items-center gap-2 px-4 py-3 text-left text-xs font-medium tracking-wider uppercase"
      :class="open ? 'text-foreground' : 'text-muted-foreground'"
      :aria-expanded="open"
      @click="open = !open"
    >
      <component :is="icon" class="size-3.5" />
      <span class="flex-1">{{ title }}</span>
      <ChevronRight class="size-3.5 transition-transform" :class="{ 'rotate-90': open }" />
    </button>
    <div v-if="open" class="flex flex-col gap-3 px-4 pt-1 pb-4">
      <slot />
    </div>
  </section>
</template>
