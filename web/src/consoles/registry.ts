import type { ConsoleDriver, ConsoleProtocol } from './types'

export interface ConsoleDriverInfo {
  label: string
  description: string
  /** Drivers are loaded on demand so each client library is its own chunk. */
  create: () => Promise<ConsoleDriver>
}

export const consoleDrivers: Record<ConsoleProtocol, ConsoleDriverInfo> = {
  spice: {
    label: 'SPICE',
    description: 'Guest resizing, clipboard sync and audio (needs the SPICE agent in the guest)',
    create: async () => new (await import('./spice/SpiceDriver')).SpiceDriver(),
  },
  vnc: {
    label: 'VNC',
    description: 'Works with any display adapter; no clipboard or audio',
    create: async () => new (await import('./vnc/VncDriver')).VncDriver(),
  },
}
