import { useEffect } from 'react'
import { createPortal } from 'react-dom'

import { DUR, reducedMotion } from '../motion'
import { useDismiss } from '../useDismiss'

/** Long enough to read a sentence and glance at what changed behind it. */
const LINGER = 4500

/**
 * The result of something that already happened.
 *
 * It used to be an alert block in the page, and an alert block is the wrong
 * shape for this: 已下载 LuckPerms v5.5.71 is finished news, and it sat there
 * pushing the list it was about half a screen down until the next navigation.
 * A block belongs to a state — 版本不一致, 下载失败, 这台服认不出核心 — and a
 * state is worth the space because it is still true. An outcome is worth four
 * and a half seconds in the corner.
 *
 * Errors are deliberately not routed here. Something that failed has to stay
 * on screen until it is read, and a message that removes itself is a message
 * that can be missed.
 */
export function Toast({ message, onDone }: { message: string; onDone: () => void }) {
  const { leaving, close } = useDismiss(onDone, DUR.mid)

  useEffect(() => {
    // Reduced motion shortens the exit to nothing, not the reading time — the
    // preference is about movement, not about how fast someone reads.
    const timer = window.setTimeout(close, LINGER)
    return () => window.clearTimeout(timer)
  }, [close])

  return createPortal(
    <div className="toast" data-state={leaving && !reducedMotion() ? 'out' : 'in'} role="status">
      <span className="toast__body">{message}</span>
      <button className="toast__close" onClick={close} aria-label="关闭">
        ×
      </button>
    </div>,
    document.body,
  )
}
