import { useCallback, useEffect, useRef, useState } from 'react'

import { DUR, reducedMotion } from './motion'

/**
 * Closing something without it vanishing.
 *
 * A popover in React is `open && <Sheet/>`, and the moment `open` goes false
 * the element is gone — there is no frame left in which to animate it out. So
 * every sheet in the panel used to rise into place over a fifth of a second and
 * then disappear between two frames, which does not read as a dismissal; it
 * reads as a rendering fault, and it is most of why closing things here felt
 * abrupt when opening them did not.
 *
 * The fix is to keep the decision to close and the unmount a couple of frames
 * apart. `close()` flips `leaving` — which the stylesheet picks up as a
 * data-state and animates — and only then calls the caller's own onClose, by
 * which time the exit has played. The component stays mounted the whole time
 * because the thing that unmounts it has not been told yet.
 *
 * The exit is one --dur, not one --dur-3: you have already decided to leave,
 * and sitting through a leisurely fade of something you are finished with is
 * its own kind of friction.
 */
export function useDismiss(onClose: () => void, ms: number = DUR.base) {
  const [leaving, setLeaving] = useState(false)
  const timer = useRef<number | null>(null)

  useEffect(
    () => () => {
      if (timer.current !== null) window.clearTimeout(timer.current)
    },
    [],
  )

  const close = useCallback(() => {
    // Already on its way out. A second Escape, or a click that lands on the
    // backdrop as it fades, must not queue a second unmount.
    if (timer.current !== null) return
    if (reducedMotion()) {
      onClose()
      return
    }
    setLeaving(true)
    timer.current = window.setTimeout(onClose, ms)
  }, [onClose, ms])

  return { leaving, close }
}
