import { useCallback, useEffect, useState } from 'react'

import { api } from './api'
import type { LibraryPlugin, PluginDownloadJob, PluginLibrary } from './types'
import { hasPluginUpdate } from './types'

/** Cadence while a download runs, for a progress bar that moves. */
const ACTIVE_POLL_MS = 800

export interface PluginInput {
  name: string
  repo: string
  assetPattern?: string
  prerelease?: boolean
  private?: boolean
  targetDir?: string
  note?: string
}

export interface PluginController {
  library: PluginLibrary | null
  plugins: LibraryPlugin[]
  job: PluginDownloadJob | null
  /** True while a plugin jar is coming down. */
  downloading: boolean
  /** How many tracked plugins have a release nobody has downloaded yet. */
  updates: number
  /** True while one of the actions below is in flight. */
  busy: boolean
  error: string | null
  clearError: () => void
  refresh: () => Promise<void>
  add: (input: PluginInput) => Promise<boolean>
  edit: (id: string, input: PluginInput) => Promise<boolean>
  remove: (id: string) => Promise<void>
  check: (id: string) => Promise<void>
  checkAll: () => Promise<void>
  download: (id: string, tag: string) => Promise<void>
  cancel: () => Promise<void>
  removeVersion: (id: string, tag: string) => Promise<void>
  /** Stores the GitHub token, or clears it with an empty string. */
  setToken: (token: string) => Promise<boolean>
  /** Chooses the download mirror, by id or as a custom URL prefix. */
  setMirror: (mirror: string) => Promise<boolean>
}

/**
 * Tracks the panel-wide plugin library.
 *
 * Polled at the app level like the core and Java jobs are: a download belongs
 * to the daemon and keeps running after you navigate away, so the sidebar can
 * say so while it does. Update checks are deliberately not polled — the
 * anonymous GitHub API allows 60 calls an hour, and a page that refreshed them
 * on its own would spend that budget on nobody's behalf.
 */
export function usePlugins(enabled: boolean): PluginController {
  const [library, setLibrary] = useState<PluginLibrary | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const job = library?.job ?? null
  const downloading = job?.state === 'downloading'
  const plugins = library?.plugins ?? []
  const updates = plugins.filter(hasPluginUpdate).length

  const refresh = useCallback(async () => {
    try {
      setLibrary(await api.pluginLibrary())
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取插件库失败')
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

  const act = useCallback(async <T,>(action: () => Promise<T>, fallback: string) => {
    setBusy(true)
    setError(null)
    try {
      return await action()
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback)
      throw err
    } finally {
      setBusy(false)
    }
  }, [])

  const add = useCallback(
    (input: PluginInput) =>
      act(async () => {
        await api.addPlugin(input)
        await refresh()
        return true
      }, '添加插件失败').catch(() => false),
    [act, refresh],
  )

  const edit = useCallback(
    (id: string, input: PluginInput) =>
      act(async () => {
        await api.editPlugin(id, input)
        await refresh()
        return true
      }, '保存失败').catch(() => false),
    [act, refresh],
  )

  const remove = useCallback(
    (id: string) =>
      act(async () => {
        await api.deletePlugin(id)
        await refresh()
      }, '删除失败').catch(() => undefined),
    [act, refresh],
  )

  const check = useCallback(
    (id: string) =>
      act(async () => {
        await api.checkPlugin(id)
      }, '检查更新失败')
        .catch(() => undefined)
        // Whether or not the check succeeded, the panel recorded that it was
        // tried, and the card should show that rather than a stale timestamp.
        .finally(() => void refresh()),
    [act, refresh],
  )

  const checkAll = useCallback(
    () =>
      act(async () => {
        setLibrary(await api.checkPlugins())
      }, '检查更新失败').catch(() => undefined),
    [act],
  )

  const download = useCallback(
    (id: string, tag: string) =>
      act(async () => {
        const started = await api.downloadPlugin(id, tag)
        // Show the job immediately; the poll takes over from here.
        setLibrary((prev) => (prev ? { ...prev, job: started } : prev))
      }, '下载失败').catch(() => undefined),
    [act],
  )

  const cancel = useCallback(
    () =>
      act(async () => {
        await api.cancelPluginDownload()
        await refresh()
      }, '取消失败').catch(() => undefined),
    [act, refresh],
  )

  const removeVersion = useCallback(
    (id: string, tag: string) =>
      act(async () => {
        await api.deletePluginVersion(id, tag)
        await refresh()
      }, '删除版本失败').catch(() => undefined),
    [act, refresh],
  )

  const setToken = useCallback(
    (token: string) =>
      act(async () => {
        // The response is the library as it looks with the new token, so the
        // "已配置" line updates without a second round trip.
        setLibrary(await api.setPluginToken(token))
        return true
      }, '保存访问令牌失败').catch(() => false),
    [act],
  )

  const setMirror = useCallback(
    (mirror: string) =>
      act(async () => {
        setLibrary(await api.setPluginMirror(mirror))
        return true
      }, '保存下载源失败').catch(() => false),
    [act],
  )

  return {
    library,
    plugins,
    job,
    downloading,
    updates,
    busy,
    error,
    clearError: () => setError(null),
    refresh,
    add,
    edit,
    remove,
    check,
    checkAll,
    download,
    cancel,
    removeVersion,
    setToken,
    setMirror,
  }
}
