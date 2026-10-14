<script setup lang="ts">
import { useIntervalFn, useTitle } from '@vueuse/core'
import {
  Check,
  CircleAlert,
  Copy,
  EllipsisVertical,
  KeyRound,
  Monitor,
  Shield,
  ShieldOff,
  SquareTerminal,
  Trash2,
  UserPlus,
  Users,
} from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { api, errorMessage, type AgentSettings, type User, type VM } from '@/api'
import AppHeader from '@/components/AppHeader.vue'
import ExpiryBadge from '@/components/ExpiryBadge.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useAuth } from '@/composables/useAuth'
import { formatMemory, vmStatus } from '@/lib/format'

useTitle('Admin · Quicksand')

const auth = useAuth()
const users = ref<User[]>([])
const vms = ref<VM[]>([])
const error = ref<string>()

async function loadUsers() {
  try {
    users.value = await api.users()
  } catch (e) {
    error.value = errorMessage(e)
  }
}
async function loadVMs() {
  try {
    vms.value = await api.vms(true)
  } catch (e) {
    error.value = errorMessage(e)
  }
}
const agent = ref<AgentSettings | null>(null)
onMounted(() =>
  Promise.all([
    loadUsers(),
    loadVMs(),
    api.agentSettings().then((a) => (agent.value = a), () => {}),
  ]),
)

const copied = ref(false)
async function copyKey() {
  if (!agent.value?.publicKey) return
  await navigator.clipboard.writeText(agent.value.publicKey)
  copied.value = true
  setTimeout(() => (copied.value = false), 1500)
}
useIntervalFn(loadVMs, 5000)

// A single dialog serves "add user" (target null) and "reset password".
const dialogOpen = ref(false)
const target = ref<User | null>(null)
const form = ref({ username: '', password: '', isAdmin: false })
const dialogError = ref<string>()
const busy = ref(false)

