<script setup lang="ts">
import { useEventListener } from '@vueuse/core'
import { CircleAlert, FileUp, Upload } from '@lucide/vue'
import { computed, nextTick, ref, shallowRef, useTemplateRef } from 'vue'
import { api, errorMessage, type VM } from '@/api'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import UploadList from '@/components/UploadList.vue'
import { filesFromDrop, useUploadQueue } from '@/composables/useUploadQueue'
import { formatBytes } from '@/lib/format'

// Files dropped on a console pane upload to its guest through the agent,
// after a confirmation (a stray drag shouldn't copy anything). Everything
// renders inside the pane, so it also works in fullscreen.

// target is the console pane it serves: drags over other panes are theirs.
// (The page stops the browser from opening files dropped elsewhere.)
const props = defineProps<{ vm: VM | null; target: HTMLElement | null }>()
const emit = defineEmits<{ done: [] }>()

const canUpload = computed(() => props.vm?.agent?.status === 'connected')

// Dragging over the pane: count enters and leaves, which fire per element.
const dragging = ref(false)
let depth = 0
const hasFiles = (e: DragEvent) => !!e.dataTransfer?.types.includes('Files')
const target = () => props.target
useEventListener(target, 'dragenter', (e: DragEvent) => {
  if (!hasFiles(e)) return
  depth++
  dragging.value = true
})
useEventListener(target, 'dragleave', (e: DragEvent) => {
  if (hasFiles(e) && --depth <= 0) {
    depth = 0
    dragging.value = false
  }
})
useEventListener(target, 'dragover', (e: DragEvent) => {
  if (!hasFiles(e)) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = canUpload.value ? 'copy' : 'none'
})
useEventListener(target, 'drop', (e: DragEvent) => {
  if (!hasFiles(e)) return
  e.preventDefault()
  depth = 0
  dragging.value = false
  if (!canUpload.value || !e.dataTransfer) return
  const { files, folders } = filesFromDrop(e.dataTransfer)
  if (files.length || folders) confirm(files, folders)
})

// The confirmation.
const pending = shallowRef<File[] | null>(null)
const skippedFolders = ref(0)
const dir = ref('')
const replace = ref(false)
const existing = ref<Set<string>>(new Set())
const checking = ref(false)
const error = ref<string>()
const dirInput = useTemplateRef<{ $el: HTMLInputElement }>('dirInput')
const lastDirKey = () => `quicksand.consoleUploadDir.${props.vm?.id}`

async function confirm(files: File[], folders: number) {
  pending.value = files
  skippedFolders.value = folders
  replace.value = false
  error.value = undefined
  existing.value = new Set()
  let last = ''
  try {
    last = localStorage.getItem(lastDirKey()) ?? ''
  } catch {
    // Storage unavailable.
  }
  dir.value = last
  await check()
  await nextTick()
  dirInput.value?.$el.focus()
}

/** Resolves the destination folder and finds files that exist there. */
async function check(): Promise<string | null> {
  if (!props.vm) return null
  checking.value = true
  error.value = undefined
  try {
    // An empty folder is the template's default path.
    const list = await api.files(props.vm.id, dir.value.trim())
    if (!dir.value.trim()) dir.value = list.path
    existing.value = new Set(list.entries.filter((e) => !e.dir).map((e) => e.name))
    return list.path
  } catch (e) {
    error.value = errorMessage(e)
    existing.value = new Set()
    return null
  } finally {
    checking.value = false
  }
}

const conflicts = computed(() => (pending.value ?? []).filter((f) => existing.value.has(f.name)))
const toUpload = computed(() => (pending.value ?? []).filter((f) => replace.value || !existing.value.has(f.name)))
const totalSize = computed(() => toUpload.value.reduce((n, f) => n + f.size, 0))

const queue = useUploadQueue(() => props.vm?.id ?? '')

// Files can also be picked (the sidebar's upload button).
const picker = useTemplateRef<HTMLInputElement>('picker')
function pickFiles() {
  if (canUpload.value) picker.value?.click()
}
function onPicked() {
  const files = [...(picker.value?.files ?? [])]
  if (picker.value) picker.value.value = ''
  if (files.length) confirm(files, 0)
}
defineExpose({ pickFiles, canUpload })

