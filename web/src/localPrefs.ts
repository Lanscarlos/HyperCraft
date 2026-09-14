/**
 * Small per-browser view preferences — column widths, which folders are open,
 * how dense a list is.
 *
 * Deliberately not panel state: none of this is worth a round trip, none of it
 * should follow an account onto somebody else's screen, and all of it should
 * survive a reload. localStorage is exactly that shape.
 *
 * Every read is guarded, and the guard is not defensive noise. A private
 * window, a profile with site data blocked, and a value some earlier version
 * of the panel wrote in another shape all arrive the same way — as a throw or
 * as JSON that does not parse — and the answer to all three is the default,
 * not a blank page.
 */
export function readPref<T>(key: string, fallback: T): T {
  try {
    const raw = window.localStorage.getItem(key)
    if (raw === null) return fallback
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

export function writePref(key: string, value: unknown): void {
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // A full or disabled store is not a reason to fail the interaction that
    // asked for the write. The preference is lost; the page is not.
  }
}
