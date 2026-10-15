import { AppWindow, Box, SquareTerminal } from '@lucide/vue'
import type { Component } from 'vue'
import type { VM, VMAction } from '@/api'

export function formatMemory(mib: number): string {
  return mib >= 1024 ? `${+(mib / 1024).toFixed(1)} GiB` : `${mib} MiB`
}

export function formatUptime(seconds: number): string {
  if (!seconds) return ''
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`
}

export function osIcon(os: string): Component {
  return os === 'windows' ? AppWindow : os === 'linux' ? SquareTerminal : Box
}

export const actionLabels: Record<VMAction, string> = {
  snapshot: 'Taking snapshot',
  revert: 'Reverting to snapshot',
  'delete-snapshot': 'Deleting snapshot',
  start: 'Starting',
  shutdown: 'Shutting down',
  stop: 'Stopping',
  reboot: 'Rebooting',
  reset: 'Resetting',
  suspend: 'Suspending',
  resume: 'Resuming',
}

/** A short status for a VM and the colour of its status dot. */
export function vmStatus(vm: VM): { label: string; dot: string } {
  switch (vm.state) {
    case 'deploying':
      return { label: 'Deploying', dot: 'bg-amber-500 animate-pulse' }
    case 'destroying':
      return { label: 'Destroying', dot: 'bg-amber-500 animate-pulse' }
    case 'failed':
      return { label: 'Failed', dot: 'bg-destructive' }
  }
  if (vm.action) return { label: actionLabels[vm.action], dot: 'bg-amber-500 animate-pulse' }
  switch (vm.power) {
    case 'running':
      return vm.booting
        ? { label: 'Booting', dot: 'bg-emerald-500 animate-pulse' }
        : { label: 'Running', dot: 'bg-emerald-500' }
    case 'stopped':
      return { label: 'Stopped', dot: 'bg-muted-foreground' }
    case 'paused':
      return { label: 'Paused', dot: 'bg-sky-500' }
    case 'suspended':
      return { label: 'Suspended', dot: 'bg-sky-500' }
    default:
      return { label: 'Unknown', dot: 'bg-muted-foreground' }
  }
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

export function formatDateTime(iso: string | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return isNaN(d.getTime()) || d.getFullYear() < 1980 ? '' : d.toLocaleString()
}

/** Formats CPU milliseconds as h:mm:ss. */
export function formatCpuTime(ms: number): string {
  const s = Math.floor(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return `${h}:${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
}

/** A duration in milliseconds as "2d 4h", "3h 12m", "12m" or "under a minute". */
export function formatRemaining(ms: number): string {
  const m = Math.floor(ms / 60_000)
  if (m < 1) return 'under a minute'
  const d = Math.floor(m / 1440)
  const h = Math.floor((m % 1440) / 60)
  if (d) return h ? `${d}d ${h}h` : `${d}d`
  if (h) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`
  return `${m}m`
}

export interface ExpiryState {
  /** Milliseconds left (0 once due). */
  remaining: number
  /** Within the warning window before expiring. */
  soon: boolean
  /** The owner may still extend it (administrators always can). */
  canExtend: boolean
}

/** Where a VM is relative to its expiry at now (ms); null if it never expires. */
export function expiryState(vm: VM, now: number): ExpiryState | null {
  if (!vm.expiresAt || vm.state === 'destroying') return null
  const remaining = Math.max(0, new Date(vm.expiresAt).getTime() - now)
  return {
    remaining,
    soon: remaining <= (vm.expiryWarningSeconds ?? 86_400) * 1000,
    canExtend: vm.extendsLeft !== 0,
  }
}

/** How long ago a time was, roughly: "just now", "5 minutes ago", "2 days ago". */
export function formatAgo(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000))
  const unit = (n: number, u: string) => `${n} ${u}${n === 1 ? '' : 's'} ago`
  if (s < 60) return 'just now'
  if (s < 3600) return unit(Math.floor(s / 60), 'minute')
  if (s < 86400) return unit(Math.floor(s / 3600), 'hour')
  return unit(Math.floor(s / 86400), 'day')
}
