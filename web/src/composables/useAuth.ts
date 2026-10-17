import { computed, ref } from 'vue'
import { api, ApiError, setUnauthorizedHandler, type AuthMethods, type User } from '@/api'
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

let methods: Promise<AuthMethods> | undefined

/** How users can sign in, asked once per page load (passwords only if the server can't say). */
export function authMethods(): Promise<AuthMethods> {
  methods ??= api.authMethods().catch(() => {
    methods = undefined
    return { password: true, sso: null }
  })
  return methods
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
      const out = await api.logout().catch(() => undefined)
      user.value = null
      checking = undefined
      // Single sign-on sessions end at the provider too, which then returns
      // to the sign-in page.
      if (out?.redirect) window.location.assign(out.redirect)
      else router.push({ name: 'login' })
    },
  }
}
