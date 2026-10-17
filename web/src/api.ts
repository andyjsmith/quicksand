import type { VMFirewall } from '@/lib/firewall'
import type { ConsoleProtocol, ConsoleSession } from '@/consoles/types'

export interface User {
  id: number
  username: string
  isAdmin: boolean
  createdAt: string
  /** Signs in with single sign-on. */
  sso: boolean
  /** Unset for single sign-on users without a password. */
  hasPassword: boolean
}

/** An API token; its secret is only shown when it's created. */
export interface ApiToken {
  id: number
  name: string
  createdAt: string
  /** Zero time until first used. */
  lastUsedAt: string
  /** Zero time for tokens that expire only when unused for 90 days. */
  expiresAt: string
}

export interface AuditEntry {
  id: number
  at: string
  userId?: number
  username: string
  action: string
  target: string
  detail: string
  ip: string
}

/** How users can sign in. */
export interface AuthMethods {
  password: boolean
  sso: {
    /** Label of the sign-in button. */
    name: string
    /** Single sign-on users' administrator role follows their groups at the provider. */
    adminFromGroups: boolean
  } | null
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
  /** Deployments can have the Quicksand agent. */
  agent: boolean
  /** The app the agent launches in deployments, if any. */
  launch?: string[]
  /** Deployments can be given a URL to open in that app. */
  opensURLs: boolean
  /** Set when the template can't be read from Proxmox. */
  error?: string
  /** Why the user can't deploy it right now (a quota is used up). */
  quotaBlocked?: string
}

export interface Network {
  id: string
  label: string
  default: boolean
}

export type VMState = 'deploying' | 'ready' | 'failed' | 'destroying'
/** hibernated: stopped with its memory saved to disk, which starting it resumes from. */
export type PowerState = 'running' | 'stopped' | 'paused' | 'suspended' | 'hibernated' | 'unknown'
export type PowerAction = 'start' | 'shutdown' | 'stop' | 'reboot' | 'reset' | 'suspend' | 'resume' | 'hibernate'
/** What a VM is busy doing: a power action, or one on its snapshot. */
export type VMAction = PowerAction | 'snapshot' | 'revert' | 'delete-snapshot'

/** A VM's place on a link network (a private network VMs are joined into). */
export interface VMLink {
  network: number
  name: string
  /** The VM's address with its prefix length, e.g. "10.98.0.5/16". */
  address: string
  /** The link NIC's MAC, to find it in the guest. */
  mac: string
  /** configured: the agent set the address; pending: being set up; manual: the user must set it. */
  status: 'configured' | 'pending' | 'manual'
  error?: string
}

export interface LinksInfo {
  enabled: boolean
  subnet?: string
}

/** A step of a running action; done and total are bytes, when known. */
export interface ActionProgress {
  step: string
  done?: number
  total?: number
}

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
  action?: VMAction
  actionError?: string
  /** How far along the action is, for those that say (snapshots). */
  actionProgress?: ActionProgress
  /** The link network the VM is in, if any. */
  link?: VMLink
  /** Supported console protocols, preferred first. */
  consoles: ConsoleProtocol[]
  /** Shells can use the VM's serial console. */
  serial: boolean
  /** The VM's snapshot (each VM has at most one). */
  snapshot?: {
    takenAt: string
    /** It includes the VM's memory, so reverting resumes it as it was. */
    memory: boolean
  }
  /** When the VM is destroyed unless extended; unset if it never expires. */
  expiresAt?: string
  /** How many more times the owner may extend it; unset means no limit. */
  extendsLeft?: number
  /** How much each extension adds. */
  extendSeconds?: number
  /** How long before expiring the VM should be flagged as about to. */
  expiryWarningSeconds?: number
  /** When the running VM is hibernated if nobody uses it before then; unset while it's in use or if VMs aren't suspended. */
  suspendAt?: string
  /** How long before being hibernated the VM should be flagged as about to. */
  suspendWarningSeconds?: number
  /** False when the guest has no USB tablet, so a VNC mouse is relative. */
  absolutePointer: boolean
  /** Set for VMs with a Quicksand agent. */
  agent?: AgentStatus
  /** The app the agent launches, for templates with one. */
  launch?: VMLaunch
  /** Set when an administrator lists everyone's VMs. */
  owner?: string
  createdAt: string
}

