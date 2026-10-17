<script setup lang="ts">
import { ChevronDown, KeyRound, KeySquare, LogOut, Server, Shield } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api'
import ChangePasswordDialog from '@/components/ChangePasswordDialog.vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { authMethods, useAuth } from '@/composables/useAuth'
import { resetMachines } from '@/composables/useMachines'

const auth = useAuth()
const changingPassword = ref(false)

// Single sign-on users have no password to change, unless an admin set one.
const passwords = ref(false)
onMounted(async () => (passwords.value = (await authMethods()).password))
const canChangePassword = computed(() => passwords.value && !!auth.user.value?.hasPassword)

const router = useRouter()
const version = ref('')
onMounted(async () => (version.value = (await api.version().catch(() => ({ version: '' }))).version))

async function logout() {
  resetMachines()
  await auth.logout()
}
</script>

<template>
  <header class="bg-background/80 sticky top-0 z-10 border-b backdrop-blur">
    <div class="mx-auto flex max-w-6xl items-center gap-4 px-4 py-2.5">
      <RouterLink to="/" class="flex items-center gap-2">
        <Server class="size-4" />
        <span class="text-sm font-semibold">Quicksand</span>
      </RouterLink>
      <nav class="flex items-center gap-1">
        <Button variant="ghost" size="sm" as-child>
          <RouterLink to="/" active-class="bg-muted">Machines</RouterLink>
        </Button>
        <Button v-if="auth.isAdmin.value" variant="ghost" size="sm" as-child>
          <RouterLink to="/admin" active-class="bg-muted">Admin</RouterLink>
        </Button>
      </nav>
      <div class="flex-1" />
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button variant="ghost" size="sm">
            <Shield v-if="auth.isAdmin.value" class="text-muted-foreground" />
            {{ auth.user.value?.username }}
            <ChevronDown />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" class="w-48">
          <DropdownMenuLabel class="text-muted-foreground text-xs">
            Signed in as {{ auth.user.value?.username }}
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem v-if="canChangePassword" @select="changingPassword = true">
            <KeyRound /> Change password
          </DropdownMenuItem>
          <DropdownMenuItem @select="router.push({ name: 'tokens' })"><KeySquare /> API tokens</DropdownMenuItem>
          <DropdownMenuItem @select="logout"><LogOut /> Sign out</DropdownMenuItem>
          <template v-if="version">
            <DropdownMenuSeparator />
            <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">Quicksand {{ version }}</DropdownMenuLabel>
          </template>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
    <ChangePasswordDialog v-model:open="changingPassword" />
  </header>
</template>
