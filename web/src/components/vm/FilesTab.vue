<script lang="ts">
/** The directory each VM's Files tab last showed, kept while the app is open. */
const lastPaths = new Map<string, string>()
</script>

<script setup lang="ts">
import { api, errorMessage, type FileEntry, type FileHashes, type FileList, type VM } from '@/api'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Spinner } from '@/components/ui/spinner'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { formatBytes, formatDateTime } from '@/lib/format'
import {
  ArrowDown,
  ArrowUp,
  ArrowUpFromLine,
  Ban,
  ChevronRight,
  CircleCheck,
  CircleX,
  Copy,
  Download,
  EllipsisVertical,
  File,
  FileLock,
  FileSymlink,
  Fingerprint,
  Folder,
  FolderPlus,
  HardDrive,
  Pencil,
  RefreshCw,
  Search,
  Trash2,
  Upload,
  X,
} from '@lucide/vue'
import { computed, nextTick, onMounted, ref, shallowRef, useTemplateRef } from 'vue'

const props = defineProps<{ vm: VM }>()

const list = shallowRef<FileList | null>(null)
const error = ref<string>()
const loading = ref(false)
const filter = ref('')

const isWindows = computed(() => props.vm.agent?.os === 'windows')
const sep = computed(() => (isWindows.value ? '\\' : '/'))
/** On Windows "/" lists the drives, where nothing can be created. */
const atDriveList = computed(() => isWindows.value && list.value?.path === '/')

function join(dir: string, name: string) {
  return dir.endsWith(sep.value) ? dir + name : dir + sep.value + name
}

async function open(path: string) {
  loading.value = true
  try {
    list.value = await api.files(props.vm.id, path)
    lastPaths.set(props.vm.id, list.value.path)
    error.value = undefined
    if (path !== list.value.path) filter.value = ''
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

const refresh = () => open(list.value?.path ?? lastPaths.get(props.vm.id) ?? '')

onMounted(refresh)

// Breadcrumbs, from the top (the drive list on Windows, / elsewhere).
const crumbs = computed(() => {
  const path = list.value?.path
  if (!path) return []
  if (isWindows.value) {
    const out = [{ label: 'Drives', path: '/' }]
    if (path === '/') return out
    const [drive = path, ...rest] = path.split('\\').filter(Boolean)
    let acc = drive + '\\'
    out.push({ label: drive, path: acc })
    for (const part of rest) {
      acc = join(acc, part)
      out.push({ label: part, path: acc })
    }
    return out
  }
  const out = [{ label: '/', path: '/' }]
  let acc = ''
  for (const part of path.split('/').filter(Boolean)) {
    acc += '/' + part
    out.push({ label: part, path: acc })
  }
  return out
})

// Typing a path instead of clicking through.
const editingPath = ref(false)
const pathInput = ref('')
const pathField = useTemplateRef<{ $el: HTMLInputElement }>('pathField')
async function editPath() {
  pathInput.value = list.value?.path ?? ''
  editingPath.value = true
  await nextTick()
  pathField.value?.$el.focus()
  pathField.value?.$el.select()
}
function goToPath() {
  editingPath.value = false
  const p = pathInput.value.trim()
  if (p && p !== list.value?.path) open(p)
}

type SortKey = 'name' | 'size' | 'modTime'
const sortKey = ref<SortKey>('name')
const sortDesc = ref(false)
function sortBy(key: SortKey) {
  if (sortKey.value === key) sortDesc.value = !sortDesc.value
  else {
    sortKey.value = key
    sortDesc.value = key !== 'name'
  }
}

const visible = computed(() => {
  const q = filter.value.trim().toLowerCase()
  const entries = (list.value?.entries ?? []).filter((e) => !q || e.name.toLowerCase().includes(q))
  const dir = sortDesc.value ? -1 : 1
  return entries.sort((a, b) => {
    // Directories stay first whichever way the list is sorted.
    if (a.dir !== b.dir) return a.dir ? -1 : 1
    switch (sortKey.value) {
      case 'size':
        return (a.size - b.size) * dir || a.name.localeCompare(b.name)
      case 'modTime':
        return (a.modTime ?? '').localeCompare(b.modTime ?? '') * dir || a.name.localeCompare(b.name)
    }
    return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' }) * dir
  })
})

const summary = computed(() => {
  const entries = list.value?.entries ?? []
  const dirs = entries.filter((e) => e.dir).length
  const files = entries.length - dirs
  const bytes = entries.reduce((n, e) => n + (e.dir ? 0 : e.size), 0)
  const parts = []
  if (dirs) parts.push(`${dirs} folder${dirs === 1 ? '' : 's'}`)
  if (files) parts.push(`${files} file${files === 1 ? '' : 's'} (${formatBytes(bytes)})`)
  return parts.join(', ') || 'Empty folder'
})

const columns: { key: SortKey; label: string; class?: string }[] = [
  { key: 'name', label: 'Name' },
  { key: 'size', label: 'Size', class: 'w-24 text-right' },
  { key: 'modTime', label: 'Modified', class: 'w-44' },
]

const entryIcon = (e: FileEntry) => (atDriveList.value ? HardDrive : e.symlink && !e.dir ? FileSymlink : e.dir ? Folder : File)

const downloadUrl = (e: FileEntry) => api.downloadUrl(props.vm.id, e.path)

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    error.value = "Couldn't copy to the clipboard."
  }
}

