/**
 * The 配色 switch.
 *
 * Built like theme.ts and pixelfont.ts, and for the same reasons: the
 * preference lives in localStorage, the resolved answer lives as a `data-`
 * attribute on <html>, and index.html applies it inline before the bundle loads
 * so a reload never paints one scheme and then swaps to another. Everything
 * visual is in styles.css — this module only decides which of the four token
 * tables `data-palette` selects.
 *
 * This is a second axis, not a replacement for the mode. A scheme and a mode
 * multiply: 松绿 has a light block and a dark block the same way the default
 * does, and the 跟随系统 toggle in the sidebar keeps working underneath
 * whichever one is picked.
 *
 * The default is stored as the *absence* of a key, the same trick the other two
 * use. The stored value only ever means "I went and picked something else", so
 * nobody who has never opened the setting is pinned to whatever shipped the day
 * they first loaded the panel.
 */

import { crossFade } from './motion'
import { notifyColours, syncChrome, syncFavicon } from './theme'

export type Palette = 'sakura' | 'green' | 'yellow' | 'blue'

export interface PaletteInfo {
  id: Palette
  name: string
  /** One line, because a colour does not need a paragraph. */
  note: string
}

/** Display order, default first. The three extras are Material Theme Builder
 *  schemes grown from one seed each; their token tables are in styles.css. */
export const PALETTES: PaletteInfo[] = [
  { id: 'sakura', name: '樱花', note: '出厂的暖粉色。' },
  { id: 'green', name: '松绿', note: '草木调的黄绿。' },
  { id: 'yellow', name: '杏黄', note: '四套里最亮的。' },
  { id: 'blue', name: '碧蓝', note: '唯一一套冷色。' },
]

const DEFAULT: Palette = 'sakura'

/** Shared with the inline script in index.html — changing it here alone would
 *  strand a stored preference and flash the wrong scheme on load. */
const STORAGE_KEY = 'hypercraft.palette'

function isPalette(value: unknown): value is Palette {
  return PALETTES.some((palette) => palette.id === value)
}

export function readPref(): Palette {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    // Anything unrecognised — a value left by an older or newer build, or by a
    // scheme that has since been removed — reads as the default rather than as
    // an attribute no stylesheet answers to.
    return isPalette(stored) ? stored : DEFAULT
  } catch {
    // Private mode, or storage disabled by policy.
    return DEFAULT
  }
}

/** What is actually on screen right now, which is the attribute rather than the
 *  preference: the inline script may have run before storage was readable. */
export function current(): Palette {
  const applied = document.documentElement.dataset.palette
  return isPalette(applied) ? applied : DEFAULT
}

export function applyPref(palette: Palette): void {
  try {
    if (palette === DEFAULT) window.localStorage.removeItem(STORAGE_KEY)
    else window.localStorage.setItem(STORAGE_KEY, palette)
  } catch {
    /* nothing to remember it with; the session still switches */
  }

  // Every painted pixel changes at once, exactly as it does on a mode switch,
  // so the same cross-fade covers it — and where the browser has no view
  // transitions, or motion is unwanted, it is a plain cut.
  crossFade(() => {
    if (palette === DEFAULT) delete document.documentElement.dataset.palette
    else document.documentElement.dataset.palette = palette
    syncChrome()
    // The tab icon is an href rather than a painted surface, so it is the one
    // mark in the panel a stylesheet cannot reach.
    syncFavicon()
    // A scheme moves --term-* and --shell-* as well, and the two canvases paint
    // outside CSS: telling them after the cross-fade would leave the previous
    // scheme's rectangle sitting in the middle of the dissolve.
    notifyColours()
  })
}
