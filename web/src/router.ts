import { createRouter, createWebHistory } from 'vue-router'
import VmListView from '@/views/VmListView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'vms', component: VmListView },
    {
      path: '/console/:node/:vmid',
      name: 'console',
      component: () => import('@/views/ConsoleView.vue'),
      props: true,
    },
  ],
})
