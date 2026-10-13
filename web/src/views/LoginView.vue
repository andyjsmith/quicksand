<script setup lang="ts">
import { useTitle } from '@vueuse/core'
import { CircleAlert, Server } from '@lucide/vue'
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorMessage } from '@/api'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import { useAuth } from '@/composables/useAuth'

useTitle('Sign in · Quicksand')

const auth = useAuth()
const route = useRoute()
const router = useRouter()

const username = ref('')
const password = ref('')
const error = ref<string>()
const busy = ref(false)

async function submit() {
  busy.value = true
  error.value = undefined
  try {
    await auth.login(username.value, password.value)
    const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') ? route.query.redirect : '/'
    router.replace(redirect)
  } catch (e) {
    error.value = errorMessage(e)
    password.value = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="bg-background text-foreground flex min-h-dvh items-center justify-center px-4">
    <Card class="w-full max-w-sm">
      <CardHeader>
        <div class="mb-2 flex items-center gap-2">
          <Server class="size-5" />
          <span class="text-sm font-semibold">Quicksand</span>
        </div>
        <CardTitle>Sign in</CardTitle>
        <CardDescription>Use the account your administrator gave you.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="flex flex-col gap-4" @submit.prevent="submit">
          <Alert v-if="error" variant="destructive">
            <CircleAlert />
            <AlertDescription>{{ error }}</AlertDescription>
          </Alert>
          <div class="flex flex-col gap-1.5">
            <Label for="username">Username</Label>
            <Input id="username" v-model="username" autocomplete="username" autofocus required />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="password">Password</Label>
            <Input id="password" v-model="password" type="password" autocomplete="current-password" required />
          </div>
          <Button type="submit" size="lg" :disabled="busy">
            <Spinner v-if="busy" />
            Sign in
          </Button>
        </form>
      </CardContent>
    </Card>
  </div>
</template>
