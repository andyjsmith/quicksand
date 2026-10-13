<script setup lang="ts">
import { usePreferredDark } from '@vueuse/core'
import { watchEffect } from 'vue'
import { RouterView } from 'vue-router'
import { TooltipProvider } from '@/components/ui/tooltip'

// Follow the system colour scheme.
const dark = usePreferredDark()
watchEffect(() => document.documentElement.classList.toggle('dark', dark.value))
</script>

<template>
  <TooltipProvider>
    <RouterView />
  </TooltipProvider>
</template>
