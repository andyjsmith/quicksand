import { ref } from 'vue'
import { api, type LinksInfo } from '@/api'

// Whether the server has link networks configured, fetched once.
const info = ref<LinksInfo>({ enabled: false })
let loading: Promise<void> | undefined

export function useLinks() {
  loading ??= api.links().then(
    (i) => void (info.value = i),
    () => void (loading = undefined),
  )
  return info
}

/** Colors telling link networks apart, picked by network ID. */
const palette = [
  { box: 'border-sky-500/40 bg-sky-500/[0.04]', text: 'text-sky-600 dark:text-sky-400', dot: 'bg-sky-500' },
  { box: 'border-violet-500/40 bg-violet-500/[0.04]', text: 'text-violet-600 dark:text-violet-400', dot: 'bg-violet-500' },
  { box: 'border-emerald-500/40 bg-emerald-500/[0.04]', text: 'text-emerald-600 dark:text-emerald-400', dot: 'bg-emerald-500' },
  { box: 'border-amber-500/40 bg-amber-500/[0.05]', text: 'text-amber-600 dark:text-amber-400', dot: 'bg-amber-500' },
  { box: 'border-rose-500/40 bg-rose-500/[0.04]', text: 'text-rose-600 dark:text-rose-400', dot: 'bg-rose-500' },
  { box: 'border-teal-500/40 bg-teal-500/[0.04]', text: 'text-teal-600 dark:text-teal-400', dot: 'bg-teal-500' },
]
export const linkColor = (network: number) => palette[network % palette.length]!
