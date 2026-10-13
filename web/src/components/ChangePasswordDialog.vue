<script setup lang="ts">
import { CircleAlert } from '@lucide/vue'
import { ref, watch } from 'vue'
import { api, errorMessage } from '@/api'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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

const open = defineModel<boolean>('open', { required: true })

const current = ref('')
const next = ref('')
const repeat = ref('')
const error = ref<string>()
const busy = ref(false)

watch(open, (o) => {
  if (o) current.value = next.value = repeat.value = ''
  error.value = undefined
})

async function submit() {
  if (next.value !== repeat.value) {
    error.value = "The new passwords don't match."
    return
  }
  busy.value = true
  error.value = undefined
  try {
    await api.changePassword(current.value, next.value)
    open.value = false
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-sm">
      <DialogHeader>
        <DialogTitle>Change password</DialogTitle>
        <DialogDescription>You'll stay signed in here; other sessions are signed out.</DialogDescription>
      </DialogHeader>
      <form id="change-password" class="flex flex-col gap-3" @submit.prevent="submit">
        <Alert v-if="error" variant="destructive">
          <CircleAlert />
          <AlertDescription>{{ error }}</AlertDescription>
        </Alert>
        <div class="flex flex-col gap-1.5">
          <Label for="pw-current">Current password</Label>
          <Input id="pw-current" v-model="current" type="password" autocomplete="current-password" required />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="pw-new">New password</Label>
          <Input id="pw-new" v-model="next" type="password" autocomplete="new-password" minlength="8" required />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="pw-repeat">Repeat new password</Label>
          <Input id="pw-repeat" v-model="repeat" type="password" autocomplete="new-password" required />
        </div>
      </form>
      <DialogFooter>
        <Button variant="outline" @click="open = false">Cancel</Button>
        <Button type="submit" form="change-password" :disabled="busy">Change password</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
