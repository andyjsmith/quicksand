<script setup lang="ts">
import { useTitle } from '@vueuse/core'
import { CircleAlert, Layers, Monitor } from '@lucide/vue'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { api, errorMessage, type Network, type Template } from '@/api'
import AppHeader from '@/components/AppHeader.vue'
import DeployDialog from '@/components/DeployDialog.vue'
import TemplateCard from '@/components/TemplateCard.vue'
import VmCard from '@/components/VmCard.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { useMachines } from '@/composables/useMachines'

useTitle('Quicksand')

const machines = useMachines()
onBeforeUnmount(machines.stop)

const templates = ref<Template[] | null>(null)
const networks = ref<Network[]>([])
const loadError = ref<string>()

onMounted(async () => {
  try {
    const [t, n] = await Promise.all([api.templates(), api.networks()])
    templates.value = t
    networks.value = n
  } catch (e) {
    loadError.value = errorMessage(e)
  }
})

const deploying = ref<Template | null>(null)
const deployOpen = ref(false)
function startDeploy(t: Template) {
  deploying.value = t
  deployOpen.value = true
}
</script>

<template>
  <div class="bg-background text-foreground min-h-dvh">
    <AppHeader />
    <main class="mx-auto flex max-w-6xl flex-col gap-8 px-4 py-6">
      <Alert v-if="loadError" variant="destructive">
        <CircleAlert />
        <AlertTitle>Couldn't load templates</AlertTitle>
        <AlertDescription>{{ loadError }}</AlertDescription>
      </Alert>

      <section class="flex flex-col gap-3">
        <h2 class="flex items-center gap-2 text-sm font-semibold"><Monitor class="size-4" /> Your machines</h2>
        <div v-if="!machines.loaded.value" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton v-for="i in 3" :key="i" class="aspect-[4/3] rounded-xl" />
        </div>
        <div
          v-else-if="!machines.machines.value.length"
          class="text-muted-foreground rounded-xl border border-dashed px-4 py-10 text-center text-sm"
        >
          No machines yet. Deploy one from a template below.
        </div>
        <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <VmCard v-for="vm in machines.machines.value" :key="vm.id" :vm="vm" />
        </div>
      </section>

      <section class="flex flex-col gap-3">
        <h2 class="flex items-center gap-2 text-sm font-semibold"><Layers class="size-4" /> Templates</h2>
        <div v-if="templates === null && !loadError" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Skeleton v-for="i in 2" :key="i" class="h-36 rounded-xl" />
        </div>
        <p v-else-if="templates && !templates.length" class="text-muted-foreground text-sm">
          No templates are configured. Add them to <code>templates</code> in config.json.
        </p>
        <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <TemplateCard v-for="t in templates" :key="t.id" :template="t" @deploy="startDeploy(t)" />
        </div>
      </section>
    </main>

    <DeployDialog
      v-model:open="deployOpen"
      :template="deploying"
      :networks="networks"
      @deployed="machines.upsert"
    />
  </div>
</template>
