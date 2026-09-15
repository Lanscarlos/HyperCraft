/**
 * The 像素字体 switch.
 *
 * Built like theme.ts and for the same reasons: the preference lives in
 * localStorage, the resolved answer lives as a `data-` attribute on <html>, and
 * index.html applies it inline before the bundle loads so a reload never paints
 * one face and then swaps to the other. Everything visual is in styles.css —
 * this module only decides whether `data-pixel-font` is there.
 *
 * Off is the default, and it is stored as the *absence* of a key. That is the
 * same trick theme.ts uses for 跟随系统: the stored value only ever means "I
 * went out of my way to turn this on", so nobody who has never opened the
 * setting is pinned to whatever the default happened to be the day they first
 * loaded the panel.
 *
 * It shipped defaulting to on and was turned around, because the face is a
 * display face doing a body face's job. Set as running prose it loses the
 * rhythm a sentence is read by — every glyph advances by the same cell, so
 * there is no word shape left to skim — and most of this panel's prose is CJK,
 * which Monocraft has no glyphs for at all and which falls through to Fusion
 * Pixel. The mark and the numbers are where it earns its place, and those are
 * still there for anyone who switches it back on.
 *
 * The two terminal canvases are deliberately outside all of this. They take
 * their font from constants in Console.tsx and HostTerminal.tsx, not from CSS,
 * so there is nothing here that could reach them by accident — which is the
 * point. Server output is read for exactness.
 */

import { crossFade } from './motion'

export type PixelFontPref = 'on' | 'off'

/** Shared with the inline script in index.html — changing it here alone would
 *  strand a stored preference and flash the wrong face on load. */
const STORAGE_KEY = 'hypercraft.pixelfont'

export function readPref(): PixelFontPref {
  try {
    // Anything that is not the explicit opt-in reads as off, including the
    // 'off' an older build wrote to mean the same thing it means now.
    return window.localStorage.getItem(STORAGE_KEY) === 'on' ? 'on' : 'off'
  } catch {
    // Private mode, or storage disabled by policy. The default is still off.
    return 'off'
  }
}

/** What is actually on screen right now, which is the attribute rather than
 *  the preference: the inline script may have run before storage was readable. */
export function current(): PixelFontPref {
  return document.documentElement.dataset.pixelFont === 'on' ? 'on' : 'off'
}

export function applyPref(pref: PixelFontPref): void {
  try {
    if (pref === 'off') window.localStorage.removeItem(STORAGE_KEY)
    else window.localStorage.setItem(STORAGE_KEY, 'on')
  } catch {
    /* nothing to remember it with; the session still switches */
  }

  // Every glyph on the page is replaced at once and there is no element to hang
  // a transition on — the same problem the mode switch has, so the same answer.
  // crossFade dissolves between the two frames, and falls back to a plain cut
  // where the browser has no view transitions or the reader has asked for less
  // motion.
  crossFade(() => {
    if (pref === 'on') document.documentElement.dataset.pixelFont = 'on'
    else delete document.documentElement.dataset.pixelFont
  })
}
