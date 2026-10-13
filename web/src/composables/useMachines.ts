import { computed, ref, shallowRef } from 'vue'
import type { VM } from '@/api'
import { forgetPreview } from '@/lib/previews'

// The signed-in user's VMs, kept current by the server's event stream.
const machines = shallowRef(new Map<string, VM>())
const loaded = ref(false)
const connected = ref(false)
let source: EventSource | undefined
let users = 0

function replaceAll(list: VM[]) {
  const next = new Map(list.map((vm) => [vm.id, vm]))
  for (const id of machines.value.keys()) if (!next.has(id)) forgetPreview(id)
  machines.value = next
  loaded.value = true
}

function upsert(vm: VM) {
  machines.value = new Map(machines.value).set(vm.id, vm)
}

function remove(id: string) {
  const next = new Map(machines.value)
  next.delete(id)
  machines.value = next
  forgetPreview(id)
}

function open() {
  source = new EventSource('/api/events')
  source.addEventListener('open', () => (connected.value = true))
  // EventSource reconnects by itself; a 401 (signed out) ends it for good.
  source.addEventListener('error', () => (connected.value = source?.readyState === EventSource.OPEN))
  source.addEventListener('vms', (e) => replaceAll(JSON.parse((e as MessageEvent).data)))
  source.addEventListener('vm', (e) => upsert(JSON.parse((e as MessageEvent).data)))
  source.addEventListener('vm-deleted', (e) => remove(JSON.parse((e as MessageEvent).data).id))
}

/**
 * Subscribes to live VM updates while a component is mounted. Call stop()
 * on unmount; the stream closes when nothing uses it.
 */
export function useMachines() {
  if (users++ === 0) open()
  return {
    machines: computed(() =>
      [...machines.value.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
    ),
    get: (id: string) => computed(() => machines.value.get(id)),
    loaded,
    connected,
    /** Apply a VM returned by an API call before its event arrives. */
    upsert,
    stop() {
      if (--users === 0) {
        source?.close()
        source = undefined
        connected.value = false
      }
    },
  }
}

/** Forget cached VMs, e.g. when signing out. */
export function resetMachines() {
  machines.value = new Map()
  loaded.value = false
}
