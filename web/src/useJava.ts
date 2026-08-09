import { useCallback, useEffect, useState } from 'react'

import { api } from './api'
import type { JavaInstallJob, JavaMajor, JavaOverview } from './types'

/** Cadence while an install runs, for a progress bar that moves. */
const ACTIVE_POLL_MS = 800

export interface JavaController {
  overview: JavaOverview | null
  majors: JavaMajor[]
  job: JavaInstallJob | null
  /** True while an install is downloading or extracting. */
  installing: boolean
  /** True while one of the actions below is in flight. */
  busy: boolean
  error: string | null
  clearError: () => void
  install: (major: number, imageType: 'jre' | 'jdk') => Promise<void>
  cancel: () => Promise<void>
  remove: (id: string) => Promise<void>
}

/**
 * Tracks the panel's Java runtimes.
 *
 * Polled at the app level rather than inside the Java page, for the same
 * reason the update check is: an install belongs to the daemon and keeps
 * running after you navigate away, so the sidebar needs to know it is still
 * going. The page reads this state, it does not own it.
 */
export function useJava(enabled: boolean): JavaController {
  const [overview, setOverview] = useState<JavaOverview | null>(null)
  const [majors, setMajors] = useState<JavaMajor[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const job = overview?.job ?? null
  const installing = job?.state === 'downloading' || job?.state === 'extracting'

  const refresh = useCallback(async () => {
    try {
      setOverview(await api.javaOverview())
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取 Java 列表失败')
    }
  }, [])

  const refreshMajors = useCallback(async () => {
    try {
      setMajors(await api.javaMajors())
    } catch {
      // The installable list comes from Adoptium; without it the page still
      // shows what is installed, which is the half that matters offline.
      setMajors([])
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    void refresh()
    void refreshMajors()
  }, [enabled, refresh, refreshMajors])

  useEffect(() => {
    if (!enabled || !installing) return
    const timer = window.setInterval(() => void refresh(), ACTIVE_POLL_MS)
    return () => window.clearInterval(timer)
  }, [enabled, installing, refresh])

  // A finished install changes the "已安装" flags. The runtimes list itself
  // arrived with the poll that saw the job finish.
  useEffect(() => {
    if (job?.state !== 'done') return
    void refreshMajors()
  }, [job?.state, job?.runtimeId, refreshMajors])

  const act = useCallback(async (action: () => Promise<void>, fallback: string) => {
    setBusy(true)
    setError(null)
    try {
      await action()
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback)
    } finally {
      setBusy(false)
    }
  }, [])

  const install = useCallback(
    (major: number, imageType: 'jre' | 'jdk') =>
      act(async () => {
        const started = await api.installJava(major, imageType)
        // Show the job immediately; the poll takes over from here.
        setOverview((prev) => (prev ? { ...prev, job: started } : prev))
      }, '安装失败'),
    [act],
  )

  const cancel = useCallback(
    () =>
      act(async () => {
        await api.cancelJavaInstall()
        await refresh()
      }, '取消失败'),
    [act, refresh],
  )

  const remove = useCallback(
    (id: string) =>
      act(async () => {
        await api.deleteJavaRuntime(id)
        await refresh()
        await refreshMajors()
      }, '删除失败'),
    [act, refresh, refreshMajors],
  )

  return {
    overview,
    majors,
    job,
    installing,
    busy,
    error,
    clearError: useCallback(() => setError(null), []),
    install,
    cancel,
    remove,
  }
}
