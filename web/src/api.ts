import type { ConsoleProtocol, ConsoleSession } from '@/consoles/types'

export interface VM {
  vmid: number
  name: string
  node: string
  status: string
  template: boolean
  cpu: number
  maxcpu: number
  mem: number
  maxmem: number
  uptime: number
  /** Supported console protocols, preferred first. */
  consoles: ConsoleProtocol[]
  /** False when the guest has no USB tablet, so a VNC mouse is relative. */
  absolutePointer: boolean
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) throw new ApiError(resp.status, data.error ?? resp.statusText)
  return data as T
}

export const listVMs = () => request<VM[]>('GET', '/api/vms')

export const getVM = (node: string, vmid: number) =>
  request<VM>('GET', `/api/vms/${encodeURIComponent(node)}/${vmid}`)

export const createConsole = (node: string, vmid: number, protocol: ConsoleProtocol) =>
  request<ConsoleSession>('POST', `/api/vms/${encodeURIComponent(node)}/${vmid}/console`, { protocol })
