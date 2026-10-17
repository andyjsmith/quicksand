<script setup lang="ts">
import { useTitle } from '@vueuse/core'
import { CircleAlert, ExternalLink, Rocket } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, errorMessage, type Network, type Template } from '@/api'
import AppHeader from '@/components/AppHeader.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { formatMemory, osIcon } from '@/lib/format'

// /open?template=ID&url=URL[&network=ID] deploys a VM of the template to
// open the URL, e.g. from the "Open in Quicksand" browser extension, on the
// network given (or chosen here). It asks first: any site could send
// someone here, and shouldn't deploy VMs in their name.

useTitle('Open link · Quicksand')

const route = useRoute()
const router = useRouter()
const templateId = computed(() => (typeof route.query.template === 'string' ? route.query.template : ''))
const url = computed(() => (typeof route.query.url === 'string' ? route.query.url : ''))

const template = ref<Template>()
const networks = ref<Network[]>([])
const networkId = ref('')
const loading = ref(true)
const busy = ref(false)
const error = ref<string>()

// Why the link can't be opened, before trying.
const problem = computed(() => {
  if (!url.value) return 'No link to open.'
  try {
    const u = new URL(url.value)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'Only http and https links can be opened.'
  } catch {
    return `"${url.value}" isn't a link.`
  }
  if (!templateId.value) return 'No template given.'
  if (loading.value) return undefined
  if (!template.value) return `There's no template "${templateId.value}".`
  if (!template.value.opensURLs) return `${template.value.name} can't open links.`
  return template.value.quotaBlocked || template.value.error
})

onMounted(async () => {
  try {
    const [t, n] = await Promise.all([api.templates(), api.networks()])
    template.value = t.find((t) => t.id === templateId.value)
    networks.value = n
    // The network asked for, if there is one, else the default.
    const asked = typeof route.query.network === 'string' ? route.query.network : ''
    networkId.value = (n.find((x) => x.id === asked) ?? n.find((x) => x.default) ?? n[0])?.id ?? ''
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
})

async function open() {
  if (!template.value || problem.value) return
  busy.value = true
  error.value = undefined
  try {
    const vm = await api.deploy({
      templateId: template.value.id,
      name: '',
      cores: template.value.cores,
      memoryMiB: template.value.memoryMiB,
      networkId: networkId.value,
      url: url.value,
    })
    // The console shows the deployment and connects once it's running.
    router.replace({ name: 'console', params: { id: vm.id } })
  } catch (e) {
    error.value = errorMessage(e)
    busy.value = false
  }
}
</script>

<template>
  <div class="bg-background text-foreground min-h-dvh">
    <AppHeader />
    <main class="mx-auto flex max-w-xl flex-col gap-6 px-4 py-10">
      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2">
            <ExternalLink class="size-4" /> Open link{{ template ? ` in ${template.name}` : '' }}
          </CardTitle>
          <CardDescription>This deploys a new VM and opens the link in it.</CardDescription>
        </CardHeader>
        <CardContent class="flex flex-col gap-4">
          <p
            v-if="url"
            class="bg-muted overflow-hidden rounded-md px-3 py-2 font-mono text-xs break-all"
            :title="url"
          >
            {{ url.length > 300 ? url.slice(0, 300) + '…' : url }}
          </p>
          <div v-if="loading" class="text-muted-foreground flex items-center gap-2 text-sm">
            <Spinner class="size-4" /> Loading…
          </div>
          <div v-else-if="template" class="flex items-center gap-3 text-sm">
            <div class="bg-muted flex size-9 shrink-0 items-center justify-center rounded-md">
              <component :is="osIcon(template.os)" class="size-4" />
            </div>
            <div class="min-w-0">
              <p class="truncate font-medium">{{ template.name }}</p>
              <p class="text-muted-foreground text-xs">
                {{ template.cores }} CPUs · {{ formatMemory(template.memoryMiB) }}
              </p>
            </div>
          </div>
          <div v-if="template && networks.length > 1" class="flex flex-col gap-1.5">
            <Label for="open-network">Network</Label>
            <Select v-model="networkId">
              <SelectTrigger id="open-network" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="n in networks" :key="n.id" :value="n.id">{{ n.label }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <Alert v-if="error || problem" variant="destructive">
            <CircleAlert />
            <AlertDescription>{{ error || problem }}</AlertDescription>
          </Alert>
        </CardContent>
        <CardFooter class="flex justify-end gap-2">
          <Button variant="outline" as-child><RouterLink to="/">Cancel</RouterLink></Button>
          <Button :disabled="loading || busy || !!problem" autofocus @click="open">
            <Spinner v-if="busy" />
            <Rocket v-else />
            Open
          </Button>
        </CardFooter>
      </Card>
    </main>
  </div>
</template>