export interface VMLaunch {
  command: string[]
  opensURLs: boolean
  /** As far as the agent last said. */
  running: boolean
  /** Why it isn't running, or why the agent can't launch it. */
  error?: string
}

export interface AgentStatus {
  ip: string
  status: 'connected' | 'unreachable'
  hostname?: string
  os?: string
  version?: string
  error?: string
}

export interface ExecRequest {
  shell?: string
  argv?: string[]
  timeoutSeconds?: number
}

export interface ExecResult {
  /** -1 when the command couldn't be started or was killed. */
  exitCode: number
  /** Base64 (JSON-encoded bytes). */
  stdout: string | null
  stderr: string | null
  truncated?: boolean
  timedOut?: boolean
  error?: string
  durationMs: number
}

/** What agent configs in templates must contain (admins only). */
export interface AgentSettings {
  enabled: boolean
  publicKey?: string
  port?: number
  subnet?: string
  macPrefix?: string
}

export interface DeployRequest {
  templateId: string
  name: string
  cores: number
  memoryMiB: number
  networkId: string
  /** For templates with the agent: false deploys without the agent NIC. */
  agent?: boolean
  /** For templates that open links: a URL to open in the VM's app. */
  url?: string
}

export interface ProcessList {
  processes: GuestProcess[]
  time: string
  numCPU: number
  memoryTotal: number
}

export interface GuestProcess {
  pid: number
  ppid: number
  name: string
  user?: string
  /** Command line (Linux) or executable path (Windows). */
  command?: string
  /** Resident set / working set in bytes. */
  memory: number
  cpuTimeMs: number
  threads?: number
  started?: string
  /** Windows services it hosts (e.g. in svchost.exe), or its systemd unit. */
  services?: { name: string; displayName?: string }[]
}

export interface ConnectionList {
  connections: GuestConnection[]
}

export interface GuestConnection {
  proto: 'tcp' | 'tcp6' | 'udp' | 'udp6'
  local: string
  remote?: string
  state?: string
  pid?: number
  processName?: string
}

export interface FileList {
  /** The listed directory; "/" on Windows is the list of drives. */
  path: string
  /** The directory above, empty at the top. */
  parent: string
  entries: FileEntry[]
}

export interface FileEntry {
  name: string
  path: string
  dir: boolean
  symlink?: boolean
  size: number
  modTime?: string
  mode?: string
}

export interface DisplayResult {
  width: number
  height: number
  /** "virtio-gpu", "qxl", or another driver's name. */
  adapter: string
  /** False when the closest mode the driver offers was used instead. */
  exact: boolean
}

export interface FileHashes {
  path: string
  size: number
  md5: string
  sha1: string
  sha256: string
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

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
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
  /** Signs out; for single sign-on, returns where to sign out at the provider too. */
  logout: () => request<{ redirect: string } | undefined>('POST', '/api/auth/logout'),
  authMethods: () => request<AuthMethods>('GET', '/api/auth/methods'),
  version: () => request<{ version: string }>('GET', '/api/version'),
  tokens: () => request<ApiToken[]>('GET', '/api/tokens'),
  /** Makes a token; expiresInDays 0 is none (only unused tokens expire). */
  createToken: (name: string, expiresInDays: number) =>
    request<{ token: ApiToken; secret: string }>('POST', '/api/tokens', { name, expiresInDays }),
  deleteToken: (id: number) => request<void>('DELETE', `/api/tokens/${id}`),
  me: () => request<User>('GET', '/api/auth/me'),
  changePassword: (current: string, next: string) =>
    request<void>('POST', '/api/auth/password', { current, new: next }),

  templates: () => request<Template[]>('GET', '/api/templates'),
  networks: () => request<Network[]>('GET', '/api/networks'),

