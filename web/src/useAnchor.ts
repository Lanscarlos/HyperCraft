import { useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

/** Between a trigger and the sheet it opens, and between that sheet and the
 *  viewport edge it would otherwise touch. */
export const GAP = 6
export const EDGE = 8

/**
 * Where a sheet goes vertically, measured against its trigger and the viewport.
 *
 * Below the trigger unless there is not room for a usable sheet down there and
 * there is more room up. This is the rule that was missing from the row menus:
 * a `⋯` in the last row of a table has a few pixels below it and half a screen
 * above, and a sheet that only ever opens downwards is a sheet nobody in that
 * row can read.
 */
export interface Placement {
  /** Whether the sheet opens above the trigger rather than below it. */
  up: boolean
  /** Distance from the viewport's top edge, or from its bottom when `up`. */
  offset: number
  maxHeight: number
}

export function placeVertically(rect: DOMRect, want: number): Placement {
  const below = window.innerHeight - rect.bottom - GAP - EDGE
  const above = rect.top - GAP - EDGE
  const up = below < Math.min(want, 180) && above > below

  return {
    up,
    offset: up ? window.innerHeight - rect.top + GAP : rect.bottom + GAP,
    maxHeight: Math.max(Math.min(want, up ? above : below), 96),
  }
}

/**
 * Keeps a portalled sheet stuck to the trigger it belongs to.
 *
 * Every popover here is portalled to <body> and positioned against its
 * trigger's viewport rect, for the reason the modal is: absolute positioning
 * puts the sheet inside whichever scroll box or `overflow: hidden` card the
 * trigger happens to live in, and the plugin table is exactly such a card —
 * `overflow: hidden` is what gives it its rounded corners, and it was also
 * what cropped the last row's menu down to nothing.
 *
 * Fixed to the viewport means anything that moves the trigger has to move the
 * sheet too, or it is left hanging in space — hence the listeners, and hence
 * the capture on scroll: the scroll that matters is usually some ancestor's,
 * and scroll events do not bubble.
 */
export function useAnchor<T>(
  open: boolean,
  trigger: RefObject<HTMLElement | null>,
  measure: (rect: DOMRect) => T,
): T | null {
  const [anchor, setAnchor] = useState<T | null>(null)

  // Held in a ref so a caller can write the measurement inline without
  // re-subscribing every render.
  const latest = useRef(measure)
  latest.current = measure

  useLayoutEffect(() => {
    if (!open) return
    const element = trigger.current
    if (!element) return

    const follow = () => setAnchor(latest.current(element.getBoundingClientRect()))
    follow()
    window.addEventListener('scroll', follow, true)
    window.addEventListener('resize', follow)
    return () => {
      window.removeEventListener('scroll', follow, true)
      window.removeEventListener('resize', follow)
    }
  }, [open, trigger])

  return open ? anchor : null
}
