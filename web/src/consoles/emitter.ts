/** A minimal typed event emitter. */
export class Emitter<Events extends object> {
  private handlers = new Map<keyof Events, Set<(payload: never) => void>>()

  on<K extends keyof Events>(event: K, handler: (payload: Events[K]) => void): () => void {
    let set = this.handlers.get(event)
    if (!set) {
      set = new Set()
      this.handlers.set(event, set)
    }
    set.add(handler as (payload: never) => void)
    return () => set.delete(handler as (payload: never) => void)
  }

  emit<K extends keyof Events>(event: K, payload: Events[K]): void {
    for (const handler of this.handlers.get(event) ?? []) {
      ;(handler as (payload: Events[K]) => void)(payload)
    }
  }

  clear(): void {
    this.handlers.clear()
  }
}
