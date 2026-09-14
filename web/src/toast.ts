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
export type ToastTone = 'ok' | 'warn' | 'error'

export interface ToastItem {
  id: number
  message: string
  tone: ToastTone
  /** Does not leave on its own; the reader has to acknowledge it. Errors
   *  default to this. The rule it replaces — "errors never come through here,
   *  because a message that removes itself can be missed" — was right about
   *  the danger and wrong about the remedy: what an error needs is to stay,
   *  not to be kept out of the one place people look. */
  sticky: boolean
  action?: ToastAction
  /** Collapses repeats onto one row. The six 保存成功 slots this replaces were
   *  each "one slot, last write wins", and without a key a triple-click on
   *  保存 would stack three identical 已保存. Distinct from the list-vs-slot
   *  point above: that one is about *different* messages not overwriting each
   *  other, this one is about the *same* message not repeating. */
  key?: string
}

/** One thing to do about what just happened — 重启 after saving a config a
 *  running server has already read. Rare on purpose: an outcome that always
 *  wants a follow-up is a state, and a state belongs in the page rather than
 *  in a corner that expires. */
export interface ToastAction {
  label: string
  onSelect: () => void
}

export interface ToastOptions {
  key?: string
  sticky?: boolean
  action?: ToastAction
}

/** Past this the corner is a log rather than a report. Raised from four to six
 *  because sticky ones no longer expire on their own and would otherwise crowd
 *  out everything that does. */
const MAX_STACKED = 6

let items: ToastItem[] = []
let seq = 0
const listeners = new Set<() => void>()

function publish(next: ToastItem[]): void {
  items = next
  for (const listener of listeners) listener()
}

/**
 * Drops the oldest thing that was going to leave anyway.
 *
 * A sticky toast is only evicted when every slot holds one, and even then the
 * loss is acceptable: sticky toasts are a reminder, not the record. A failed
 * download is still in 下载 history, a failed page load is still in the page's
 * own 错误 slot. Evicting the oldest of six unread errors costs less than a
 * column that grows until it covers the console.
 */
function evict(next: ToastItem[]): ToastItem[] {
  while (next.length > MAX_STACKED) {
    const oldestExpiring = next.findIndex((item) => !item.sticky)
    next.splice(oldestExpiring === -1 ? 0 : oldestExpiring, 1)
  }
  return next
}

function push(
  tone: ToastTone,
  message: string,
  opts: ToastOptions,
  stickyByDefault: boolean,
): void {
  seq += 1
  const item: ToastItem = {
    id: seq,
    message,
    tone,
    sticky: opts.sticky ?? stickyByDefault,
    action: opts.action,
    key: opts.key,
  }
  const kept = item.key ? items.filter((existing) => existing.key !== item.key) : items
  publish(evict([...kept, item]))
}

/** Says that something finished. */
export function toast(message: string, opts: ToastOptions = {}): void {
  push('ok', message, opts, false)
}

/** Says that something finished, but not the way it was meant to. Does not
 *  stay by default: a warning the reader can act on immediately — the
 *  clipboard refused, select it by hand — does not need acknowledging. */
export function toastWarn(message: string, opts: ToastOptions = {}): void {
  push('warn', message, opts, false)
}

/** Says that something failed. Stays until acknowledged. */
export function toastError(message: string, opts: ToastOptions = {}): void {
  push('error', message, opts, true)
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
