import { useCallback, useEffect, useState } from 'react'

import { ApiError, api } from './api'
import type { CoreDownloadJob, CoreLibrary, ServerCore } from './types'

/** Cadence while a download runs, for a progress bar that moves. */
const ACTIVE_POLL_MS = 800

export interface CoreController {
  library: CoreLibrary | null
  cores: ServerCore[]
  job: CoreDownloadJob | null
  /** True while a core is coming down. */
  downloading: boolean
  /** True while one of the actions below is in flight. */
  busy: boolean
  error: string | null
  refresh: () => Promise<void>
  download: (project: string, version: string, overwrite?: boolean) => Promise<void>
  cancel: () => Promise<void>
  remove: (id: string) => Promise<void>
}

/**
 * Tracks the panel's server core library.
 *
 * Polled at the app level for the same reason the Java installs are: a download
 * belongs to the daemon and keeps running after you navigate away, so the
 * sidebar can say so while it does. The pages read this state, they do not own
 * it.
 */
export function useCores(enabled: boolean): CoreController {
  const [library, setLibrary] = useState<CoreLibrary | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const job = library?.job ?? null
  const downloading = job?.state === 'downloading'

  const refresh = useCallback(async () => {
    try {
      setLibrary(await api.coreLibrary())
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取核心库失败')
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    void refresh()
  }, [enabled, refresh])

  useEffect(() => {
    if (!enabled || !downloading) return
    const timer = window.setInterval(() => void refresh(), ACTIVE_POLL_MS)
    return () => window.clearInterval(timer)
  }, [enabled, downloading, refresh])

  const act = useCallback(async (action: () => Promise<void>, fallback: string) => {
    setBusy(true)
    setError(null)
    try {
      await action()
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback)
      throw err
    } finally {
      setBusy(false)
    }
  }, [])

  const download = useCallback(
    (project: string, version: string, overwrite = false) =>
      act(async () => {
        try {
          const started = await api.startCoreDownload({ project, version, overwrite })
          // Show the job immediately; the poll takes over from here.
          setLibrary((prev) => (prev ? { ...prev, job: started } : prev))
        } catch (err) {
          // 409 is "that build is already in the library" — a re-download is
          // usually a repair after a bad file, worth offering and never worth
          // doing silently to a jar instances are being stamped out of.
          if (err instanceof ApiError && err.status === 409 && !overwrite) {
            if (!window.confirm(`${version} 的这个构建已经在核心库里了，要重新下载并覆盖吗？`)) {
              return
            }
            const started = await api.startCoreDownload({ project, version, overwrite: true })
            setLibrary((prev) => (prev ? { ...prev, job: started } : prev))
            return
          }
          throw err
        }
      }, '下载失败').catch(() => undefined),
    [act],
  )

  const cancel = useCallback(
    () =>
      act(async () => {
        await api.cancelCoreDownload()
        await refresh()
      }, '取消失败').catch(() => undefined),
    [act, refresh],
  )

  const remove = useCallback(
    (id: string) =>
      act(async () => {
        await api.deleteCore(id)
        await refresh()
      }, '删除失败').catch(() => undefined),
    [act, refresh],
  )

  return {
    library,
    cores: library?.cores ?? [],
    job,
    downloading,
    busy,
    error,
    refresh,
    download,
    cancel,
    remove,
  }
}
