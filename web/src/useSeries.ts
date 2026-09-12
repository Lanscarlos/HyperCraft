import { useEffect, useState } from 'react'

import { api } from './api'
import type { InstanceMetrics } from './types'

/** The daemon samples every five seconds; asking faster only returns the same
 *  reading twice. */
const POLL_MS = 5000

/**
 * One instance's resource samples, polled while the caller is in front.
 *
 * Lifted out of InstanceCockpit because the status strip in the top bar reads
 * the same numbers the cockpit's tiles do, and it is on screen on every
 * instance page. Two components each owning an interval against the same
 * endpoint would double the daemon's sampling load to show one readout twice,
 * so the poll lives above both of them and the samples come down as a prop.
 *
 * `id` is nullable for the caller that does not always have an instance — the
 * top bar is mounted on panel-wide pages too, and a hook cannot be called
 * conditionally.
 */
export function useSeries(id: string | null, enabled: boolean): InstanceMetrics | null {
  const [data, setData] = useState<InstanceMetrics | null>(null)

  useEffect(() => {
    // Cleared on every change of instance, not only when the poll stops: the
    // samples on screen belong to the server that was selected a moment ago,
    // and showing them under the new server's name is worse than showing
    // nothing while the first response is in flight.
    setData(null)
    if (!enabled || id === null) return
    let cancelled = false

    const load = async () => {
      try {
        const fetched = await api.instanceMetrics(id)
        if (!cancelled) setData(fetched)
      } catch {
        // Informational; the console itself reports a lost connection.
      }
    }

    void load()
    const timer = window.setInterval(() => void load(), POLL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [id, enabled])

  return data
}
