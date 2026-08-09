import { useEffect, useState } from 'react'

/**
 * Whether a media query currently matches, kept in sync with the viewport.
 *
 * The shell has two forms of the same navigation — a rail beside the content on
 * a desktop, a drawer over it on a phone — and one button drives both. Which
 * one is on screen therefore has to be readable from JS, not only from CSS.
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches)

  useEffect(() => {
    const list = window.matchMedia(query)
    const onChange = () => setMatches(list.matches)
    // Read once on subscribe as well: the query can have flipped between the
    // first render and this effect (a rotated phone, a resized window).
    onChange()
    list.addEventListener('change', onChange)
    return () => list.removeEventListener('change', onChange)
  }, [query])

  return matches
}