// New folder.
const creatingFolder = ref(false)
const folderName = ref('')
const folderError = ref<string>()
const validFolderName = computed(() => {
  const n = folderName.value.trim()
  return n !== '' && n !== '.' && n !== '..' && !/[/\\]/.test(n)
})
function askNewFolder() {
  folderName.value = ''
  folderError.value = undefined
  creatingFolder.value = true
}
async function createFolder() {
  if (!list.value || !validFolderName.value) return
  try {
    const e = await api.mkdir(props.vm.id, join(list.value.path, folderName.value.trim()))
    creatingFolder.value = false
    open(e.path)
  } catch (e) {
    folderError.value = errorMessage(e)
  }
}

// Delete.
const deleting = shallowRef<FileEntry | null>(null)
const deleteError = ref<string>()
const deleteBusy = ref(false)
function askDelete(e: FileEntry) {
  deleting.value = e
  deleteError.value = undefined
}
async function remove() {
  const e = deleting.value
  if (!e) return
  deleteBusy.value = true
  try {
    await api.deleteFile(props.vm.id, e.path, e.dir)
    deleting.value = null
    refresh()
  } catch (err) {
    deleteError.value = errorMessage(err)
  } finally {
    deleteBusy.value = false
  }
}

// Encrypted zip downloads: the password goes in a POST, which returns a
// one-time link the browser then downloads like any other.
const zipping = shallowRef<FileEntry | null>(null)
const zipPassword = ref('')
const zipError = ref<string>()
const zipBusy = ref(false)
function askZip(e: FileEntry) {
  zipping.value = e
  zipPassword.value = 'infected'
  zipError.value = undefined
}
async function downloadZip() {
  const e = zipping.value
  if (!e || !zipPassword.value) return
  zipBusy.value = true
  try {
    const { url } = await api.prepareZip(props.vm.id, e.path, zipPassword.value)
    const a = document.createElement('a')
    a.href = url
    a.download = ''
    a.click()
    zipping.value = null
  } catch (err) {
    zipError.value = errorMessage(err)
  } finally {
    zipBusy.value = false
  }
}

// Checksums.
const hashing = shallowRef<FileEntry | null>(null)
const hashes = ref<FileHashes>()
const hashError = ref<string>()
async function showHashes(e: FileEntry) {
  hashing.value = e
  hashes.value = undefined
  hashError.value = undefined
  try {
    const h = await api.hashFile(props.vm.id, e.path)
    if (hashing.value === e) hashes.value = h
  } catch (err) {
    if (hashing.value === e) hashError.value = errorMessage(err)
  }
}
const hashRows = computed(() =>
  hashes.value
    ? ([
      ['MD5', hashes.value.md5],
      ['SHA-1', hashes.value.sha1],
      ['SHA-256', hashes.value.sha256],
    ] as const)
    : [],
)

