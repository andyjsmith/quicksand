import type { ConsoleProtocol, ConsoleSession } from '@/consoles/types'

export interface User {
  id: number
  username: string
  isAdmin: boolean
  createdAt: string
}

export interface Template {
  id: string
  name: string
  description: string
  /** Icon hint: windows, linux or other. */
  os: string
  cores: number
  memoryMiB: number
  maxCores: number
  maxMemoryMiB: number
  /** Set when the template can't be read from Proxmox. */
  error?: string
}

export interface Network {
  id: string
  label: string
  default: boolean
}

export type VMState = 'deploying' | 'ready' | 'failed' | 'destroying'
export type PowerState = 'running' | 'stopped' | 'paused' | 'suspended' | 'unknown'
export type PowerAction = 'start' | 'shutdown' | 'stop' | 'reboot' | 'reset' | 'suspend' | 'resume'

export interface VM {
  id: string
  name: string
  templateId: string
  templateName: string
  os: string
  vmid: number
  cores: number
  memoryMiB: number
  networkLabel: string
  state: VMState
  step?: string
  /** 0..1 while deploying or destroying. */
  progress: number
  error?: string
  power: PowerState
  /** The guest OS is starting up (until its guest agent answers). */
  booting: boolean
  uptime: number
  cpu: number
  mem: number
  maxmem: number
  /** A power action in progress, and the last one's error. */
  action?: PowerAction
  actionError?: string
  /** Supported console protocols, preferred first. */
  consoles: ConsoleProtocol[]
  /** False when the guest has no USB tablet, so a VNC mouse is relative. */
  absolutePointer: boolean
  /** Set when an administrator lists everyone's VMs. */
  owner?: string
  createdAt: string
}

export interface DeployRequest {
  templateId: string
  name: string
  cores: number
  memoryMiB: number
  networkId: string
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

/** Called when a request finds the session has ended (set by the auth composable). */
let onUnauthorized: (() => void) | undefined
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await resp.text()
  let data: { error?: string } = {}
  try {
    data = text ? JSON.parse(text) : {}
  } catch {
    // Not JSON, e.g. a proxy error page.
  }
  if (!resp.ok) {
    if (resp.status === 401 && path !== '/api/auth/login' && path !== '/api/auth/me') onUnauthorized?.()
    throw new ApiError(resp.status, data.error ?? resp.statusText)
  }
  return (text ? data : undefined) as T
}

const vmPath = (id: string) => `/api/vms/${encodeURIComponent(id)}`

export const api = {
  login: (username: string, password: string) => request<User>('POST', '/api/auth/login', { username, password }),
  logout: () => request<void>('POST', '/api/auth/logout'),
  me: () => request<User>('GET', '/api/auth/me'),
  changePassword: (current: string, next: string) =>
    request<void>('POST', '/api/auth/password', { current, new: next }),

  templates: () => request<Template[]>('GET', '/api/templates'),
  networks: () => request<Network[]>('GET', '/api/networks'),

  vms: (all = false) => request<VM[]>('GET', all ? '/api/vms?all=1' : '/api/vms'),
  vm: (id: string) => request<VM>('GET', vmPath(id)),
  deploy: (req: DeployRequest) => request<VM>('POST', '/api/vms', req),
  destroy: (id: string) => request<void>('DELETE', vmPath(id)),
  power: (id: string, action: PowerAction) => request<void>('POST', `${vmPath(id)}/power`, { action }),
  createConsole: (id: string, protocol: ConsoleProtocol) =>
    request<ConsoleSession>('POST', `${vmPath(id)}/console`, { protocol }),

  users: () => request<User[]>('GET', '/api/admin/users'),
  createUser: (username: string, password: string, isAdmin: boolean) =>
    request<User>('POST', '/api/admin/users', { username, password, isAdmin }),
  updateUser: (id: number, changes: { password?: string; isAdmin?: boolean }) =>
    request<User>('PATCH', `/api/admin/users/${id}`, changes),
  deleteUser: (id: number) => request<void>('DELETE', `/api/admin/users/${id}`),
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
