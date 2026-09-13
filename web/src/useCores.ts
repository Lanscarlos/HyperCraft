import { useCallback, useEffect, useState } from 'react'

import { ApiError, api } from './api'
import { ask } from './confirm'
import type { CoreLibrary, DownloadJob, ServerCore } from './types'
import type { DownloadController } from './useDownloads'

export interface CoreController {
  library: CoreLibrary | null
  cores: ServerCore[]
  job: DownloadJob | null
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
export function useCores(enabled: boolean, downloads: DownloadController): CoreController {
  const [library, setLibrary] = useState<CoreLibrary | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // From the panel-wide queue rather than from this shelf's own listing. Before
  // this, showing a moving bar meant re-fetching the entire core library eight
  // times a second to read one byte count — and three other hooks were doing
  // the same to their own lists at the same time.
  const job = downloads.latest('core')
  const downloading = downloads.activeOf('core') > 0

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

  // The listing is re-read when a download stops, not while it runs: the only
  // thing that changes during one is the byte count, and that arrives on the
  // job. What changes at the end is the shelf.
  useEffect(() => {
    if (!enabled || downloading) return
    void refresh()
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
          await api.startCoreDownload({ project, version, overwrite })
          // Ask the queue at once rather than waiting for its next tick, so the
          // row appears under the button that was just pressed.
          await downloads.refresh()
        } catch (err) {
          // 409 is "that build is already in the library" — a re-download is
          // usually a repair after a bad file, worth offering and never worth
          // doing silently to a jar instances are being stamped out of.
          if (err instanceof ApiError && err.status === 409 && !overwrite) {
            const ok = await ask({
              title: '这个构建已经在核心库里了',
              lead: `${version} 的这一版之前下载过。`,
              detail: '重新下载会覆盖库里的那份文件。已经复制到实例目录里的副本不受影响。',
              confirmLabel: '重新下载',
            })
            if (!ok) return
            await api.startCoreDownload({ project, version, overwrite: true })
            await downloads.refresh()
            return
          }
          throw err
        }
      }, '下载失败').catch(() => undefined),
    [act, downloads],
  )

  const cancel = useCallback(
    () =>
      act(async () => {
        await downloads.cancel(job?.id ?? '')
        await refresh()
      }, '取消失败').catch(() => undefined),
    [act, refresh, downloads, job],
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
