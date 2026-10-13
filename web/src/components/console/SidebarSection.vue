<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{
  title: string
  icon: Component
}>()
</script>

<template>
  <section class="flex flex-col gap-3 px-4 py-4">
    <h3 class="text-muted-foreground flex items-center gap-2 text-[0.7rem] font-medium tracking-wider uppercase">
      <component :is="icon" class="size-3.5" />
      {{ title }}
    </h3>
    <slot />
  </section>
</template>