// Uploads, one at a time, into the directory shown when they were started.
interface UploadItem {
  name: string
  size: number
  progress: number
  state: 'queued' | 'uploading' | 'done' | 'failed' | 'cancelled'
  error?: string
}
const uploads = ref<UploadItem[]>([])
const fileInput = useTemplateRef('fileInput')
const pendingFiles = shallowRef<File[]>([])
const conflicts = computed(() => {
  const existing = new Set((list.value?.entries ?? []).filter((e) => !e.dir).map((e) => e.name))
  return pendingFiles.value.filter((f) => existing.has(f.name))
})
const dragging = ref(false)
let uploading = false
const queue: { file: File; dir: string; overwrite: boolean; item: UploadItem }[] = []
// The upload in progress (one at a time), to cancel it.
let current: { item: UploadItem; controller: AbortController } | undefined

function chooseFiles() {
  fileInput.value?.click()
}

function onFileInput(ev: Event) {
  const input = ev.target as HTMLInputElement
  startUploads([...(input.files ?? [])])
  input.value = ''
}

function onDrop(ev: DragEvent) {
  dragging.value = false
  if (!list.value || atDriveList.value || !ev.dataTransfer) return
  const files: File[] = []
  let folders = 0
  for (const item of ev.dataTransfer.items) {
    if (item.kind !== 'file') continue
    if (item.webkitGetAsEntry()?.isDirectory) {
      folders++
      continue
    }
    const f = item.getAsFile()
    if (f) files.push(f)
  }
  if (folders) error.value = "Folders can't be uploaded; upload their files, or a zip of them."
  startUploads(files)
}

function onDragOver(ev: DragEvent) {
  if (!list.value || atDriveList.value || !ev.dataTransfer?.types.includes('Files')) return
  ev.preventDefault()
  dragging.value = true
}

function startUploads(files: File[]) {
  if (!files.length || !list.value) return
  const dirs = new Set((list.value.entries ?? []).filter((e) => e.dir).map((e) => e.name))
  const clash = files.filter((f) => dirs.has(f.name))
  if (clash.length) {
    error.value = `A folder named ${clash.map((f) => f.name).join(', ')} is already here.`
    files = files.filter((f) => !dirs.has(f.name))
  }
  pendingFiles.value = files
  // Ask about replacing existing files first.
  if (!conflicts.value.length) confirmUploads('replace')
}

function confirmUploads(existing: 'replace' | 'skip' | 'cancel') {
  const files = pendingFiles.value
  const clashing = new Set(conflicts.value)
  pendingFiles.value = []
  if (existing === 'cancel' || !list.value) return
  for (const file of files) {
    if (existing === 'skip' && clashing.has(file)) continue
    const item: UploadItem = { name: file.name, size: file.size, progress: 0, state: 'queued' }
    uploads.value.push(item)
    queue.push({ file, dir: list.value.path, overwrite: clashing.has(file), item: uploads.value.at(-1)! })
  }
  runQueue()
}

async function runQueue() {
  if (uploading) return
  uploading = true
  while (queue.length) {
    const { file, dir, overwrite, item } = queue.shift()!
    item.state = 'uploading'
    const controller = new AbortController()
    current = { item, controller }
    try {
      await api.upload(props.vm.id, join(dir, file.name), file, overwrite, (f) => (item.progress = f * 100), controller.signal)
      item.state = 'done'
      item.progress = 100
      if (list.value?.path === dir) refresh()
    } catch (e) {
      if (controller.signal.aborted) item.state = 'cancelled'
      else {
        item.state = 'failed'
        item.error = errorMessage(e)
      }
    }
    current = undefined
  }
  uploading = false
}

function cancelUpload(item: UploadItem) {
  const i = queue.findIndex((q) => q.item === item)
  if (i >= 0) {
    queue.splice(i, 1)
    item.state = 'cancelled'
  } else if (current?.item === item) {
    current.controller.abort()
  }
}

function cancelAllUploads() {
  for (const q of queue.splice(0)) q.item.state = 'cancelled'
  current?.controller.abort()
}

const activeUploads = computed(() => uploads.value.filter((u) => u.state === 'queued' || u.state === 'uploading').length)
function clearUploads() {
  uploads.value = uploads.value.filter((u) => u.state === 'queued' || u.state === 'uploading')
}
</script>

