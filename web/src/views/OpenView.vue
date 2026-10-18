<script setup lang="ts">
import { useTitle } from '@vueuse/core'
import { CircleAlert, ExternalLink, Monitor, Plus, Rocket } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api, errorMessage, type Network, type Template, type VM } from '@/api'
import AppHeader from '@/components/AppHeader.vue'
import TemplateIcon from '@/components/TemplateIcon.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

// /open?url=URL[&template=ID][&network=ID] opens the URL, e.g. from the
// "Open in Quicksand" browser extension: in a new VM of a template that opens
// links, on a network (both preselected from the query if given, and chosen
// here), or in one of the user's running VMs that opens links. It asks first: any site could send someone here, and
// shouldn't deploy VMs or open pages in their name.

useTitle('Open link · Quicksand')

const route = useRoute()
const router = useRouter()
const askedTemplate = typeof route.query.template === 'string' ? route.query.template : ''
const url = computed(() => (typeof route.query.url === 'string' ? route.query.url : ''))

// templates are those that open links; templateId is the one chosen.
const templates = ref<Template[]>([])
const templateId = ref('')
const template = computed(() => templates.value.find((t) => t.id === templateId.value))
const networks = ref<Network[]>([])
const networkId = ref('')
// mode is whether to open the link in a new VM or an existing one; vms are
// the user's running VMs that can open it.
const mode = ref<'new' | 'existing'>('new')
const vms = ref<VM[]>([])
const vmId = ref('')
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
  if (loading.value) return undefined
  if (mode.value === 'existing') return vmId.value ? undefined : 'Choose a VM.'
  if (!templates.value.length) return 'None of the templates can open links.'
  if (!template.value) return 'Choose a template.'
  return template.value.quotaBlocked || template.value.error
})

onMounted(async () => {
  try {
    const [t, n, v] = await Promise.all([api.templates(), api.networks(), api.vms()])
    templates.value = t.filter((t) => t.opensURLs)
    // The template asked for, else the one last used here, else the first.
    const last = loadLastTemplate()
    templateId.value =
      [askedTemplate, last].find((id) => id && templates.value.some((t) => t.id === id)) ??
      templates.value[0]?.id ??
      ''
    networks.value = n
    // Running VMs that open links, those of the chosen template first
    // (newest first, as listed).
    vms.value = v
      .filter((vm) => vm.state === 'ready' && vm.power === 'running' && vm.launch?.opensURLs)
      .sort((a, b) => Number(b.templateId === templateId.value) - Number(a.templateId === templateId.value))
    vmId.value = vms.value[0]?.id ?? ''
    // The network asked for, if there is one, else the default.
    const asked = typeof route.query.network === 'string' ? route.query.network : ''
    networkId.value = (n.find((x) => x.id === asked) ?? n.find((x) => x.default) ?? n[0])?.id ?? ''
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
})

// The template last opened a link in is remembered in this browser, to
// choose it again when the link doesn't name one.
const lastTemplateKey = 'quicksand.open.template'

function loadLastTemplate() {
  try {
    return localStorage.getItem(lastTemplateKey) ?? ''
  } catch {
    return ''
  }
}

function saveLastTemplate(id: string) {
  try {
    localStorage.setItem(lastTemplateKey, id)
  } catch {
    // Not remembered, which is fine.
  }
}

async function open() {
  if (problem.value) return
  busy.value = true
  error.value = undefined
  try {
    if (mode.value === 'existing') {
      await api.openURL(vmId.value, url.value)
      router.replace({ name: 'console', params: { id: vmId.value } })
      return
    }
    if (!template.value) return
    saveLastTemplate(template.value.id)
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
            <ExternalLink class="size-4" /> Open link
          </CardTitle>
          <CardDescription>
            {{
              mode === 'new'
                ? 'This deploys a new VM and opens the link in it.'
                : 'This opens the link in a new tab of a VM you already have.'
            }}
          </CardDescription>
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
          <ToggleGroup
            v-if="!loading"
            :model-value="mode"
            type="single"
            variant="outline"
            class="w-full"
            @update:model-value="(v) => v && (mode = v as 'new' | 'existing')"
          >
            <ToggleGroupItem value="new" class="flex-1"><Plus /> New VM</ToggleGroupItem>
            <ToggleGroupItem
              value="existing"
              class="flex-1"
              :disabled="!vms.length"
              :title="vms.length ? undefined : 'None of your running VMs can open links'"
            >
              <Monitor /> Existing VM
            </ToggleGroupItem>
          </ToggleGroup>
          <div v-if="!loading && mode === 'existing'" class="flex flex-col gap-1.5">
            <Label for="open-vm">VM</Label>
            <Select v-model="vmId">
              <SelectTrigger id="open-vm" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="vm in vms" :key="vm.id" :value="vm.id">
                  {{ vm.name }} <span class="text-muted-foreground">· {{ vm.templateName }}</span>
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-else-if="!loading && templates.length" class="flex flex-col gap-1.5">
            <Label for="open-template">Template</Label>
            <Select v-model="templateId">
              <SelectTrigger id="open-template" class="w-full">
                <SelectValue placeholder="Choose a template">
                  <span v-if="template" class="flex items-center gap-2">
                    <TemplateIcon
                      :icon="template.icon"
                      :os="template.os"
                      :class="template.icon ? 'size-4' : 'size-3.5'"
                    />
                    {{ template.name }}
                  </span>
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="t in templates" :key="t.id" :value="t.id">
                  <TemplateIcon :icon="t.icon" :os="t.os" :class="t.icon ? 'size-4' : 'size-3.5'" />
                  {{ t.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-if="template && mode === 'new' && networks.length > 1" class="flex flex-col gap-1.5">
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
            <Rocket v-else-if="mode === 'new'" />
            <ExternalLink v-else />
            Open
          </Button>
        </CardFooter>
      </Card>
    </main>
  </div>
</template>
