import { useEffect, useState } from 'react'

import { api } from './api'
import type { SystemInfo } from './types'

/** The collector samples every five seconds; anything faster re-reads a point. */
const POLL_INTERVAL_MS = 5000

export interface SystemController {
  info: SystemInfo | null
  /** Set only when the very first read failed; a later failure keeps the last
   *  good snapshot rather than blanking the page. */
  error: string | null
}

/**
 * Machine-level numbers, polled once for the whole app.
 *
 * They are read in three places at once — the overview's summary bar, its
 * alert list, and the host pages — and three copies of the same five-second
 * poll is three times the work for one answer. Hoisting it also means the disk
 * alert on the home page and the disk page itself can never disagree.
 */
export function useSystem(enabled: boolean): SystemController {
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!enabled) return
    let cancelled = false

    const load = async () => {
      try {
        const fetched = await api.system()
        if (cancelled) return
        setInfo(fetched)
        setError(null)
      } catch (err) {
        if (cancelled) return
        setError(err instanceof Error ? err.message : '读取本机状态失败')
      }
    }

    void load()
    const timer = window.setInterval(() => void load(), POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [enabled])

  return { info, error }
}