async function upload() {
  const path = await check()
  if (!path || !pending.value) return
  try {
    localStorage.setItem(lastDirKey(), path)
  } catch {
    // Storage unavailable.
  }
  const files = toUpload.value
  pending.value = null
  if (files.length) queue.enqueue(files, path, () => replace.value)
  emit('done')
}

function cancel() {
  pending.value = null
  emit('done')
}
</script>

<template>
  <input ref="picker" type="file" multiple class="hidden" @change="onPicked" />
  <!-- Drop target, while dragging files over the console. -->
  <div
    v-if="dragging"
    class="pointer-events-none absolute inset-0 z-20 flex items-center justify-center bg-black/60"
  >
    <div class="bg-background/95 text-foreground flex flex-col items-center gap-2 rounded-lg border-2 border-dashed p-8 shadow-lg">
      <template v-if="canUpload">
        <FileUp class="size-8" />
        <p class="text-sm font-medium">Drop to upload to {{ vm?.name }}</p>
        <p class="text-muted-foreground text-xs">You'll choose where before anything is copied.</p>
      </template>
      <template v-else>
        <CircleAlert class="text-muted-foreground size-8" />
        <p class="text-sm font-medium">Uploading needs the Quicksand agent</p>
        <p class="text-muted-foreground text-xs">{{ vm?.name }} has no connected agent.</p>
      </template>
    </div>
  </div>

  <!-- Confirmation. -->
  <div v-if="pending" class="absolute inset-0 z-30 flex items-center justify-center bg-black/60" @keydown.esc="cancel">
    <form
      class="bg-background text-foreground flex w-[28rem] max-w-[calc(100%-2rem)] flex-col gap-4 rounded-lg border p-5 shadow-xl"
      @submit.prevent="upload"
    >
      <div>
        <h2 class="text-base font-semibold">Upload to {{ vm?.name }}?</h2>
        <p class="text-muted-foreground text-sm">
          {{ pending.length }} file{{ pending.length === 1 ? '' : 's' }} to copy into the guest.
        </p>
      </div>
      <ul class="bg-muted/50 max-h-36 overflow-y-auto rounded-md border px-3 py-2 text-xs">
        <li v-for="f in pending" :key="f.name" class="flex justify-between gap-3">
          <span class="truncate" :class="{ 'text-muted-foreground line-through': !replace && existing.has(f.name) }">
            {{ f.name }}
          </span>
          <span class="text-muted-foreground shrink-0 tabular-nums">{{ formatBytes(f.size) }}</span>
        </li>
      </ul>
      <p v-if="skippedFolders" class="text-muted-foreground text-xs">
        {{ skippedFolders }} folder{{ skippedFolders === 1 ? ' was' : 's were' }} left out: folders can't be uploaded.
      </p>
      <div class="flex flex-col gap-1.5">
        <Label for="drop-dir">Folder in the guest</Label>
        <Input id="drop-dir" ref="dirInput" v-model="dir" class="font-mono text-xs" @change="check" />
      </div>
      <div v-if="conflicts.length" class="flex flex-col gap-1.5 text-xs">
        <p class="text-muted-foreground">
          {{ conflicts.length === 1 ? `${conflicts[0]!.name} already exists` : `${conflicts.length} files already exist` }}
          there and {{ replace ? 'will be replaced' : 'will be skipped' }}.
        </p>
        <div class="flex items-center gap-2">
          <Checkbox id="drop-replace" v-model="replace" />
          <Label for="drop-replace" class="text-xs">Replace existing files</Label>
        </div>
      </div>
      <p v-if="error" class="text-destructive text-xs">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <Button type="button" variant="outline" @click="cancel">Cancel</Button>
        <!-- Not disabled while checking: leaving the folder field starts a
             check, which would swallow the click that left it. -->
        <Button type="submit" :disabled="!toUpload.length">
          <Spinner v-if="checking" /> <Upload v-else />
          Upload {{ toUpload.length }} file{{ toUpload.length === 1 ? '' : 's' }} ({{ formatBytes(totalSize) }})
        </Button>
      </div>
    </form>
  </div>

  <!-- Progress; "Clear finished" hides it. -->
  <div
    v-if="queue.uploads.value.length"
    class="bg-background/95 text-foreground absolute right-3 bottom-3 z-10 w-[26rem] max-w-[calc(100%-1.5rem)] rounded-md shadow-lg"
  >
    <UploadList :queue="queue" />
  </div>
</template>
