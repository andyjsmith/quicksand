import { computed, ref } from 'vue'
import { api, ApiError, setUnauthorizedHandler, type User } from '@/api'
import { router } from '@/router'

// The signed-in user, shared app-wide. undefined until first checked.
const user = ref<User | null | undefined>(undefined)
let checking: Promise<User | null> | undefined

/** Resolves the current user, asking the server once per page load. */
function ensureUser(): Promise<User | null> {
  if (user.value !== undefined) return Promise.resolve(user.value)
  checking ??= api.me().then(
    (u) => (user.value = u),
    (e) => {
      if (e instanceof ApiError && e.status === 401) return (user.value = null)
      checking = undefined
      throw e
    },
  )
  return checking
}

// Any API call that finds the session gone sends the user to sign in.
setUnauthorizedHandler(() => {
  user.value = null
  const current = router.currentRoute.value
  if (current.name !== 'login') router.push({ name: 'login', query: { redirect: current.fullPath } })
})

export function useAuth() {
  return {
    user: computed(() => user.value ?? null),
    isAdmin: computed(() => user.value?.isAdmin ?? false),
    ensureUser,
    async login(username: string, password: string) {
      user.value = await api.login(username, password)
    },
    async logout() {
      await api.logout().catch(() => {})
      user.value = null
      checking = undefined
      router.push({ name: 'login' })
    },
  }
}
