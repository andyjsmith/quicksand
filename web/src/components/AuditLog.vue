<script setup lang="ts">
import { watchDebounced } from '@vueuse/core'
import { CircleAlert, ScrollText, Search } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { api, errorMessage, type AuditEntry } from '@/api'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

// What each action reads as in the log.
const actionLabels: Record<string, string> = {
  login: 'Signed in',
  'login.failed': 'Failed sign-in',
  logout: 'Signed out',
  'password.change': 'Changed password',
  'token.create': 'Created API token',
  'token.revoke': 'Revoked API token',
  'vm.deploy': 'Deployed',
  'vm.destroy': 'Destroyed',
  'vm.power': 'Power',
  'vm.console': 'Opened console',
  'vm.serial': 'Opened serial console',
  'vm.extend': 'Extended',
  'vm.snapshot': 'Took snapshot',
  'vm.snapshot.revert': 'Reverted to snapshot',
  'vm.snapshot.delete': 'Deleted snapshot',
  'vm.firewall': 'Changed firewall',
  'vm.link': 'Linked',
  'vm.unlink': 'Unlinked',
  'link.rename': 'Renamed link network',
  'agent.exec': 'Ran command',
  'agent.shell': 'Opened shell',
  'agent.forward': 'Forwarded port',
  'agent.kill': 'Killed process',
  'agent.file.upload': 'Uploaded',
  'agent.file.download': 'Downloaded',
  'agent.file.delete': 'Deleted file',
  'agent.file.mkdir': 'Made folder',
  'user.create': 'Added user',
  'user.update': 'Changed user',
  'user.delete': 'Deleted user',
}

const entries = ref<AuditEntry[]>([])
const more = ref(false)
const query = ref('')
const loading = ref(false)
const error = ref<string>()

// load fetches the newest entries, or with append, the next older page.
async function load(append = false) {
  loading.value = true
  error.value = undefined
  try {
    const before = append ? (entries.value.at(-1)?.id ?? 0) : 0
    const page = await api.audit(before, query.value.trim())
    entries.value = append ? [...entries.value, ...page.entries] : page.entries
    more.value = page.more
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => load())
watchDebounced(query, () => load(), { debounce: 300 })

const formatTime = (iso: string) =>
  new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <Card>
    <CardHeader class="flex flex-row items-start justify-between gap-4">
      <div class="flex flex-col gap-1">
        <CardTitle class="flex items-center gap-2"><ScrollText class="size-4" /> Audit log</CardTitle>
        <CardDescription>Sign-ins and what users did to machines and accounts, newest first.</CardDescription>
      </div>
      <div class="relative w-full max-w-60">
        <Search class="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
        <Input v-model="query" placeholder="Filter" class="pl-8" aria-label="Filter the audit log" />
      </div>
    </CardHeader>
    <CardContent class="flex flex-col gap-3">
      <Alert v-if="error" variant="destructive">
        <CircleAlert />
        <AlertDescription>{{ error }}</AlertDescription>
      </Alert>
      <div class="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>User</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Target</TableHead>
              <TableHead>Details</TableHead>
              <TableHead>Address</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="e in entries" :key="e.id">
              <TableCell class="whitespace-nowrap tabular-nums">{{ formatTime(e.at) }}</TableCell>
              <TableCell class="font-medium">{{ e.username }}</TableCell>
              <TableCell :class="{ 'text-destructive': e.action === 'login.failed' }" :title="e.action">
                {{ actionLabels[e.action] ?? e.action }}
              </TableCell>
              <TableCell>{{ e.target }}</TableCell>
              <TableCell class="max-w-md truncate font-mono text-xs" :title="e.detail">{{ e.detail }}</TableCell>
              <TableCell class="text-muted-foreground font-mono text-xs">{{ e.ip }}</TableCell>
            </TableRow>
            <TableEmpty v-if="!entries.length && !loading" :colspan="6">
              {{ query ? 'Nothing matches.' : 'Nothing has been logged yet.' }}
            </TableEmpty>
          </TableBody>
        </Table>
      </div>
      <div v-if="more || loading" class="flex justify-center">
        <Button variant="outline" size="sm" :disabled="loading" @click="load(true)">
          <Spinner v-if="loading" /> Load more
        </Button>
      </div>
    </CardContent>
  </Card>
</template>
