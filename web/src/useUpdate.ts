import { useCallback, useEffect, useRef, useState } from 'react'

import { api, panelVersion } from './api'
import type { UpdateStatus } from './types'

/** Idle cadence. The panel caches its own GitHub check, so this only pulls a
 *  value that is already in memory; it exists to notice a check that ran on the
 *  server timer while this page was open. */
const IDLE_POLL_MS = 5 * 60 * 1000

/** Cadence once an update is running, for a progress bar that moves. */
const ACTIVE_POLL_MS = 1500

export interface UpdateController {
  status: UpdateStatus | null
  /** True from the moment an update is accepted until the page reloads. */
  updating: boolean
  /** True once the panel has stopped answering, i.e. it is restarting. */
  restarting: boolean
  error: string | null
  checking: boolean
  check: () => Promise<void>
  apply: () => Promise<void>
}

/**
 * Tracks the panel's update state.
 *
 * The interesting part is what happens after an update is accepted: the panel
 * stops the servers, replaces its own binary and re-execs, so it stops
 * answering for a few seconds. Rather than showing an error, this polls the
 * unauthenticated health endpoint and reloads the page as soon as a different
 * version answers — which is also the moment the session is known to still be
 * valid, since sessions live in memory and do not survive the restart.
 */
export function useUpdate(enabled: boolean): UpdateController {
  const [status, setStatus] = useState<UpdateStatus | null>(null)
  const [updating, setUpdating] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // The version the page was loaded against. A health response that differs
  // from it means the new binary is serving.
  const bootVersion = useRef<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const next = await api.updateStatus()
      setStatus(next)
      if (bootVersion.current === null) bootVersion.current = next.currentVersion
      return next
    } catch {
      // A failure here during an update is expected: the panel is restarting.
      return null
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    void refresh()
  }, [enabled, refresh])

  // Idle polling.
  useEffect(() => {
    if (!enabled || updating) return
    const timer = window.setInterval(() => void refresh(), IDLE_POLL_MS)
    return () => window.clearInterval(timer)
  }, [enabled, updating, refresh])

  // Active polling: progress, then watch for the panel coming back.
  useEffect(() => {
    if (!updating) return
    let cancelled = false

    const tick = async () => {
      const live = await panelVersion()
      if (cancelled) return

      if (live === null) {
        // Unreachable: the swap and re-exec are happening now.
        setRestarting(true)
      } else if (bootVersion.current && live !== bootVersion.current) {
        // A different version is answering: the update landed. Reload rather
        // than patching state, so every component sees the new build.
        window.location.reload()
        return
      } else {
        // Still the old binary and still answering: the download is running.
        const next = await refresh()
        if (cancelled) return
        if (next && next.phase === 'idle' && next.error) {
          // The update failed before the restart; the panel is still the old
          // version and usable.
          setUpdating(false)
          setRestarting(false)
          setError(next.error)
        }
      }
    }

    const timer = window.setInterval(() => void tick(), ACTIVE_POLL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [updating, refresh])

  const check = useCallback(async () => {
    setChecking(true)
    setError(null)
    try {
      const next = await api.checkUpdate()
      setStatus(next)
      if (next.checkError) setError(next.checkError)
    } catch (err) {
      setError(err instanceof Error ? err.message : '检查更新失败')
    } finally {
      setChecking(false)
    }
  }, [])

  const apply = useCallback(async () => {
    setError(null)
    try {
      const next = await api.applyUpdate()
      setStatus(next)
      setUpdating(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新失败')
    }
  }, [])

  return { status, updating, restarting, error, checking, check, apply }
}
