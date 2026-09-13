import { useCallback, useEffect, useState } from 'react'

import { api } from './api'
import type { DownloadController } from './useDownloads'
import type {
  DownloadJob,
  JavaDistribution,
  JavaMajor,
  JavaOverview,
} from './types'

export interface JavaController {
  overview: JavaOverview | null
  majors: JavaMajor[]
  /** The OpenJDK builds an install can pick from, default first, each with
   *  its own download sources. */
  distributions: JavaDistribution[]
  job: DownloadJob | null
  /** True while an install is downloading or extracting. */
  installing: boolean
  /** True while one of the actions below is in flight. */
  busy: boolean
  error: string | null
  clearError: () => void
  install: (
    distribution: string,
    major: number,
    imageType: 'jre' | 'jdk',
    source: string,
  ) => Promise<void>
  cancel: () => Promise<void>
  remove: (id: string) => Promise<void>
  /** Registers a Java already on this machine. detected marks one the panel
   *  proposed off PATH and the operator accepted, rather than typed. */
  register: (path: string, detected?: boolean) => Promise<void>
  unregister: (id: string) => Promise<void>
  /** Re-reads the version of a registered path, for a JDK upgraded in place. */
  reprobe: (id: string) => Promise<void>
}

/**
 * Tracks the panel's Java runtimes.
 *
 * Polled at the app level rather than inside the Java page, for the same
 * reason the update check is: an install belongs to the daemon and keeps
 * running after you navigate away, so the sidebar needs to know it is still
 * going. The page reads this state, it does not own it.
 */
export function useJava(enabled: boolean, downloads: DownloadController): JavaController {
  const [overview, setOverview] = useState<JavaOverview | null>(null)
  const [majors, setMajors] = useState<JavaMajor[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // From the panel-wide queue rather than from this shelf's own listing: while
  // an install runs the only thing changing is the byte count, and re-fetching
  // the whole runtime inventory eight times a second to read it was the cost
  // this replaces.
  const job = downloads.latest('java')
  const installing = downloads.activeOf('java') > 0

  const refresh = useCallback(async () => {
    try {
      setOverview(await api.javaOverview())
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取 Java 列表失败')
    }
  }, [])

  // Which versions exist depends on the distribution — Zulu ships 13, 14 and
  // 15 and Adoptium does not — so this list has to be refetched when it
  // changes, not just on mount.
  const distribution = overview?.distribution ?? ''

  const refreshMajors = useCallback(async () => {
    if (!distribution) return
    try {
      setMajors(await api.javaMajors(distribution))
    } catch {
      // The installable list comes from the distribution's own API; without it
      // the page still shows what is installed, which is the half that matters
      // offline.
      setMajors([])
    }
  }, [distribution])

  useEffect(() => {
    if (!enabled) return
    void refresh()
    void refreshMajors()
  }, [enabled, refresh, refreshMajors])

  // Re-read when an install stops, not while it runs: what changes at the end
  // is the shelf, and that is what this listing is.
  useEffect(() => {
    if (!enabled || installing) return
    void refresh()
  }, [enabled, installing, refresh])

  // A finished install changes the "已安装" flags. The runtimes list itself
  // arrived with the poll that saw the job finish.
  useEffect(() => {
    if (job?.state !== 'done') return
    void refreshMajors()
  }, [job?.state, job?.ref, refreshMajors])

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
    (distribution: string, major: number, imageType: 'jre' | 'jdk', source: string) =>
      act(async () => {
        const started = await api.installJava(distribution, major, imageType, source)
        // Show the job immediately; the poll takes over from here.
        setOverview((prev) => (prev ? { ...prev, job: started } : prev))
      }, '安装失败'),
    [act],
  )

  const cancel = useCallback(
    () =>
      act(async () => {
        await downloads.cancel(job?.id ?? '')
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

  const register = useCallback(
    (path: string, detected = false) =>
      act(async () => {
        await api.registerJava(path, detected)
        await refresh()
      }, '登记失败'),
    [act, refresh],
  )

  const unregister = useCallback(
    (id: string) =>
      act(async () => {
        await api.unregisterJava(id)
        await refresh()
      }, '删除失败'),
    [act, refresh],
  )

  const reprobe = useCallback(
    (id: string) =>
      act(async () => {
        await api.probeJava(id)
        await refresh()
      }, '重新检测失败'),
    [act, refresh],
  )

  return {
    overview,
    majors,
    distributions: overview?.distributions ?? [],
    job,
    installing,
    busy,
    error,
    clearError: useCallback(() => setError(null), []),
    install,
    cancel,
    remove,
    register,
    unregister,
    reprobe,
  }
}
