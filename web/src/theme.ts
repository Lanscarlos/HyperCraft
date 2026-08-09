/**
 * Light/dark mode.
 *
 * Everything visual lives in CSS custom properties; this module only decides
 * which of the two token blocks in styles.css is active, by writing a resolved
 * `data-theme` of exactly "light" or "dark" onto <html>. Resolving here rather
 * than in a media query means one dark block in the stylesheet instead of two,
 * and it is also what lets "跟随系统" be a real third choice: the preference
 * that is stored is one of three, the attribute that is applied is one of two.
 *
 * index.html applies the same resolution inline before the bundle loads, so the
 * first paint is already in the right mode.
 */

export type ThemePref = 'system' | 'light' | 'dark'
export type Theme = 'light' | 'dark'

/** Shared with the inline script in index.html — changing it here alone would
 *  leave a stored preference stranded and flash the wrong mode on load. */
const STORAGE_KEY = 'hypercraft.theme'

const DARK_QUERY = '(prefers-color-scheme: dark)'

function isPref(value: unknown): value is ThemePref {
  return value === 'system' || value === 'light' || value === 'dark'
}

export function readPref(): ThemePref {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    return isPref(stored) ? stored : 'system'
  } catch {
    // Private mode, or storage disabled by policy. Following the system is the
    // right answer when we cannot remember anything else.
    return 'system'
  }
}

export function resolve(pref: ThemePref): Theme {
  if (pref !== 'system') return pref
  return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

/** The mode actually on screen right now. */
export function current(): Theme {
  return document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'
}

export function applyPref(pref: ThemePref): Theme {
  const theme = resolve(pref)
  document.documentElement.dataset.theme = theme
  try {
    // "system" is stored as the absence of a choice, so a machine that later
    // switches to dark follows along instead of being pinned to today's mode.
    if (pref === 'system') window.localStorage.removeItem(STORAGE_KEY)
    else window.localStorage.setItem(STORAGE_KEY, pref)
  } catch {
    /* nothing to remember it with; the session still switches */
  }
  for (const listener of listeners) listener(theme)
  return theme
}

type Listener = (theme: Theme) => void

const listeners = new Set<Listener>()

/** Notified after every switch, whether it came from the toggle or from the
 *  system changing under a "跟随系统" preference. Anything painting outside
 *  CSS — the xterm canvases — subscribes to repaint itself. */
export function onThemeChange(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** Called once at startup: re-resolves when the OS flips, but only while the
 *  preference is still "跟随系统". */
export function watchSystem(): void {
  window.matchMedia(DARK_QUERY).addEventListener('change', () => {
    if (readPref() === 'system') applyPref('system')
  })
}

/**
 * The xterm palette, read from the same tokens the rest of the panel uses.
 *
 * xterm paints to a canvas and cannot see CSS, so the values have to be handed
 * over as strings — but they are still read off :root rather than duplicated
 * here, which is what keeps the terminal from drifting when a token moves.
 *
 * Two palettes, because there are two terminals and they carry very different
 * authority: `server` is a Minecraft console, `shell` is a prompt on the whole
 * machine. They are told apart by colour before they are read, which is the
 * point — the failure mode is typing into the wrong one.
 */
export function terminalTheme(kind: 'server' | 'shell' = 'server') {
  const styles = getComputedStyle(document.documentElement)
  const token = (name: string) => styles.getPropertyValue(name).trim()
  const prefix = kind === 'shell' ? '--shell' : '--term'
  return {
    background: token(`${prefix}-bg`),
    foreground: token(`${prefix}-fg`),
    cursor: token(`${prefix}-cursor`),
    selectionBackground: token(`${prefix}-selection`),
    // Minecraft's log colours map onto the ANSI 16, and ANSI assumes a dark
    // canvas — which is why the terminal keeps one in both modes. Black would
    // otherwise be invisible, so it is lifted to a legible grey.
    black: token('--term-black'),
    brightBlack: token('--term-bright-black'),
    blue: token('--term-blue'),
    brightBlue: token('--term-bright-blue'),
  }
}
