import { AppWindow, Box, SquareTerminal } from '@lucide/vue'
import type { Component } from 'vue'
import type { PowerAction, VM } from '@/api'

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

export const actionLabels: Record<PowerAction, string> = {
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
