import { useCallback, useEffect } from 'react'
import { createPortal } from 'react-dom'

import { DUR, reducedMotion } from '../motion'
import type { ToastItem } from '../toast'
import { dismissToast, useToasts } from '../toast'
import { useDismiss } from '../useDismiss'

/** Long enough to look up from what you were doing, find the corner and read a
 *  sentence — which is a second or two more than it takes to read one. */
const LINGER = 6000

/**
 * Everything that has finished lately, in the corner.
 *
 * Bottom-anchored and growing upward, so the newest arrival is always in the
 * same place — the corner the eye already learned — and the ones already there
 * move up to make room rather than being replaced by it. The direction matters
 * on the way out too: expiring from the top shortens the column from the top,
 * so the message being read at the bottom does not slide as its elders go.
 *
 * One of these, at the app root. Everything else says `toast(...)`.
 */
export function ToastStack() {
  const items = useToasts()
  if (items.length === 0) return null

  return createPortal(
    <div className="toasts">
      {items.map((item) => (
        <Toast key={item.id} item={item} />
      ))}
    </div>,
    document.body,
  )
}

/**
 * The result of something that already happened.
 *
 * It used to be an alert block in the page, and an alert block is the wrong
 * shape for this: 已下载 LuckPerms v5.5.71 is finished news, and it sat there
 * pushing the list it was about half a screen down until the next navigation.
 * A block belongs to a state — 版本不一致, 下载失败, 这台服认不出核心 — and a
 * state is worth the space because it is still true. An outcome is worth a few
 * seconds in the corner.
 *
 * Sized to be caught out of the corner of an eye rather than to be discreet.
 * The first version was 13px in a box that shrank to fit its sentence, put a
 * thousand pixels away from the button that caused it, and the honest report on
 * it was that people clicked things twice because they never saw the first one
 * land. A fixed width, a tick, and body text a step up from the page's is the
 * difference between a message in the corner and a message you notice.
 *
 * Errors are deliberately not routed here. Something that failed has to stay
 * on screen until it is read, and a message that removes itself is a message
 * that can be missed.
 */
function Toast({ item }: { item: ToastItem }) {
  // Stable for the life of this toast, and it has to be: the effect below
  // keys its clock off `close`, so an onDone rebuilt on every render of the
  // stack would restart the countdown of everything already on screen each
  // time something new arrived — a busy minute would leave four toasts that
  // never expire.
  const done = useCallback(() => dismissToast(item.id), [item.id])
  const { leaving, close } = useDismiss(done, DUR.mid)

  useEffect(() => {
    // Reduced motion shortens the exit to nothing, not the reading time — the
    // preference is about movement, not about how fast someone reads.
    const timer = window.setTimeout(close, LINGER)
    return () => window.clearTimeout(timer)
  }, [close])

  return (
    <div className="toast" data-state={leaving && !reducedMotion() ? 'out' : 'in'} role="status">
      <span className="toast__mark" aria-hidden="true" />
      <span className="toast__body">{item.message}</span>
      <button className="toast__close" onClick={close} aria-label="关闭">
        ×
      </button>
    </div>
  )
}
