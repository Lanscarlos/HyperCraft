/**
 * The parts of the panel's motion that CSS cannot do on its own.
 *
 * Almost all of it is in styles.css, where it belongs — durations, curves,
 * every hover and press and entrance. Three things are not expressible there
 * and live here instead:
 *
 *   - a component that must stay mounted while it animates away (a menu, a
 *     dialog), which is a scheduled unmount and therefore JavaScript;
 *   - an element that has to travel from where another element *was*, which
 *     needs two measurements and cannot be written as a keyframe;
 *   - switching the whole colour scheme, which changes every painted pixel at
 *     once and has no single element to hang a transition on.
 *
 * A fourth — a number walking to the value just polled, which no transition can
 * reach because it is the contents of a text node — is a hook and therefore in
 * useTween.ts, where the rest of the hooks are. It borrows its duration and its
 * reduced-motion check from here.
 *
 * All of them read `prefers-reduced-motion` themselves. The stylesheet's
 * blanket override only reaches CSS animations and transitions; a WAAPI
 * animation, a setTimeout and a requestAnimationFrame loop would sail straight
 * through it, so honouring the preference has to be a decision each of these
 * makes.
 */

/** Mirrors the --dur-* tokens. Kept in step with styles.css by hand: these are
 *  the few durations JavaScript has to know, not a second source of truth for
 *  the ones CSS already owns. */
export const DUR = {
  /** --dur-1: a tint under the pointer. */
  fast: 90,
  /** --dur: the default, and the length of every exit in the panel. */
  base: 140,
  /** --dur-3: something arriving or leaving where it stands. */
  mid: 220,
  /** --dur-4: something that crosses the screen or covers it. */
  slow: 300,
  /** --dur-data: a number or a bar walking to the value just polled. Longer
   *  than any of the above on purpose — this one is not a response to a press
   *  and has nobody waiting on it, and the extra time is what makes the
   *  direction of travel readable. */
  data: 400,
} as const

export const EASE = 'cubic-bezier(0.32, 0.72, 0, 1)'
export const EASE_IN = 'cubic-bezier(0.5, 0, 0.9, 0.3)'

/** Read live rather than cached: the preference can be changed while the panel
 *  is open, and a stale answer here means motion someone has asked not to see. */
export function reducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

/**
 * A whole-page cross-fade around a change nothing else can animate.
 *
 * Used for one thing: light ↔ dark. Every surface, every border and every piece
 * of ink changes in the same frame, and there is no element to put a transition
 * on — transitioning `background-color` on `*` would mean animating a few
 * thousand properties at once, which on a page with a live console is a visible
 * stall. The view transition takes one snapshot, swaps the attribute, and
 * cross-fades the two frames on the compositor.
 *
 * Where the API is missing — or motion is unwanted — the change simply happens,
 * which is exactly what happened here before.
 */
export function crossFade(mutate: () => void): void {
  const start = (
    document as Document & {
      startViewTransition?: (callback: () => void) => unknown
    }
  ).startViewTransition

  if (typeof start !== 'function' || reducedMotion()) {
    mutate()
    return
  }
  start.call(document, mutate)
}
