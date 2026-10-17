<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { osIcon } from '@/lib/format'

// A template's own icon (configured on the server), or its OS's if it has
// none or it fails to load.
const props = defineProps<{ icon?: string; os: string }>()

const failed = ref(false)
watch(
  () => props.icon,
  () => (failed.value = false),
)
const fallback = computed(() => osIcon(props.os))
</script>

<template>
  <img v-if="icon && !failed" :src="icon" alt="" class="object-contain" @error="failed = true" />
  <component :is="fallback" v-else />
</template>
