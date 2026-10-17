<script setup lang="ts">
import { useClipboard, useTitle } from '@vueuse/core'
import { Check, CircleAlert, Copy, KeyRound, Plus, Trash2 } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { api, errorMessage, type ApiToken } from '@/api'
import AppHeader from '@/components/AppHeader.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

useTitle('API tokens · Quicksand')

const tokens = ref<ApiToken[]>([])
const error = ref<string>()

async function load() {
  try {
    tokens.value = await api.tokens()
  } catch (e) {
    error.value = errorMessage(e)
  }
}
onMounted(load)

// The Go zero time stands for "not set".
const isSet = (iso: string) => !iso.startsWith('0001-')
const formatDate = (iso: string) => new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' })

// Creating a token: a form, then its secret, shown once.
const dialogOpen = ref(false)
const name = ref('')
const expiry = ref('0')
const secret = ref('')
const dialogError = ref<string>()
const busy = ref(false)
const { copy, copied } = useClipboard({ legacy: true })
const loginCommand = computed(() => `qs login --token ${location.origin}`)

function openCreate() {
  name.value = ''
  expiry.value = '0'
  secret.value = ''
  dialogError.value = undefined
  dialogOpen.value = true
}
async function create() {
  busy.value = true
  dialogError.value = undefined
  try {
    secret.value = (await api.createToken(name.value, Number(expiry.value))).secret
    await load()
  } catch (e) {
    dialogError.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}

const revoking = ref<ApiToken | null>(null)
async function revoke() {
  if (!revoking.value) return
  error.value = undefined
  try {
    await api.deleteToken(revoking.value.id)
    await load()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    revoking.value = null
  }
}
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
            <CardTitle class="flex items-center gap-2"><KeyRound class="size-4" /> API tokens</CardTitle>
            <CardDescription>
              Tokens sign in the <code>qs</code> command-line client and scripts as you. <code>qs login</code> makes
              one for each computer you sign in on. Tokens stop working when unused for 90 days, when revoked here, or
              when your password changes.
            </CardDescription>
          </div>
          <Button @click="openCreate"><Plus /> New token</Button>
        </CardHeader>
        <CardContent>
          <div class="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead class="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="t in tokens" :key="t.id">
                  <TableCell class="font-medium">{{ t.name }}</TableCell>
                  <TableCell>{{ formatDate(t.createdAt) }}</TableCell>
                  <TableCell>
                    <template v-if="isSet(t.lastUsedAt)">{{ formatDate(t.lastUsedAt) }}</template>
                    <span v-else class="text-muted-foreground">Never</span>
                  </TableCell>
                  <TableCell>
                    <template v-if="isSet(t.expiresAt)">{{ formatDate(t.expiresAt) }}</template>
                    <span v-else class="text-muted-foreground">When unused for 90 days</span>
                  </TableCell>
                  <TableCell>
                    <Button variant="ghost" size="icon-sm" :title="`Revoke ${t.name}`" @click="revoking = t">
                      <Trash2 />
                    </Button>
                  </TableCell>
                </TableRow>
                <TableEmpty v-if="!tokens.length" :colspan="5">You have no API tokens.</TableEmpty>
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
    </main>

    <Dialog v-model:open="dialogOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ secret ? 'Your new token' : 'New API token' }}</DialogTitle>
          <DialogDescription>
            {{
              secret
                ? "Copy it now: it isn't shown again. Sign qs in with it by running the command below and pasting it."
                : 'For qs on a computer without a browser, or for scripts.'
            }}
          </DialogDescription>
        </DialogHeader>
        <Alert v-if="dialogError" variant="destructive">
          <CircleAlert />
          <AlertDescription>{{ dialogError }}</AlertDescription>
        </Alert>
        <div v-if="secret" class="flex flex-col gap-3">
          <div class="flex items-center gap-2">
            <code class="bg-muted min-w-0 flex-1 truncate rounded-md px-2 py-1.5 text-xs">{{ secret }}</code>
            <Button variant="outline" size="sm" @click="copy(secret)">
              <component :is="copied ? Check : Copy" /> {{ copied ? 'Copied' : 'Copy' }}
            </Button>
          </div>
          <code class="bg-muted rounded-md px-2 py-1.5 text-xs">{{ loginCommand }}</code>
        </div>
        <form v-else id="token-form" class="flex flex-col gap-3" @submit.prevent="create">
          <div class="flex flex-col gap-1.5">
            <Label for="token-name">Name</Label>
            <Input id="token-name" v-model="name" placeholder="e.g. build server" maxlength="100" required />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="token-expiry">Expires</Label>
            <Select v-model="expiry">
              <SelectTrigger id="token-expiry" class="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="0">When unused for 90 days</SelectItem>
                <SelectItem value="30">In 30 days</SelectItem>
                <SelectItem value="90">In 90 days</SelectItem>
                <SelectItem value="365">In a year</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </form>
        <DialogFooter>
          <Button v-if="secret" @click="dialogOpen = false">Done</Button>
          <template v-else>
            <Button variant="outline" @click="dialogOpen = false">Cancel</Button>
            <Button type="submit" form="token-form" :disabled="busy">Create token</Button>
          </template>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <AlertDialog :open="!!revoking" @update:open="(o) => !o && (revoking = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke {{ revoking?.name }}?</AlertDialogTitle>
          <AlertDialogDescription>
            Anything signed in with it, such as qs on that computer, is signed out.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button variant="destructive" @click="revoke">Revoke</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
