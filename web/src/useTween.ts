import { useEffect, useRef, useState } from 'react'

import { DUR, reducedMotion } from './motion'

/* --ease-out from styles.css, as control points instead of a string.
 *
 * The same hand-kept correspondence DUR has with the --dur-* tokens, and for
 * the same reason: CSS takes the curve as a string it can parse, and this file
 * has to walk it a frame at a time. One curve, two shapes — not two curves.
 *
 * --ease-out is the right one here and the others are not. A number tracking
 * data has to arrive as soon as it credibly can; --ease's long tail reads as
 * the panel lagging behind the server rather than as the value settling. It is
 * the curve the meter fills already use (styles.css: `transition: width
 * var(--dur-data) var(--ease-out)`), which matters — a bar and the number
 * beside it describing the same reading must move as one thing.
 */
const [X1, Y1, X2, Y2] = [0.33, 0, 0.2, 1]

/** One axis of a cubic Bézier with its ends pinned at 0 and 1. */
function axis(a: number, b: number, u: number): number {
  const v = 1 - u
  return 3 * v * v * u * a + 3 * v * u * u * b + u * u * u
}

function slope(a: number, b: number, u: number): number {
  const v = 1 - u
  return 3 * v * v * a + 6 * v * u * (b - a) + 3 * u * u * (1 - b)
}

/**
 * The curve at elapsed fraction `t`.
 *
 * A cubic Bézier is parameterised by its own variable, not by x, so reading it
 * at a point in time means solving x(u) = t first. Newton–Raphson from u = t
 * converges in two or three passes on a curve this gentle; five is the ceiling
 * rather than the cost, and the whole thing is a handful of multiplications on
 * a value that is about to be rounded to a percent anyway.
 */
function easeOut(t: number): number {
  if (t <= 0) return 0
  if (t >= 1) return 1

  let u = t
  for (let i = 0; i < 5; i++) {
    const error = axis(X1, X2, u) - t
    if (Math.abs(error) < 1e-4) break
    const d = slope(X1, X2, u)
    if (d === 0) break
    u -= error / d
  }
  return axis(Y1, Y2, u)
}

/**
 * A readout that travels to its next value instead of cutting to it.
 *
 * The panel polls every five seconds, so a live number spends most of its life
 * standing still and then jumps. A jump carries no direction: 47% replacing
 * 12% and 47% replacing 52% look identical, and the one thing an operator
 * wants from a glance at the CPU tile is which way it is going. A few hundred
 * milliseconds of travel answers that without anyone having to watch for it.
 *
 * This is the one piece of the panel's motion CSS cannot do at all — there is
 * no transition on the contents of a text node — which is why it is here and
 * not in styles.css with everything else.
 *
 * Two rules keep it a readout rather than an effect:
 *
 *   - The first reading lands. A tile that counts up from zero every time the
 *     page opens is a stopwatch; the animation is only meaningful when there
 *     is a previous value it is moving away from. `null` is how a caller says
 *     "no reading" — a stopped server, a sample that has not arrived — and
 *     coming back out of `null` lands too.
 *   - A sample arriving mid-flight continues from the digits currently on
 *     screen, not from the last target, so a fast-moving value never snaps
 *     backwards between polls.
 *
 * Everywhere this is used, the element has `font-variant-numeric: tabular-nums`
 * already — without it the digits change width as they run and the whole line
 * shivers for the length of the animation.
 */
export function useTween(target: number): number
export function useTween(target: number | null): number | null
export function useTween(target: number | null): number | null {
  const [shown, setShown] = useState<number | null>(target)
  /** What is on screen this instant, which is where the next journey starts. */
  const live = useRef<number | null>(target)

  useEffect(() => {
    const from = live.current
    // Nothing to travel between, or nobody who wants to watch it: WAAPI and
    // CSS honour prefers-reduced-motion on their own, a requestAnimationFrame
    // loop does not (see motion.ts).
    if (target === null || from === null || !Number.isFinite(target) || reducedMotion()) {
      live.current = target
      setShown(target)
      return
    }
    if (from === target) return

    let frame = 0
    const started = performance.now()
    const step = (now: number) => {
      const t = Math.min(1, (now - started) / DUR.data)
      const value = from + (target - from) * easeOut(t)
      live.current = value
      setShown(value)
      if (t < 1) frame = requestAnimationFrame(step)
    }
    frame = requestAnimationFrame(step)
    return () => cancelAnimationFrame(frame)
  }, [target])

  return shown
}
