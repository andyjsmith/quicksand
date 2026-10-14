import { shallowRef, type Ref } from 'vue'

let shared: Ref<Date> | undefined

/** A clock shared by every expiry display, ticking every 30 seconds. */
export function useMinuteClock(): Ref<Date> {
  if (!shared) {
    const now = shallowRef(new Date())
    setInterval(() => (now.value = new Date()), 30_000)
    shared = now
  }
  return shared
}
