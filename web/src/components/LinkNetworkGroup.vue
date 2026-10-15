<script setup lang="ts">
import { Check, Network, Pencil, X } from '@lucide/vue'
import { nextTick, ref, useTemplateRef } from 'vue'
import { api, errorMessage, type VM } from '@/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { linkColor } from '@/composables/useLinks'

// A link network on the dashboard: an outlined box around its machines,
// named in the corner (click the name to rename it).

const props = defineProps<{ network: number; name: string; vms: VM[] }>()

const color = linkColor(props.network)
const editing = ref(false)
const draft = ref('')
const error = ref<string>()
const input = useTemplateRef<{ $el: HTMLInputElement }>('input')

async function edit() {
  draft.value = props.name
  error.value = undefined
  editing.value = true
  await nextTick()
  input.value?.$el.select()
}

async function save() {
  const name = draft.value.trim()
  if (!name || name === props.name) {
    editing.value = false
    return
  }
  try {
    // The new name arrives with the machines' events.
    await api.renameLink(props.network, name)
    editing.value = false
  } catch (e) {
    error.value = errorMessage(e)
  }
}
</script>

<template>
  <section class="rounded-2xl border-2 border-dashed p-3 pt-2" :class="color.box">
    <header class="mb-2 flex min-h-8 flex-wrap items-center gap-x-3 gap-y-1 px-1">
      <Network class="size-4 shrink-0" :class="color.text" />
      <form v-if="editing" class="flex items-center gap-1" @submit.prevent="save">
        <Input
          ref="input"
          v-model="draft"
          maxlength="40"
          class="h-7 w-48 text-sm"
          aria-label="Network name"
          @keydown.esc="editing = false"
        />
        <Button type="submit" size="icon-sm" variant="ghost" title="Save"><Check /></Button>
        <Button type="button" size="icon-sm" variant="ghost" title="Cancel" @click="editing = false"><X /></Button>
      </form>
      <button
        v-else
        type="button"
        class="group/name flex items-center gap-1.5 rounded-md px-1 py-0.5 text-sm font-semibold hover:bg-black/5 dark:hover:bg-white/5"
        title="Rename"
        @click="edit"
      >
        <span :class="color.text">{{ name }}</span>
        <Pencil class="text-muted-foreground size-3 opacity-0 transition group-hover/name:opacity-100" />
      </button>
      <span class="text-muted-foreground text-xs">
        {{ vms.length }} linked machine{{ vms.length === 1 ? '' : 's' }}
      </span>
      <span v-if="error" class="text-destructive text-xs">{{ error }}</span>
    </header>
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <slot />
    </div>
  </section>
</template>
