import { useEffect, useMemo, useState } from 'react'

import { api } from './api'

/** How many servers the overview will poll for a memory bar. Past this the
 *  cards fall back to the configured -Xmx, which needs no request at all: a
 *  home page that fires thirty requests every five seconds is a home page that
 *  makes the machine it is reporting on slower. */
const MAX_WATCHED = 8

const POLL_INTERVAL_MS = 5000

export interface LiveMetric {
  cpuPercent: number
  memoryBytes: number
  /** The -Xmx the process was started with, in bytes; 0 when unset. */
  xmxBytes: number
  cpuCores: number
}

/**
 * The newest sample for several servers at once.
 *
 * The per-instance metrics endpoint is per instance, so the overview's cards
 * need one call each. Keeping that here means one timer for the page instead
 * of one per card, and one place to decide how many cards are worth the calls.
 */
export function useLiveMetrics(ids: string[], enabled: boolean): Record<string, LiveMetric> {
  const [metrics, setMetrics] = useState<Record<string, LiveMetric>>({})

  // The caller builds this array by filtering, so it is a new array every
  // render; the effect has to depend on its contents, not its identity.
  const key = ids.slice(0, MAX_WATCHED).join(',')
  const watched = useMemo(() => (key === '' ? [] : key.split(',')), [key])

  useEffect(() => {
    if (!enabled || watched.length === 0) {
      setMetrics((prev) => (Object.keys(prev).length === 0 ? prev : {}))
      return
    }
    let cancelled = false

    const load = async () => {
      const results = await Promise.all(
        watched.map(async (id) => {
          try {
            const data = await api.instanceMetrics(id)
            const latest = data.samples[data.samples.length - 1]
            if (!latest) return null
            return [
              id,
              {
                cpuPercent: latest.cpuPercent,
                memoryBytes: latest.memoryBytes,
                xmxBytes: data.maxMemoryMB > 0 ? data.maxMemoryMB * 1024 * 1024 : 0,
                cpuCores: data.cpuCores,
              },
            ] as const
          } catch {
            // One server's metrics failing must not blank the other cards.
            return null
          }
        }),
      )
      if (cancelled) return
      setMetrics(Object.fromEntries(results.filter((entry) => entry !== null)))
    }

    void load()
    const timer = window.setInterval(() => void load(), POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [watched, enabled])

  return metrics
}