<template>
  <div class="relative flex flex-col gap-3" @dragover="onDragOver" @dragleave.self="dragging = false" @drop.prevent="onDrop">
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="outline" size="icon-sm" title="Up" :disabled="!list?.parent || loading" @click="list?.parent && open(list.parent)">
        <ArrowUp />
      </Button>
      <form v-if="editingPath" class="min-w-48 flex-1" @submit.prevent="goToPath">
        <Input ref="pathField" v-model="pathInput" class="h-8 font-mono text-xs" @keydown.esc="editingPath = false" @blur="editingPath = false" />
      </form>
      <nav v-else class="bg-background flex h-8 min-w-48 flex-1 items-center overflow-x-auto rounded-md border px-1 text-sm" aria-label="Path">
        <template v-for="(c, i) in crumbs" :key="c.path">
          <ChevronRight v-if="i > 0 && !(i === 1 && !isWindows)" class="text-muted-foreground size-3.5 shrink-0" />
          <button type="button" class="hover:bg-accent shrink-0 rounded px-1.5 py-0.5 whitespace-nowrap" :class="{ 'font-medium': i === crumbs.length - 1 }" @click="open(c.path)">
            {{ c.label }}
          </button>
        </template>
        <button type="button" class="text-muted-foreground hover:text-foreground ml-auto shrink-0 p-1" title="Type a path" @click="editPath">
          <Pencil class="size-3.5" />
        </button>
      </nav>
      <div class="relative w-48">
        <Search class="text-muted-foreground absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
        <Input v-model="filter" placeholder="Filter" class="h-8 pl-8" />
      </div>
      <Button variant="outline" size="sm" :disabled="!list || atDriveList" @click="askNewFolder">
        <FolderPlus /> New folder
      </Button>
      <Button variant="outline" size="sm" :disabled="!list || atDriveList" @click="chooseFiles">
        <Upload /> Upload
      </Button>
      <input ref="fileInput" type="file" multiple class="hidden" @change="onFileInput" />
      <Button variant="outline" size="icon-sm" title="Refresh" :disabled="loading" @click="refresh">
        <RefreshCw :class="{ 'animate-spin': loading }" />
      </Button>
    </div>
    <p v-if="error" class="text-destructive flex items-start gap-1.5 text-sm">
      <span class="flex-1">{{ error }}</span>
      <button type="button" class="opacity-70 hover:opacity-100" title="Dismiss" @click="error = undefined">
        <X class="size-4" />
      </button>
    </p>

    <div v-if="uploads.length" class="flex flex-col gap-1.5 rounded-md border p-3 text-xs">
      <div class="flex items-center gap-2">
        <span class="font-medium">
          {{ activeUploads ? `Uploading ${activeUploads} file${activeUploads === 1 ? '' : 's'}` : 'Uploads' }}
        </span>
        <div class="ml-auto flex gap-1">
          <Button v-if="activeUploads > 1" variant="ghost" size="sm" class="h-6" @click="cancelAllUploads">Cancel all</Button>
          <Button v-if="uploads.length > activeUploads" variant="ghost" size="sm" class="h-6" @click="clearUploads">
            Clear finished
          </Button>
        </div>
      </div>
      <div v-for="(u, i) in uploads" :key="i" class="grid grid-cols-[minmax(0,1fr)_8rem_5rem_1.5rem] items-center gap-3">
        <span class="truncate" :title="u.name">{{ u.name }}</span>
        <span v-if="u.state === 'done'" class="flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
          <CircleCheck class="size-3.5" /> Uploaded
        </span>
        <span v-else-if="u.state === 'failed'" class="text-destructive flex items-center gap-1">
          <CircleX class="size-3.5" /> Failed
        </span>
        <span v-else-if="u.state === 'cancelled'" class="text-muted-foreground flex items-center gap-1">
          <Ban class="size-3.5" /> Cancelled
        </span>
        <Progress v-else :model-value="u.progress" class="h-1.5" />
        <span class="text-right tabular-nums" :class="u.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'" :title="u.error">
          {{
            u.state === 'failed' || u.state === 'cancelled'
              ? ''
              : u.state === 'done'
                ? formatBytes(u.size)
                : u.state === 'queued'
                  ? 'Waiting'
                  : `${Math.floor(u.progress)}%`
          }}
        </span>
        <Button v-if="u.state === 'queued' || u.state === 'uploading'" variant="ghost" size="icon-sm" class="size-6" title="Cancel upload" @click="cancelUpload(u)">
          <X />
        </Button>
        <span v-else />
        <p v-if="u.error" class="text-destructive col-span-4 -mt-1">{{ u.error }}</p>
      </div>
    </div>

    <div class="overflow-x-auto rounded-md border">
      <Table class="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead v-for="c in columns" :key="c.key" :class="c.class">
              <button type="button" class="inline-flex items-center gap-1" @click="sortBy(c.key)">
                {{ c.label }}
                <component :is="sortDesc ? ArrowDown : ArrowUp" v-if="sortKey === c.key" class="size-3" />
              </button>
            </TableHead>
            <TableHead v-if="!isWindows" class="w-28">Mode</TableHead>
            <TableHead class="w-20" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="e in visible" :key="e.path">
            <TableCell class="max-w-md">
              <button v-if="e.dir" type="button" class="flex max-w-full cursor-pointer items-center gap-2 font-medium hover:underline" @click="open(e.path)">
                <component :is="entryIcon(e)" class="size-4 shrink-0 text-sky-600 dark:text-sky-400" />
                <span class="truncate">{{ e.name }}</span>
              </button>
              <span v-else class="flex items-center gap-2" :title="e.symlink ? 'Symbolic link' : undefined">
                <component :is="entryIcon(e)" class="text-muted-foreground size-4 shrink-0" />
                <span class="truncate">{{ e.name }}</span>
              </span>
            </TableCell>
            <TableCell class="text-right tabular-nums">{{ e.dir ? '' : formatBytes(e.size) }}</TableCell>
            <TableCell class="text-muted-foreground">{{ formatDateTime(e.modTime) }}</TableCell>
            <TableCell v-if="!isWindows" class="text-muted-foreground font-mono">{{ e.mode }}</TableCell>
            <TableCell class="text-right whitespace-nowrap">
              <Button v-if="!atDriveList" variant="ghost" size="icon-sm" as="a" :href="downloadUrl(e)" download :title="e.dir ? 'Download as zip' : 'Download'">
                <Download />
              </Button>
              <DropdownMenu v-if="!atDriveList">
                <DropdownMenuTrigger as-child>
                  <Button variant="ghost" size="icon-sm" title="More">
                    <EllipsisVertical />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" class="w-44">
                  <DropdownMenuItem as-child>
                    <a :href="downloadUrl(e)" download>
                      <Download /> {{ e.dir ? 'Download as zip' : 'Download' }}
                    </a>
                  </DropdownMenuItem>
                  <DropdownMenuItem @select="askZip(e)">
                    <FileLock /> Download encrypted zip…
                  </DropdownMenuItem>
                  <DropdownMenuItem v-if="!e.dir" @select="showHashes(e)">
                    <Fingerprint /> Checksums
                  </DropdownMenuItem>
                  <DropdownMenuItem @select="copy(e.path)">
                    <Copy /> Copy path
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem variant="destructive" @select="askDelete(e)">
                    <Trash2 /> Delete
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </TableCell>
          </TableRow>
          <TableEmpty v-if="list && !visible.length" :colspan="isWindows ? 4 : 5">
            {{ filter ? 'No matching files.' : 'This folder is empty.' }}
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <p v-if="list" class="text-muted-foreground text-xs">
      {{ summary }}<template v-if="!atDriveList"> · drop files here to upload them</template>
    </p>

    <div v-if="dragging" class="border-primary bg-background/80 pointer-events-none absolute inset-0 flex items-center justify-center rounded-md border-2 border-dashed">
      <p class="flex items-center gap-2 text-sm font-medium">
        <ArrowUpFromLine class="size-4" /> Drop to upload to {{ list?.path }}
      </p>
    </div>

    <Dialog v-model:open="creatingFolder">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>New folder</DialogTitle>
          <DialogDescription class="font-mono text-xs break-all">{{ list?.path }}</DialogDescription>
        </DialogHeader>
        <form id="new-folder" @submit.prevent="createFolder">
          <Input v-model="folderName" placeholder="Folder name" autofocus />
        </form>
        <p v-if="folderError" class="text-destructive text-sm">{{ folderError }}</p>
        <DialogFooter>
          <Button variant="outline" @click="creatingFolder = false">Cancel</Button>
          <Button type="submit" form="new-folder" :disabled="!validFolderName">Create</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <AlertDialog :open="!!deleting" @update:open="(o) => !o && (deleting = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {{ deleting?.name }}?</AlertDialogTitle>
          <AlertDialogDescription>
            <template v-if="deleting?.dir">The folder and everything in it are deleted from the guest.</template>
            <template v-else>The file is deleted from the guest.</template>
            This can't be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <p v-if="deleteError" class="text-destructive text-sm">{{ deleteError }}</p>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button variant="destructive" :disabled="deleteBusy" @click="remove">
            <Trash2 /> Delete
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <AlertDialog :open="conflicts.length > 0" @update:open="(o) => !o && confirmUploads('cancel')">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {{ conflicts.length === 1 ? `${conflicts[0]!.name} already exists` : `${conflicts.length} files already exist` }}
          </AlertDialogTitle>
          <AlertDialogDescription>
            <template v-if="conflicts.length > 1">{{conflicts.map((f) => f.name).join(', ')}}. </template>
            Replace {{ conflicts.length === 1 ? 'it' : 'them' }} with the
            {{ conflicts.length === 1 ? 'file' : 'files' }} you're uploading?
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button v-if="pendingFiles.length > conflicts.length" variant="outline" @click="confirmUploads('skip')">
            Skip {{ conflicts.length === 1 ? 'it' : 'them' }}
          </Button>
          <Button @click="confirmUploads('replace')">Replace</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <Dialog :open="!!zipping" @update:open="(o) => !o && (zipping = null)">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Download {{ zipping?.name }} as an encrypted zip</DialogTitle>
          <DialogDescription>
            The {{ zipping?.dir ? 'folder' : 'file' }} is zipped and encrypted as it downloads. ZipCrypto
            encryption opens anywhere, Windows Explorer included, but is weak: treat it as a deterrent, not
            real protection.
          </DialogDescription>
        </DialogHeader>
        <form id="zip-password" class="flex flex-col gap-1.5" @submit.prevent="downloadZip">
          <Label for="zip-password-input">Password</Label>
          <Input id="zip-password-input" v-model="zipPassword" autocomplete="off" autofocus />
        </form>
        <p v-if="zipError" class="text-destructive text-sm">{{ zipError }}</p>
        <DialogFooter>
          <Button variant="outline" @click="zipping = null">Cancel</Button>
          <Button type="submit" form="zip-password" :disabled="!zipPassword || zipBusy">
            <Download /> Download
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog :open="!!hashing" @update:open="(o) => !o && (hashing = null)">
      <DialogContent class="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Checksums</DialogTitle>
          <DialogDescription class="font-mono text-xs break-all">{{ hashing?.path }}</DialogDescription>
        </DialogHeader>
        <p v-if="hashError" class="text-destructive text-sm">{{ hashError }}</p>
        <p v-else-if="!hashes" class="text-muted-foreground flex items-center gap-2 text-sm">
          <Spinner /> Reading {{ hashing ? formatBytes(hashing.size) : '' }}…
        </p>
        <dl v-else class="grid grid-cols-[max-content_minmax(0,1fr)_max-content] items-center gap-x-3 gap-y-1 text-sm">
          <template v-for="[label, value] in hashRows" :key="label">
            <dt class="text-muted-foreground">{{ label }}</dt>
            <dd class="font-mono text-xs break-all select-all">{{ value }}</dd>
            <Button variant="ghost" size="icon-sm" title="Copy" @click="copy(value)">
              <Copy />
            </Button>
          </template>
        </dl>
      </DialogContent>
    </Dialog>
  </div>
</template>
