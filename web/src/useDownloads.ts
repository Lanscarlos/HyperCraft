import { useCallback, useEffect, useMemo, useState } from 'react'

import { api } from './api'
import type { DownloadJob, DownloadKind, DownloadsView } from './types'
import { isDownloadActive } from './types'

/** While something is coming down. The bar has to move. */
const ACTIVE_POLL_MS = 800

export interface DownloadController {
  /** The queue and its history, newest first. */
  jobs: DownloadJob[]
  /** How many are still going. The daemon's count, not a re-derived one: it is
   *  filtered by what this account may see, and a number that disagrees with
   *  the list is a number that says something the list would not. */
  active: number
  busy: boolean
  error: string | null
  refresh: () => Promise<void>
  cancel: (id: string) => Promise<void>
  clearFinished: () => Promise<void>
  /** This shelf's jobs, for the pages that still show their own progress. */
  of: (kind: DownloadKind) => DownloadJob[]
  /** The newest job of one shelf, which is what those pages used to keep. */
  latest: (kind: DownloadKind) => DownloadJob | null
  /** How many of one shelf's jobs are still going, for the sidebar badges. */
  activeOf: (kind: DownloadKind) => number
}

const EMPTY: DownloadsView = { jobs: [], active: 0 }

/**
 * Every download the panel is doing, as one source.
 *
 * This replaces four hooks that each polled their whole list every 800ms
 * whether or not anything was happening — installing one JDK meant re-fetching
 * the complete runtime inventory eight times a second to read one byte count,
 * and all four ran at the app level whichever page was open. Here there is one
 * request, it carries only jobs, and it stops when the queue is quiet.
 *
 * Polled at the app level for the same reason the queue lives in the daemon: a
 * download belongs to the panel, not to whichever page happened to start it.
 */
export function useDownloads(enabled: boolean): DownloadController {
  const [view, setView] = useState<DownloadsView>(EMPTY)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    if (!enabled) return
    try {
      setView(await api.downloads())
      setError(null)
    } catch (err) {
      // A failed poll is not worth a toast: the next one is 800ms away, and a
      // bar that freezes for one tick is less alarming than an error that
      // appears and clears itself. A failure the operator *acted* on — cancel,
      // clear — does get reported, below.
      void err
    }
  }, [enabled])

  // One fetch whenever the hook wakes, so the history is there before anything
  // is downloaded: the page has to be readable when the queue is empty.
  useEffect(() => {
    if (!enabled) {
      setView(EMPTY)
      return
    }
    void refresh()
  }, [enabled, refresh])

  // The interval exists only while something is actually coming down. This is
  // the whole point of the rewrite — the four hooks it replaces polled forever.
  useEffect(() => {
    if (!enabled || view.active === 0) return
    const timer = window.setInterval(() => void refresh(), ACTIVE_POLL_MS)
    return () => window.clearInterval(timer)
  }, [enabled, view.active, refresh])

  const cancel = useCallback(
    async (id: string) => {
      setBusy(true)
      try {
        await api.cancelDownload(id)
        await refresh()
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setBusy(false)
      }
    },
    [refresh],
  )

  const clearFinished = useCallback(async () => {
    setBusy(true)
    try {
      setView(await api.clearDownloads())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }, [])

  // Grouped once per change rather than filtered at each call site: the sidebar
  // asks for four counts on every render.
  const byKind = useMemo(() => {
    const out = new Map<DownloadKind, DownloadJob[]>()
    for (const job of view.jobs) {
      const list = out.get(job.kind)
      if (list) list.push(job)
      else out.set(job.kind, [job])
    }
    return out
  }, [view.jobs])

  const of = useCallback((kind: DownloadKind) => byKind.get(kind) ?? [], [byKind])
  const latest = useCallback((kind: DownloadKind) => byKind.get(kind)?.[0] ?? null, [byKind])
  const activeOf = useCallback(
    (kind: DownloadKind) => (byKind.get(kind) ?? []).filter((job) => isDownloadActive(job.state)).length,
    [byKind],
  )

  return {
    jobs: view.jobs,
    active: view.active,
    busy,
    error,
    refresh,
    cancel,
    clearFinished,
    of,
    latest,
    activeOf,
  }
}