  vms: (all = false) => request<VM[]>('GET', all ? '/api/vms?all=1' : '/api/vms'),
  vm: (id: string) => request<VM>('GET', vmPath(id)),
  deploy: (req: DeployRequest) => request<VM>('POST', '/api/vms', req),
  destroy: (id: string) => request<void>('DELETE', vmPath(id)),
  /** Snapshots a VM (with its memory unless told not to), replacing its snapshot. */
  takeSnapshot: (id: string, memory: boolean) => request<void>('POST', `${vmPath(id)}/snapshot`, { memory }),
  /** Returns a VM to its snapshot, which it keeps; with start, a snapshot without memory boots the VM too. */
  revertSnapshot: (id: string, start = false) => request<void>('POST', `${vmPath(id)}/snapshot/revert`, { start }),
  deleteSnapshot: (id: string) => request<void>('DELETE', `${vmPath(id)}/snapshot`),
  /**
   * The guest clipboard through the agent; when its seq is `after`, waits
   * up to `wait` seconds for a change first.
   */
  clipboard: (id: string, after: number, wait: number, signal?: AbortSignal) =>
    request<{ seq: number; text: string }>('GET', `${vmPath(id)}/clipboard?after=${after}&wait=${wait}`, undefined, signal),
  /** Puts text on the guest clipboard through the agent. */
  setClipboard: (id: string, text: string) => request<void>('PUT', `${vmPath(id)}/clipboard`, { text }),
  firewall: (id: string) => request<VMFirewall>('GET', `${vmPath(id)}/firewall`),
  /** Saves and applies; a warning means it was saved but not (yet) applied. */
  setFirewall: (id: string, fw: VMFirewall) =>
    request<{ firewall: VMFirewall; warning?: string }>('PUT', `${vmPath(id)}/firewall`, fw),
  links: () => request<LinksInfo>('GET', '/api/links'),
  /** Joins a VM and others into one link network; merge is needed when they're in different ones. */
  link: (id: string, withIds: string[], merge = false) =>
    request<{ network: { id: number; name: string }; warning?: string }>('POST', `${vmPath(id)}/link`, {
      with: withIds,
      merge,
    }),
  unlink: (id: string) => request<void>('POST', `${vmPath(id)}/unlink`),
  renameLink: (id: number, name: string) => request<void>('PATCH', `/api/links/${id}`, { name }),
  /** Pushes back a VM's expiry by the configured extension. */
  extend: (id: string) => request<VM>('POST', `${vmPath(id)}/extend`),
  relaunch: (id: string) => request<void>('POST', `${vmPath(id)}/relaunch`),
  power: (id: string, action: PowerAction) => request<void>('POST', `${vmPath(id)}/power`, { action }),
  exec: (id: string, req: ExecRequest) => request<ExecResult>('POST', `${vmPath(id)}/exec`, req),
  createConsole: (id: string, protocol: ConsoleProtocol) =>
    request<ConsoleSession>('POST', `${vmPath(id)}/console`, { protocol }),

