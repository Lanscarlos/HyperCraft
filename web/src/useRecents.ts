import { useCallback, useEffect, useState } from 'react'

/** Enough to cover the servers one operator actually touches in a session.
 *  The sidebar has to stay readable at 30 instances, so it never grows. */
export const RECENT_LIMIT = 5

const KEY = 'hypercraft.recent-instances'

function read(): string[] {
  try {
    const raw = window.localStorage.getItem(KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : []
  } catch {
    return []
  }
}

/**
 * The handful of servers to put in the sidebar.
 *
 * The full list lives on its own page: an operator with thirty servers has a
 * sidebar that scrolls past the navigation otherwise, and the entry they want
 * is never the one the alphabet puts on top. What they want is the one they
 * were just in, so that is what the sidebar keeps.
 */
export function useRecents(): {
  recents: string[]
  remember: (id: string) => void
} {
  const [recents, setRecents] = useState<string[]>(read)

  useEffect(() => {
    window.localStorage.setItem(KEY, JSON.stringify(recents))
  }, [recents])

  const remember = useCallback((id: string) => {
    setRecents((prev) => {
      // Already at the front: the same object back, so opening the page you are
      // on does not re-render the sidebar.
      if (prev[0] === id) return prev
      return [id, ...prev.filter((entry) => entry !== id)].slice(0, RECENT_LIMIT)
    })
  }, [])

  return { recents, remember }
}