function openAdd() {
  target.value = null
  form.value = { username: '', password: '', isAdmin: false }
  dialogError.value = undefined
  dialogOpen.value = true
}
function openReset(u: User) {
  target.value = u
  form.value = { username: u.username, password: '', isAdmin: u.isAdmin }
  dialogError.value = undefined
  dialogOpen.value = true
}
async function submitDialog() {
  busy.value = true
  dialogError.value = undefined
  try {
    if (target.value) await api.updateUser(target.value.id, { password: form.value.password })
    else await api.createUser(form.value.username, form.value.password, form.value.isAdmin)
    dialogOpen.value = false
    await loadUsers()
  } catch (e) {
    dialogError.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}

async function setAdmin(u: User, isAdmin: boolean) {
  error.value = undefined
  try {
    await api.updateUser(u.id, { isAdmin })
    await loadUsers()
  } catch (e) {
    error.value = errorMessage(e)
  }
}

const deleting = ref<User | null>(null)
async function deleteUser() {
  if (!deleting.value) return
  error.value = undefined
  try {
    await api.deleteUser(deleting.value.id)
    await loadUsers()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    deleting.value = null
  }
}

// Administrators can destroy anyone's machine.
const destroying = ref<VM | null>(null)
async function destroyVM() {
  const vm = destroying.value
  if (!vm) return
  error.value = undefined
  try {
    await api.destroy(vm.id)
    await loadVMs()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    destroying.value = null
  }
}

const vmCount = (u: User) => vms.value.filter((vm) => vm.owner === u.username).length
const formatDate = (iso: string) => new Date(iso).toLocaleDateString()
</script>

<template>
  <div class="bg-background text-foreground min-h-dvh">
    <AppHeader />
    <main class="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-6">
      <Alert v-if="error" variant="destructive">
        <CircleAlert />
        <AlertDescription>{{ error }}</AlertDescription>
      </Alert>

      <Card>
        <CardHeader class="flex flex-row items-start justify-between gap-4">
          <div class="flex flex-col gap-1">
            <CardTitle class="flex items-center gap-2"><Users class="size-4" /> Users</CardTitle>
            <CardDescription>Users only see the machines they deploy.</CardDescription>
          </div>
          <Button @click="openAdd"><UserPlus /> Add user</Button>
        </CardHeader>
        <CardContent>
          <div class="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Username</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Machines</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead class="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="u in users" :key="u.id">
                  <TableCell class="font-medium">
                    {{ u.username }}
                    <span v-if="u.id === auth.user.value?.id" class="text-muted-foreground font-normal">(you)</span>
                  </TableCell>
                  <TableCell>
                    <Badge v-if="u.isAdmin" variant="secondary"><Shield /> Admin</Badge>
                    <span v-else class="text-muted-foreground">User</span>
                  </TableCell>
                  <TableCell class="tabular-nums">{{ vmCount(u) }}</TableCell>
                  <TableCell>{{ formatDate(u.createdAt) }}</TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger as-child>
                        <Button variant="ghost" size="icon-sm" title="Actions"><EllipsisVertical /></Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" class="w-48">
                        <DropdownMenuItem @select="openReset(u)"><KeyRound /> Reset password</DropdownMenuItem>
                        <DropdownMenuItem v-if="!u.isAdmin" @select="setAdmin(u, true)">
                          <Shield /> Make administrator
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          v-else
                          :disabled="u.id === auth.user.value?.id"
                          @select="setAdmin(u, false)"
                        >
                          <ShieldOff /> Remove administrator
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          variant="destructive"
                          :disabled="u.id === auth.user.value?.id"
                          @select="deleting = u"
                        >
                          <Trash2 /> Delete user
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      <Card v-if="agent?.enabled">
        <CardHeader>
          <CardTitle class="flex items-center gap-2"><SquareTerminal class="size-4" /> Agent</CardTitle>
          <CardDescription>
            Templates with the Quicksand agent must trust this server key. Pass it to the agent installer
            (see docs/agent.md).
          </CardDescription>
        </CardHeader>
        <CardContent class="flex flex-col gap-3">
          <div class="flex items-center gap-2">
            <code class="bg-muted min-w-0 flex-1 truncate rounded-md px-2 py-1.5 text-xs">{{ agent.publicKey }}</code>
            <Button variant="outline" size="sm" @click="copyKey">
              <component :is="copied ? Check : Copy" /> {{ copied ? 'Copied' : 'Copy' }}
            </Button>
          </div>
          <p class="text-muted-foreground text-xs">
            Agent network {{ agent.subnet }} · port {{ agent.port }} · MAC prefix {{ agent.macPrefix }}
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="flex items-center gap-2"><Monitor class="size-4" /> All machines</CardTitle>
          <CardDescription>Every deployed machine, across all users.</CardDescription>
        </CardHeader>
        <CardContent>
          <div class="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Owner</TableHead>
                  <TableHead>Template</TableHead>
                  <TableHead>VM ID</TableHead>
                  <TableHead>Resources</TableHead>
                  <TableHead>Agent</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead class="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="vm in vms" :key="vm.id">
                  <TableCell class="font-medium">{{ vm.name }}</TableCell>
                  <TableCell>{{ vm.owner }}</TableCell>
                  <TableCell>{{ vm.templateName }}</TableCell>
                  <TableCell class="font-mono">{{ vm.vmid }}</TableCell>
                  <TableCell>{{ vm.cores }} CPUs · {{ formatMemory(vm.memoryMiB) }} · {{ vm.networkLabel }}</TableCell>
                  <TableCell class="font-mono text-xs">
                    <template v-if="vm.agent">{{ vm.agent.ip }} ({{ vm.agent.status }})</template>
                    <span v-else class="text-muted-foreground">—</span>
                  </TableCell>
                  <TableCell>
                    <span class="inline-flex items-center gap-1.5">
                      <span class="inline-block size-2 rounded-full" :class="vmStatus(vm).dot" />
                      {{ vmStatus(vm).label }}
                    </span>
                  </TableCell>
                  <TableCell>
                    <ExpiryBadge v-if="vm.expiresAt" :vm="vm" extendable @error="error = $event" />
                    <span v-else class="text-muted-foreground">—</span>
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      :title="`Destroy ${vm.name}`"
                      :disabled="vm.state === 'destroying'"
                      @click="destroying = vm"
                    >
                      <Trash2 />
                    </Button>
                  </TableCell>
                </TableRow>
                <TableEmpty v-if="!vms.length" :colspan="9">No machines are deployed.</TableEmpty>
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
    </main>

    <Dialog v-model:open="dialogOpen">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{{ target ? `Reset password for ${target.username}` : 'Add user' }}</DialogTitle>
          <DialogDescription>
            {{ target ? 'They are signed out everywhere.' : 'Share the password with them; they can change it later.' }}
          </DialogDescription>
        </DialogHeader>
        <form id="user-form" class="flex flex-col gap-3" @submit.prevent="submitDialog">
          <Alert v-if="dialogError" variant="destructive">
            <CircleAlert />
            <AlertDescription>{{ dialogError }}</AlertDescription>
          </Alert>
          <div v-if="!target" class="flex flex-col gap-1.5">
            <Label for="user-name">Username</Label>
            <Input id="user-name" v-model="form.username" autocomplete="off" required />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="user-password">{{ target ? 'New password' : 'Password' }}</Label>
            <Input id="user-password" v-model="form.password" type="password" autocomplete="new-password" minlength="8" required />
          </div>
          <div v-if="!target" class="flex items-center justify-between">
            <Label for="user-admin">Administrator</Label>
            <Switch id="user-admin" v-model="form.isAdmin" />
          </div>
        </form>
        <DialogFooter>
          <Button variant="outline" @click="dialogOpen = false">Cancel</Button>
          <Button type="submit" form="user-form" :disabled="busy">{{ target ? 'Reset password' : 'Add user' }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <AlertDialog :open="!!destroying" @update:open="(o) => !o && (destroying = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Destroy {{ destroying?.name }}?</AlertDialogTitle>
          <AlertDialogDescription>
            {{ destroying?.owner }}'s machine (VM {{ destroying?.vmid }}) and its disks are deleted. This can't be
            undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" @click="destroyVM">Destroy</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <AlertDialog :open="!!deleting" @update:open="(o) => !o && (deleting = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {{ deleting?.username }}?</AlertDialogTitle>
          <AlertDialogDescription>
            Their account is removed. Users who still own machines can't be deleted until those are destroyed.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" @click="deleteUser">Delete</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
