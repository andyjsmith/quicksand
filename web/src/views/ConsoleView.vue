<script setup lang="ts">
import { useEventListener, useFullscreen, useTitle } from '@vueuse/core'
import { Columns2, Plus, Square, X } from '@lucide/vue'
import { computed, onBeforeUnmount, reactive, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ConsolePane from '@/components/console/ConsolePane.vue'
import ConsoleSidebar from '@/components/console/ConsoleSidebar.vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { useMachines } from '@/composables/useMachines'
import type { ConsoleProtocol } from '@/consoles/types'
import { vmStatus } from '@/lib/format'

// The console workspace: one or more VMs' consoles as tabs, one shown (or
// two side by side, with a draggable divider). The sidebar and fullscreen act
// on the focused one. Tabs and their protocols live in the URL
// (?tabs=id:spice,id2), so a reload restores them.

const props = defineProps<{ id: string }>()
const route = useRoute()
const router = useRouter()
const root = ref<HTMLElement | null>(null)
const workspace = ref<HTMLElement | null>(null)

const machines = useMachines()
onBeforeUnmount(machines.stop)

interface Tab {
  id: string
  protocol?: ConsoleProtocol
}

function initialTabs(): Tab[] {
  const parse = (entry: string): Tab => {
    const [id = '', protocol] = entry.split(':')
    return { id, protocol: protocol === 'spice' || protocol === 'vnc' ? protocol : undefined }
  }
  const query = typeof route.query.tabs === 'string' ? route.query.tabs.split(',').filter(Boolean).map(parse) : []
  const protocol = route.query.protocol
  const first: Tab = { id: props.id, protocol: protocol === 'spice' || protocol === 'vnc' ? protocol : undefined }
  const tabs = query.some((t) => t.id === props.id) ? query : [first, ...query]
  return tabs.filter((t, i) => t.id && tabs.findIndex((u) => u.id === t.id) === i)
}

const tabs = ref<Tab[]>(initialTabs())
const focused = ref(props.id)
/** The two tabs shown side by side, if split (?split=left,right). */
const split = ref<{ left: string; right: string } | null>(initialSplit())
/** The left pane's share of the width when split (?ratio=0.3). */
const ratio = ref(Math.min(0.85, Math.max(0.15, Number(route.query.ratio) || 0.5)))

function initialSplit() {
  const [left, right] = typeof route.query.split === 'string' ? route.query.split.split(',') : []
  const open = (id?: string) => !!id && tabs.value.some((t) => t.id === id)
  return open(left) && open(right) && left !== right ? { left: left!, right: right! } : null
}

const vmOf = (id: string) => machines.machines.value.find((v) => v.id === id)
const focusedVM = computed(() => vmOf(focused.value))
useTitle(computed(() => (focusedVM.value ? `${focusedVM.value.name} · Quicksand` : 'Console · Quicksand')))

// Keep the URL in step: the focused VM in the path; tabs, the split and the
// divider in the query. (The divider updates it when a drag ends.)
function syncURL() {
  const entry = (t: Tab) => (t.protocol ? `${t.id}:${t.protocol}` : t.id)
  const query = {
    ...route.query,
    tabs: tabs.value.length > 1 ? tabs.value.map(entry).join(',') : undefined,
    protocol: tabs.value.find((t) => t.id === focused.value)?.protocol,
    split: split.value ? `${split.value.left},${split.value.right}` : undefined,
    ratio: split.value && ratio.value !== 0.5 ? ratio.value.toFixed(3) : undefined,
  }
  router.replace({ name: 'console', params: { id: focused.value }, query })
}
watch([tabs, focused, split], syncURL, { deep: true })

// The panes, by tab ID, for the sidebar and keyboard focus.
type PaneInstance = InstanceType<typeof ConsolePane>
const panes = reactive(new Map<string, PaneInstance>())
const setPane = (id: string, el: unknown) => (el ? panes.set(id, el as PaneInstance) : panes.delete(id))
const focusedCtl = computed(() => panes.get(focused.value)?.ctl)

function focusTab(id: string) {
  // In split view, a tab that isn't showing replaces the focused side.
  if (split.value && split.value.left !== id && split.value.right !== id) {
    const side = split.value.left === focused.value ? 'left' : 'right'
    split.value = { ...split.value, [side]: id }
  }
  focused.value = id
  panes.get(id)?.ctl.focus()
}

function closeTab(id: string) {
  const i = tabs.value.findIndex((t) => t.id === id)
  if (i < 0 || tabs.value.length === 1) return
  tabs.value = tabs.value.filter((t) => t.id !== id)
  if (split.value && (split.value.left === id || split.value.right === id)) {
    const other = split.value.left === id ? split.value.right : split.value.left
    split.value = null
    focused.value = other
  } else if (focused.value === id) {
    focused.value = tabs.value[Math.min(i, tabs.value.length - 1)]!.id
  }
}

function setProtocol(id: string, protocol: ConsoleProtocol) {
  const tab = tabs.value.find((t) => t.id === id)
  if (tab && tab.protocol !== protocol) tab.protocol = protocol
}

/** Where each pane is: hidden, or a share of the workspace's width. */
function placement(id: string): { left: string; width: string } | null {
  if (split.value) {
    if (split.value.left === id) return { left: '0%', width: `${ratio.value * 100}%` }
    if (split.value.right === id) return { left: `${ratio.value * 100}%`, width: `${(1 - ratio.value) * 100}%` }
    return null
  }
  return focused.value === id ? { left: '0%', width: '100%' } : null
}

// Opening more VMs.
const picking = ref(false)
const chosen = ref<string[]>([])
const openable = computed(() => machines.machines.value.filter((v) => !tabs.value.some((t) => t.id === v.id)))
function openPicker() {
  panelOpen.value = false
  chosen.value = []
  picking.value = true
}
function openChosen() {
  const ids = chosen.value.filter((id) => !tabs.value.some((t) => t.id === id))
  tabs.value = [...tabs.value, ...ids.map((id) => ({ id }))]
  picking.value = false
  if (ids[0]) focusTab(ids[0])
}
function toggleChosen(id: string, on: boolean) {
  chosen.value = on ? [...chosen.value, id] : chosen.value.filter((c) => c !== id)
}

// Dragging a tab onto the left or right half splits the view.
const TAB_TYPE = 'application/x-quicksand-tab'
const draggedTab = ref<string | null>(null)
const dropSide = ref<'left' | 'right' | null>(null)
function onTabDragStart(e: DragEvent, id: string) {
  draggedTab.value = id
  e.dataTransfer?.setData(TAB_TYPE, id)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function onTabDragEnd() {
  draggedTab.value = null
  dropSide.value = null
}
function onWorkspaceDragOver(e: DragEvent) {
  if (!draggedTab.value || !workspace.value) return
  e.preventDefault()
  const rect = workspace.value.getBoundingClientRect()
  dropSide.value = e.clientX - rect.left < rect.width / 2 ? 'left' : 'right'
}
function onWorkspaceDrop(e: DragEvent) {
  const id = draggedTab.value
  const side = dropSide.value
  onTabDragEnd()
  if (!id || !side) return
  e.preventDefault()
  // The other side keeps what it shows: the other half of the split, or the
  // focused tab (or another, if that's the one being dragged).
  const current = split.value
  let other = current ? (side === 'left' ? current.right : current.left) : focused.value
  if (other === id) other = (current ? (side === 'left' ? current.left : current.right) : undefined) ?? ''
  if (!other || other === id) other = tabs.value.find((t) => t.id !== id)?.id ?? ''
  if (!other) return
  split.value = side === 'left' ? { left: id, right: other } : { left: other, right: id }
  focusTab(id)
}
function unsplit() {
  split.value = null
}

// The divider between split panes.
const resizing = ref(false)
function onDividerDown(e: PointerEvent) {
  resizing.value = true
  ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
}
function onDividerMove(e: PointerEvent) {
  if (!resizing.value || !workspace.value) return
  const rect = workspace.value.getBoundingClientRect()
  ratio.value = Math.min(0.85, Math.max(0.15, (e.clientX - rect.left) / rect.width))
}
function onDividerUp() {
  if (!resizing.value) return
  resizing.value = false
  syncURL()
}

// Files dropped outside a pane (the tab bar, the divider) mustn't make the
// browser open them; panes handle their own drops.
useEventListener(window, 'dragover', (e: DragEvent) => e.dataTransfer?.types.includes('Files') && e.preventDefault())
useEventListener(window, 'drop', (e: DragEvent) => e.dataTransfer?.types.includes('Files') && e.preventDefault())

// The sidebar.
const panelOpen = ref(false)
watch(panelOpen, (open) => {
  if (!open) focusedCtl.value?.focus()
})
// Ctrl+Alt+Shift toggles the panel, as in Guacamole.
useEventListener(
  window,
  'keydown',
  (e: KeyboardEvent) => {
    const isModifier = ['Control', 'Alt', 'Shift'].includes(e.key)
    if (isModifier && e.ctrlKey && e.altKey && e.shiftKey) panelOpen.value = !panelOpen.value
  },
  { capture: true },
)

// Fullscreen. Browser shortcuts (Ctrl+W, Ctrl+T, Ctrl+Tab, ...) go to the
// guest instead: Firefox (152+) with the fullscreen keyboardLock option,
// Chromium browsers with the Keyboard Lock API, which also captures Escape
// (hold it to leave fullscreen) and the Windows key. Other browsers still
// keep their own shortcuts.
const { isFullscreen, exit: exitFullscreen } = useFullscreen(root)
const keyboard = (navigator as Navigator & { keyboard?: { lock(): Promise<void>; unlock(): void } }).keyboard
async function toggleFullscreen() {
  panelOpen.value = false
  if (document.fullscreenElement) {
    await exitFullscreen()
    return
  }
  try {
    await root.value?.requestFullscreen({ navigationUI: 'hide', keyboardLock: 'browser' } as FullscreenOptions)
  } catch {
    return
  }
  await keyboard?.lock().catch(() => {})
  focusedCtl.value?.focus()
}
// However fullscreen ends (Escape, the browser), give the keys back.
watch(isFullscreen, (full) => {
  if (!full) keyboard?.unlock()
})

const showTabs = computed(() => tabs.value.length > 1)
const dotClass = (id: string) => {
  const vm = vmOf(id)
  return vm ? vmStatus(vm).dot : 'bg-muted-foreground'
}
const sidebarKey = shallowRef(0)
watch(focused, () => sidebarKey.value++)
</script>

<template>
  <div ref="root" class="dark bg-background text-foreground relative flex h-dvh w-full flex-col overflow-hidden">
    <!-- Tabs, once there's more than one console open. -->
    <div v-if="showTabs" class="bg-muted/40 flex h-9 shrink-0 items-end gap-1 border-b px-2 pt-1.5">
      <div
        v-for="t in tabs"
        :key="t.id"
        draggable="true"
        class="group flex h-full max-w-56 min-w-28 cursor-pointer items-center gap-2 rounded-t-md border border-b-0 px-3 text-xs select-none"
        :class="
          t.id === focused
            ? 'bg-background text-foreground'
            : 'text-muted-foreground hover:bg-background/60 border-transparent'
        "
        :title="`${vmOf(t.id)?.name ?? t.id} — drag to the left or right half to split`"
        @mousedown="focusTab(t.id)"
        @dragstart="onTabDragStart($event, t.id)"
        @dragend="onTabDragEnd"
      >
        <span class="inline-block size-2 shrink-0 rounded-full" :class="dotClass(t.id)" />
        <span class="flex-1 truncate">{{ vmOf(t.id)?.name ?? 'Loading…' }}</span>
        <span
          v-if="split && (split.left === t.id || split.right === t.id)"
          class="text-muted-foreground text-[10px]"
          :title="split.left === t.id ? 'Shown on the left' : 'Shown on the right'"
        >
          {{ split.left === t.id ? 'L' : 'R' }}
        </span>
        <button
          type="button"
          class="hover:bg-muted -mr-1 rounded p-0.5 opacity-60 hover:opacity-100"
          title="Close tab"
          @mousedown.stop
          @click="closeTab(t.id)"
        >
          <X class="size-3" />
        </button>
      </div>
      <Button variant="ghost" size="icon-sm" class="mb-0.5 size-7" title="Open another VM" @click="openPicker">
        <Plus />
      </Button>
      <div class="ml-auto mb-0.5 flex items-center">
        <Button
          v-if="split"
          variant="ghost"
          size="sm"
          class="h-7 text-xs"
          title="Show only the focused console"
          @click="unsplit"
        >
          <Square /> Single view
        </Button>
        <span v-else class="text-muted-foreground flex items-center gap-1 pr-1 text-[11px]">
          <Columns2 class="size-3.5" /> Drag a tab to a side to split
        </span>
      </div>
    </div>

    <!-- The consoles. -->
    <div ref="workspace" class="relative flex-1" @dragover="onWorkspaceDragOver" @drop="onWorkspaceDrop">
      <ConsolePane
        v-for="t in tabs"
        :key="t.id"
        :ref="(el) => setPane(t.id, el)"
        v-show="placement(t.id)"
        :vm-id="t.id"
        :protocol="t.protocol"
        class="inset-y-0"
        :class="{ 'ring-primary/70 z-[1] ring-2 ring-inset': split && t.id === focused }"
        :style="placement(t.id) ?? {}"
        @pointerdown.capture="focused !== t.id && focusTab(t.id)"
        @protocol="setProtocol(t.id, $event)"
        @open-panel="panelOpen = true"
      />

      <!-- Divider between split panes. -->
      <div
        v-if="split"
        class="absolute inset-y-0 z-[2] -ml-1 w-2 cursor-col-resize"
        :style="{ left: `${ratio * 100}%` }"
        title="Drag to resize"
        @pointerdown="onDividerDown"
        @pointermove="onDividerMove"
        @pointerup="onDividerUp"
        @pointercancel="onDividerUp"
      >
        <div class="bg-border mx-auto h-full w-px" :class="{ 'bg-primary w-0.5': resizing }" />
      </div>
      <!-- Keeps the consoles from taking the pointer while resizing. -->
      <div v-if="resizing" class="absolute inset-0 z-[1] cursor-col-resize" />

      <!-- Drop zones while dragging a tab. -->
      <div v-if="draggedTab" class="pointer-events-none absolute inset-0 z-[3] flex">
        <div
          v-for="side in (['left', 'right'] as const)"
          :key="side"
          class="m-2 flex flex-1 items-center justify-center rounded-lg border-2 border-dashed text-sm font-medium transition-colors"
          :class="dropSide === side ? 'border-primary bg-primary/20 text-foreground' : 'border-white/30 bg-black/30 text-white/70'"
        >
          Show on the {{ side }}
        </div>
      </div>
    </div>

    <!-- Choosing VMs to open (inside the page, so it works in fullscreen). -->
    <div v-if="picking" class="absolute inset-0 z-30 flex items-center justify-center bg-black/60" @keydown.esc="picking = false">
      <form
        class="bg-background flex max-h-[80vh] w-96 max-w-[calc(100%-2rem)] flex-col gap-3 rounded-lg border p-5 shadow-xl"
        @submit.prevent="openChosen"
      >
        <div>
          <h2 class="text-base font-semibold">Open another VM</h2>
          <p class="text-muted-foreground text-sm">Each opens in a new tab.</p>
        </div>
        <div class="flex min-h-0 flex-col gap-1 overflow-y-auto">
          <label
            v-for="v in openable"
            :key="v.id"
            class="hover:bg-muted flex cursor-pointer items-center gap-3 rounded-md px-2 py-1.5 text-sm"
          >
            <Checkbox
              :model-value="chosen.includes(v.id)"
              @update:model-value="(on) => toggleChosen(v.id, on === true)"
            />
            <span class="inline-block size-2 shrink-0 rounded-full" :class="vmStatus(v).dot" />
            <span class="flex-1 truncate">{{ v.name }}</span>
            <span class="text-muted-foreground text-xs">{{ v.templateName }}</span>
          </label>
          <p v-if="!openable.length" class="text-muted-foreground py-2 text-sm">Every machine is already open.</p>
        </div>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="picking = false">Cancel</Button>
          <Button type="submit" :disabled="!chosen.length">
            Open{{ chosen.length > 1 ? ` ${chosen.length}` : '' }}
          </Button>
        </div>
      </form>
    </div>

    <ConsoleSidebar
      v-if="focusedCtl"
      :key="sidebarKey"
      v-model:open="panelOpen"
      :console="focusedCtl"
      :vm="focusedVM ?? null"
      :fullscreen="isFullscreen"
      @toggle-fullscreen="toggleFullscreen"
      @open-vm="openPicker"
      @upload="panes.get(focused)?.upload()"
    />
  </div>
</template>
