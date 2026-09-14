import { useCallback, useEffect } from 'react'
import { createPortal } from 'react-dom'

import { DUR, reducedMotion } from '../motion'
import type { ToastItem, ToastTone } from '../toast'
import { dismissToast, useToasts } from '../toast'
import { useDismiss } from '../useDismiss'

/** Long enough to look up from what you were doing, find the corner and read a
 *  sentence — which is a second or two more than it takes to read one.
 *
 *  Only consulted for a toast that leaves on its own; a sticky one never starts
 *  a clock. An error that is explicitly not sticky gets the warn duration,
 *  because what makes an error worth longer is the risk of going unread, and
 *  this one has been declared readable at a glance. */
const LINGER: Record<ToastTone, number> = {
  ok: 6000,
  warn: 10000,
  error: 10000,
}

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
 * Errors come through here now and do not leave on their own. The rule this
 * replaces — errors stay in the page because a message that removes itself can
 * be missed — was right that a failure has to stay, and wrong that the corner
 * cannot hold one: 开关机失败 sat at the top of the instance page, which is the
 * page you leave to go and look at why.
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
    if (item.sticky) return
    const timer = window.setTimeout(close, LINGER[item.tone])
    return () => window.clearTimeout(timer)
  }, [close, item.sticky, item.tone])

  return (
    <div
      className="toast"
      data-tone={item.tone}
      data-state={leaving && !reducedMotion() ? 'out' : 'in'}
      // A failure has to reach a screen reader as it lands rather than waiting
      // for a pause in whatever is being read.
      role={item.tone === 'error' ? 'alert' : 'status'}
    >
      <span className="toast__mark" aria-hidden="true" />
      <span className="toast__body">
        {item.message}
        {/* A sticky toast closes by being acknowledged, not by being swatted:
            × reads as "stop bothering me" and 知道了 reads as "I have read it",
            and for the one kind of message that is not allowed to go unread
            that difference is the whole point. */}
        {item.sticky && (
          <button className="link toast__ack" onClick={close}>
            知道了
          </button>
        )}
      </span>
      {!item.sticky && (
        <button className="toast__close" onClick={close} aria-label="关闭">
          ×
        </button>
      )}
    </div>
  )
}
