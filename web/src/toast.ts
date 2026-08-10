import { useSyncExternalStore } from 'react'

/**
 * The queue behind the corner.
 *
 * A toast used to be a piece of page state — one string, one slot — and that
 * shape has a failure mode the page never showed anyone: the second outcome
 * overwrites the first. 对账 walks the fleet while a download is still landing,
 * and whichever finished last was the only one anybody read. Worse, the slot
 * held its own timer, so a message that arrived two seconds into the previous
 * one's four and a half got whatever was left of them.
 *
 * So outcomes go into a list instead, oldest first, and the stack renders them
 * bottom-anchored: a new one appears in the corner and pushes the older ones
 * up, each keeping its own clock. Nothing is replaced, and nothing is skipped.
 *
 * Module state rather than a context, because the callers are event handlers
 * halfway down a promise chain in four different files, and threading a
 * provider to each of them buys nothing — there is one corner of one screen,
 * and it is the same corner from everywhere.
 */
export interface ToastItem {
  id: number
  message: string
}

/** Past this the corner is a log rather than a report, and the oldest of them
 *  is being scrolled off the top unread anyway. The oldest goes. */
const MAX_STACKED = 4

let items: ToastItem[] = []
let seq = 0
const listeners = new Set<() => void>()

function publish(next: ToastItem[]): void {
  items = next
  for (const listener of listeners) listener()
}

/** Says that something finished. Errors do not come through here — something
 *  that failed has to stay on screen until it is read. */
export function toast(message: string): void {
  seq += 1
  const next = [...items, { id: seq, message }]
  publish(next.length > MAX_STACKED ? next.slice(next.length - MAX_STACKED) : next)
}

export function dismissToast(id: number): void {
  publish(items.filter((item) => item.id !== id))
}

export function useToasts(): ToastItem[] {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => items,
  )
}
