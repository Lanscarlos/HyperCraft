import { useCallback, useEffect, useState } from 'react'

import { api } from './api'
import type {
  DatabaseInstallJob,
  DatabaseOverview,
  DatabaseService,
  DatabaseVersion,
  NewDatabase,
} from './types'

/** Cadence while an install runs, for a progress bar that moves. */
const ACTIVE_POLL_MS = 800

/** Cadence while a database is starting or stopping. Slower than the install
 *  poll: nothing here has a percentage, only a state that flips once. */
const TRANSITION_POLL_MS = 1500

export interface DatabaseController {
  overview: DatabaseOverview | null
  /** Installable versions per engine, fetched on demand — three engines means
   *  three upstreams, and fetching all of them to draw one page would make the
   *  page as slow as the slowest of them. */
  versions: Record<string, DatabaseVersion[]>
  job: DatabaseInstallJob | null
  /** True while an engine is downloading or extracting. */
  installing: boolean
  /** True while any database is starting or stopping. */
  transitioning: boolean
  /** True while one of the actions below is in flight. */
  busy: boolean
  error: string | null
  clearError: () => void
  refresh: () => Promise<void>
  loadVersions: (engine: string) => Promise<void>
  install: (engine: string, version: string) => Promise<void>
  cancelInstall: () => Promise<void>
  removeEngine: (id: string) => Promise<void>
  create: (input: NewDatabase) => Promise<DatabaseService | null>
  start: (id: string) => Promise<void>
  stop: (id: string) => Promise<void>
  update: (id: string, input: Partial<DatabaseService>) => Promise<void>
  remove: (id: string, deleteData: boolean) => Promise<void>
  logs: (id: string) => Promise<string[]>
}

/**
 * Tracks the panel's databases.
 *
 * Polled at the app level rather than inside the page, for the same reason
 * useJava is: an install belongs to the daemon and keeps running after you
 * navigate away, and so does a database — the sidebar has to be able to say a
 * database is up while you are looking at a server's console.
 */
export function useDatabases(enabled: boolean): DatabaseController {
  const [overview, setOverview] = useState<DatabaseOverview | null>(null)
  const [versions, setVersions] = useState<Record<string, DatabaseVersion[]>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const job = overview?.job ?? null
  const installing = job?.state === 'downloading' || job?.state === 'extracting'
  const transitioning = (overview?.services ?? []).some(
    (service) => service.state === 'starting' || service.state === 'stopping',
  )

  const refresh = useCallback(async () => {
    try {
      setOverview(await api.databaseOverview())
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取数据库列表失败')
    }
  }, [])

  const loadVersions = useCallback(async (engine: string) => {
    try {
      const list = await api.databaseVersions(engine)
      setVersions((prev) => ({ ...prev, [engine]: list }))
    } catch {
      // The installable list comes from upstream; without it the page still
      // shows what is installed and what is running, which is the half that
      // matters on a machine with no route out.
      setVersions((prev) => ({ ...prev, [engine]: [] }))
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    void refresh()
  }, [enabled, refresh])

  useEffect(() => {
    if (!enabled || (!installing && !transitioning)) return
    const interval = installing ? ACTIVE_POLL_MS : TRANSITION_POLL_MS
    const timer = window.setInterval(() => void refresh(), interval)
    return () => window.clearInterval(timer)
  }, [enabled, installing, transitioning, refresh])

  // A finished install changes the 已安装 flags on the version list.
  useEffect(() => {
    if (job?.state !== 'done' || !job.engine) return
    void loadVersions(job.engine)
  }, [job?.state, job?.engine, job?.installId, loadVersions])

  const act = useCallback(async <T,>(action: () => Promise<T>, fallback: string) => {
    setBusy(true)
    setError(null)
    try {
      return await action()
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback)
      return null
    } finally {
      setBusy(false)
    }
  }, [])

  const install = useCallback(
    async (engine: string, version: string) => {
      await act(async () => {
        const started = await api.installDatabaseEngine(engine, version)
        // Show the job immediately; the poll takes over from here.
        setOverview((prev) => (prev ? { ...prev, job: started } : prev))
      }, '安装失败')
    },
    [act],
  )

  const cancelInstall = useCallback(async () => {
    await act(async () => {
      await api.cancelDatabaseInstall()
      await refresh()
    }, '取消失败')
  }, [act, refresh])

  const removeEngine = useCallback(
    async (id: string) => {
      await act(async () => {
        await api.deleteDatabaseEngine(id)
        await refresh()
      }, '删除失败')
    },
    [act, refresh],
  )

  const create = useCallback(
    (input: NewDatabase) =>
      act(async () => {
        const created = await api.createDatabase(input)
        await refresh()
        return created
      }, '创建失败'),
    [act, refresh],
  )

  // Starting waits for the engine to open its port, so this request is slow on
  // purpose: when it comes back the answer is real.
  const start = useCallback(
    async (id: string) => {
      await act(async () => {
        await api.startDatabase(id)
        await refresh()
      }, '启动失败')
    },
    [act, refresh],
  )

  const stop = useCallback(
    async (id: string) => {
      await act(async () => {
        await api.stopDatabase(id)
        await refresh()
      }, '停止失败')
    },
    [act, refresh],
  )

  const update = useCallback(
    async (id: string, input: Partial<DatabaseService>) => {
      await act(async () => {
        await api.updateDatabase(id, input)
        await refresh()
      }, '保存失败')
    },
    [act, refresh],
  )

  const remove = useCallback(
    async (id: string, deleteData: boolean) => {
      await act(async () => {
        await api.deleteDatabase(id, deleteData)
        await refresh()
      }, '删除失败')
    },
    [act, refresh],
  )

  const logs = useCallback(async (id: string) => {
    try {
      return (await api.databaseLogs(id)).lines
    } catch {
      return []
    }
  }, [])

  return {
    overview,
    versions,
    job,
    installing,
    transitioning,
    busy,
    error,
    clearError: useCallback(() => setError(null), []),
    refresh,
    loadVersions,
    install,
    cancelInstall,
    removeEngine,
    create,
    start,
    stop,
    update,
    remove,
    logs,
  }
}
