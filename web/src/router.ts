import { createRouter, createWebHistory } from 'vue-router'
import { useAuth } from '@/composables/useAuth'

declare module 'vue-router' {
  interface RouteMeta {
    /** Reachable without signing in. */
    public?: boolean
    admin?: boolean
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { public: true } },
    { path: '/', name: 'dashboard', component: () => import('@/views/DashboardView.vue') },
    { path: '/vm/:id', name: 'vm', component: () => import('@/views/VmView.vue'), props: true },
    {
      path: '/console/:id',
      name: 'console',
      component: () => import('@/views/ConsoleView.vue'),
      props: true,
    },
    { path: '/admin', name: 'admin', component: () => import('@/views/AdminView.vue'), meta: { admin: true } },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to) => {
  // If the server can't be reached (e.g. restarting), treat the session as
  // unknown and show the sign-in page rather than a blank one; the check is
  // retried on the next navigation.
  const user = await useAuth()
    .ensureUser()
    .catch(() => null)
  if (to.meta.public) return user && to.name === 'login' ? { name: 'dashboard' } : true
  if (!user) return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  if (to.meta.admin && !user.isAdmin) return { name: 'dashboard' }
  return true
})
