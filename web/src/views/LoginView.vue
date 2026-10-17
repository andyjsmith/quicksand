<script setup lang="ts">
import { useTitle } from '@vueuse/core'
import { CircleAlert, LogIn, Server } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorMessage, type AuthMethods } from '@/api'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Spinner } from '@/components/ui/spinner'
import { authMethods, useAuth } from '@/composables/useAuth'

useTitle('Sign in · Quicksand')

const auth = useAuth()
const route = useRoute()
const router = useRouter()

const methods = ref<AuthMethods>()
const username = ref('')
const password = ref('')
const error = ref<string>()
const busy = ref(false)

const redirect = computed(() =>
  typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') ? route.query.redirect : '/',
)
const ssoURL = computed(() => `/api/auth/oidc/login?${new URLSearchParams({ next: redirect.value })}`)

// A failed single sign-on leaves its reason in a short-lived cookie.
function takeSSOError(): string | undefined {
  const match = document.cookie.match(/(?:^|;\s*)quicksand_sso_error=([^;]*)/)
  if (!match) return undefined
  document.cookie = 'quicksand_sso_error=; Max-Age=0; Path=/'
  try {
    const b64 = match[1]!.replace(/-/g, '+').replace(/_/g, '/')
    return new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)))
  } catch {
    return undefined
  }
}

onMounted(async () => {
  error.value = takeSSOError()
  methods.value = await authMethods()
})

async function submit() {
  busy.value = true
  error.value = undefined
  try {
    await auth.login(username.value, password.value)
    router.replace(redirect.value)
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
        <CardDescription v-if="methods?.password && !methods.sso">Use the account your administrator gave you.</CardDescription>
      </CardHeader>
      <CardContent class="flex flex-col gap-4">
        <Alert v-if="error" variant="destructive">
          <CircleAlert />
          <AlertDescription>{{ error }}</AlertDescription>
        </Alert>
        <div v-if="!methods" class="flex justify-center py-4"><Spinner /></div>
        <template v-else>
          <Button v-if="methods.sso" size="lg" as-child>
            <a :href="ssoURL"><LogIn /> Sign in with {{ methods.sso.name }}</a>
          </Button>
          <div v-if="methods.sso && methods.password" class="text-muted-foreground flex items-center gap-3 text-xs">
            <Separator class="flex-1" /> or with a password <Separator class="flex-1" />
          </div>
          <form v-if="methods.password" class="flex flex-col gap-4" @submit.prevent="submit">
            <div class="flex flex-col gap-1.5">
              <Label for="username">Username</Label>
              <Input id="username" v-model="username" autocomplete="username" :autofocus="!methods.sso" required />
            </div>
            <div class="flex flex-col gap-1.5">
              <Label for="password">Password</Label>
              <Input id="password" v-model="password" type="password" autocomplete="current-password" required />
            </div>
            <Button type="submit" size="lg" :variant="methods.sso ? 'outline' : 'default'" :disabled="busy">
              <Spinner v-if="busy" />
              Sign in
            </Button>
          </form>
        </template>
      </CardContent>
    </Card>
  </div>
</template>
