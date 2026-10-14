import { computed, ref } from 'vue'
import { api, errorMessage } from '@/api'

export interface UploadItem {
  name: string
  size: number
  /** 0..100 */
  progress: number
  state: 'queued' | 'uploading' | 'done' | 'failed' | 'cancelled'
  error?: string
}

/** Joins a guest directory and a file name with the guest's separator. */
export function guestJoin(dir: string, name: string): string {
  const sep = dir.includes('\\') || /^[A-Za-z]:/.test(dir) ? '\\' : '/'
  return dir.endsWith(sep) ? dir + name : dir + sep + name
}

/** The files in a drop, and how many folders (which can't be uploaded) it had. */
export function filesFromDrop(data: DataTransfer): { files: File[]; folders: number } {
  const files: File[] = []
  let folders = 0
  for (const item of data.items) {
    if (item.kind !== 'file') continue
    if (item.webkitGetAsEntry()?.isDirectory) {
      folders++
      continue
    }
    const f = item.getAsFile()
    if (f) files.push(f)
  }
  return { files, folders }
}

/**
 * Uploads files to a VM's guest one at a time, with progress and
 * cancelling; onUploaded is called with each finished file's directory.
 */
export function useUploadQueue(vmId: () => string, onUploaded?: (dir: string) => void) {
  const uploads = ref<UploadItem[]>([])
  const queue: { file: File; dir: string; overwrite: boolean; item: UploadItem }[] = []
  // The upload in progress, to cancel it.
  let current: { item: UploadItem; controller: AbortController } | undefined
  let running = false

  /** Queues files for dir; overwrite says which may replace existing files. */
  function enqueue(files: File[], dir: string, overwrite: (f: File) => boolean = () => false) {
    for (const file of files) {
      uploads.value.push({ name: file.name, size: file.size, progress: 0, state: 'queued' })
      queue.push({ file, dir, overwrite: overwrite(file), item: uploads.value.at(-1)! })
    }
    run()
  }

  async function run() {
    if (running) return
    running = true
    while (queue.length) {
      const { file, dir, overwrite, item } = queue.shift()!
      item.state = 'uploading'
      const controller = new AbortController()
      current = { item, controller }
      try {
        await api.upload(vmId(), guestJoin(dir, file.name), file, overwrite, (f) => (item.progress = f * 100), controller.signal)
        item.state = 'done'
        item.progress = 100
        onUploaded?.(dir)
      } catch (e) {
        if (controller.signal.aborted) item.state = 'cancelled'
        else {
          item.state = 'failed'
          item.error = errorMessage(e)
        }
      }
      current = undefined
    }
    running = false
  }

  function cancel(item: UploadItem) {
    const i = queue.findIndex((q) => q.item === item)
    if (i >= 0) {
      queue.splice(i, 1)
      item.state = 'cancelled'
    } else if (current?.item === item) {
      current.controller.abort()
    }
  }

  function cancelAll() {
    for (const q of queue.splice(0)) q.item.state = 'cancelled'
    current?.controller.abort()
  }

  const active = computed(() => uploads.value.filter((u) => u.state === 'queued' || u.state === 'uploading').length)
  function clearFinished() {
    uploads.value = uploads.value.filter((u) => u.state === 'queued' || u.state === 'uploading')
  }

  return { uploads, enqueue, cancel, cancelAll, clearFinished, active }
}

export type UploadQueue = ReturnType<typeof useUploadQueue>
