<script setup lang="ts">
import { ChevronDown, KeyRound, LogOut, Server, Shield } from '@lucide/vue'
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
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
import { useAuth } from '@/composables/useAuth'
import { resetMachines } from '@/composables/useMachines'

const auth = useAuth()
const changingPassword = ref(false)

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
          <DropdownMenuItem @select="changingPassword = true">
            <KeyRound /> Change password
          </DropdownMenuItem>
          <DropdownMenuItem @select="logout"><LogOut /> Sign out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
    <ChangePasswordDialog v-model:open="changingPassword" />
  </header>
</template>