  processes: (id: string) => request<ProcessList>('GET', `${vmPath(id)}/processes`),
  kill: (id: string, pid: number, force: boolean) =>
    request<void>('POST', `${vmPath(id)}/processes/${pid}/kill`, { force }),
  /**
   * Changes the guest's screen resolution through its agent (Windows guests),
   * exactly where the display driver allows, else to the closest mode.
   */
  setDisplay: (id: string, width: number, height: number) =>
    request<DisplayResult>('POST', `${vmPath(id)}/display`, { width, height }),
  connections: (id: string) => request<ConnectionList>('GET', `${vmPath(id)}/connections`),
  files: (id: string, path = '') => request<FileList>('GET', `${vmPath(id)}/files?${new URLSearchParams({ path })}`),
  mkdir: (id: string, path: string) =>
    request<FileEntry>('POST', `${vmPath(id)}/files/mkdir?${new URLSearchParams({ path })}`),
  deleteFile: (id: string, path: string, recursive: boolean) =>
    request<void>('DELETE', `${vmPath(id)}/files?${new URLSearchParams({ path, recursive: recursive ? '1' : '0' })}`),
  hashFile: (id: string, path: string) =>
    request<FileHashes>('GET', `${vmPath(id)}/files/hash?${new URLSearchParams({ path })}`),
  /**
   * The websocket URL of a terminal shell: the agent's (shell, or by default
   * PowerShell on Windows and a login shell elsewhere) or the serial console's.
   */
  shellUrl: (id: string, source: 'agent' | 'serial', cols: number, rows: number, shell = '') => {
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const q = new URLSearchParams({ pty: '1', cols: String(cols), rows: String(rows), term: 'xterm-256color' })
    // The shell program for agent shells; empty means the guest's default.
    if (shell && source === 'agent') q.set('shell', shell)
    return `${scheme}//${location.host}${vmPath(id)}/${source === 'serial' ? 'serial' : 'shell'}?${q}`
  },
  /** A URL that downloads a guest file (or a directory as a zip). */
  downloadUrl: (id: string, path: string) => `${vmPath(id)}/files/content?${new URLSearchParams({ path })}`,
  /**
   * Prepares downloading a file or folder as a password-protected
   * (ZipCrypto) zip, returning a one-time link that works for a minute.
   */
  prepareZip: (id: string, path: string, password: string) =>
    request<{ url: string }>('POST', `${vmPath(id)}/files/zip`, { path, password }),
  upload: (
    id: string,
    path: string,
    file: Blob,
    overwrite: boolean,
    onProgress?: (fraction: number) => void,
    signal?: AbortSignal,
  ) =>
    upload(
      `${vmPath(id)}/files/content?${new URLSearchParams({ path, overwrite: overwrite ? '1' : '0' })}`,
      file,
      onProgress,
      signal,
    ),

  agentSettings: () => request<AgentSettings>('GET', '/api/admin/agent'),
  /** A page of the audit log, newest first: entries older than before (an ID) containing q. */
  audit: (before: number, q: string) =>
    request<{ entries: AuditEntry[]; more: boolean }>(
      'GET',
      `/api/admin/audit?${new URLSearchParams({ before: String(before), q })}`,
    ),
  users: () => request<User[]>('GET', '/api/admin/users'),
  createUser: (username: string, password: string, isAdmin: boolean) =>
    request<User>('POST', '/api/admin/users', { username, password, isAdmin }),
  updateUser: (id: number, changes: { password?: string; isAdmin?: boolean }) =>
    request<User>('PATCH', `/api/admin/users/${id}`, changes),
  deleteUser: (id: number) => request<void>('DELETE', `/api/admin/users/${id}`),
}

/**
 * PUTs a file with upload progress, which fetch can't report. Aborting
 * signal cancels it; the guest keeps nothing of a cancelled upload.
 */
function upload(url: string, file: Blob, onProgress?: (fraction: number) => void, signal?: AbortSignal): Promise<FileEntry> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    if (signal?.aborted) return reject(new ApiError(0, 'Upload cancelled.'))
    signal?.addEventListener('abort', () => xhr.abort(), { once: true })
    xhr.open('PUT', url)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded / e.total)
    }
    xhr.onload = () => {
      let data: { error?: string } = {}
      try {
        data = JSON.parse(xhr.responseText)
      } catch {
        // Not JSON.
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as FileEntry)
      else {
        if (xhr.status === 401) onUnauthorized?.()
        reject(new ApiError(xhr.status, data.error ?? xhr.statusText))
      }
    }
    xhr.onerror = () => reject(new ApiError(0, 'The upload failed (network error).'))
    xhr.onabort = () => reject(new ApiError(0, 'Upload cancelled.'))
    xhr.send(file)
  })
}

/** Decodes base64 command output as UTF-8 (invalid bytes become U+FFFD). */
export function decodeOutput(b64: string | null): string {
  if (!b64) return ''
  const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
