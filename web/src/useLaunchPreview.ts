import { useEffect, useRef, useState } from 'react'

import { api } from './api'
import type { LaunchPreview, StartupDraft } from './types'

/** How long after the last keystroke to ask. Long enough that typing a
 *  four-digit heap is one request, short enough that the command still reads
 *  as following the form rather than lagging it. */
const SETTLE_MS = 300

/**
 * The command line and checks for a draft nobody has saved.
 *
 * Keeps the last good answer through a refresh and through a failure. A
 * command that blinks out while you type is worse than one a beat behind, and
 * a preview that fails must never be able to stop you saving — it is a mirror,
 * not a gate.
 */
export function useLaunchPreview(id: string, draft: StartupDraft) {
  const [preview, setPreview] = useState<LaunchPreview | null>(null)
  const [stale, setStale] = useState(true)
  const [failed, setFailed] = useState(false)
  // Serialised so the effect compares by value: the draft object is rebuilt on
  // every render and would otherwise refire on keystrokes that changed nothing.
  const key = JSON.stringify(draft)
  // The first ask is not a debounce — there is nothing to settle yet.
  const settled = useRef(false)

  useEffect(() => {
    const controller = new AbortController()
    setStale(true)
    const ask = () => {
      api
        .launchPreview(id, JSON.parse(key) as StartupDraft, controller.signal)
        .then((next) => {
          setPreview(next)
          setFailed(false)
          setStale(false)
        })
        .catch((err: unknown) => {
          // An abort is this effect being superseded, not a failure to report.
          if (err instanceof DOMException && err.name === 'AbortError') return
          setFailed(true)
          setStale(false)
        })
    }
    if (!settled.current) {
      settled.current = true
      ask()
      return () => controller.abort()
    }
    const timer = window.setTimeout(ask, SETTLE_MS)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [id, key])

  return { preview, stale, failed }
}
