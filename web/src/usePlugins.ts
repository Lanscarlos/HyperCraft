import { useCallback, useEffect, useState } from 'react'

import { api } from './api'
import type { DownloadJob, LibraryPlugin, PluginLibrary } from './types'
import type { DownloadController } from './useDownloads'
import { hasPluginUpdate, isDownloadActive } from './types'


export interface PluginInput {
  name: string
  repo: string
  assetPattern?: string
  prerelease?: boolean
  private?: boolean
  /** Which stored GitHub token reads this repository. Empty is the default. */
  tokenId?: string
  targetDir?: string
  note?: string
}

export interface PluginController {
  library: PluginLibrary | null
  plugins: LibraryPlugin[]
  /** The download queue and its history, newest first. */
  jobs: DownloadJob[]
  /** The newest job, for the places that only ever showed one. */
  job: DownloadJob | null
  /** How many jars are queued or coming down. Zero is the quiet state, and it
   *  is what the sidebar badge and the queue page's summary both read. */
  active: number
  /** True while any plugin jar is queued or coming down. */
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
  /** Asks upstream about every tracked plugin, and hands back the library it
   *  got — the caller is the one that has to say what the check found. */
  checkAll: () => Promise<PluginLibrary | null>
  download: (id: string, tag: string, asset?: string) => Promise<void>
  /** Stops one download by job id, or everything in flight when given none. */
  cancel: (jobId?: string) => Promise<void>
  /** Forgets the finished rows. Does not stop anything still running. */
  clearFinished: () => Promise<void>
  removeVersion: (id: string, tag: string) => Promise<void>
  /** Adds a GitHub credential under an operator-chosen name. */
  addToken: (name: string, token: string) => Promise<boolean>
  /** Renames a token, replaces its secret, or makes it the default one. */
  updateToken: (
    id: string,
    input: { name?: string; token?: string; default?: boolean },
  ) => Promise<boolean>
  /** Forgets a token. The plugins naming it start saying so. */
  removeToken: (id: string) => Promise<boolean>
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
export function usePlugins(enabled: boolean, downloads: DownloadController): PluginController {
  const [library, setLibrary] = useState<PluginLibrary | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // From the panel-wide queue rather than from this shelf's own listing. This
  // shelf had the only real queue before the kernel existed, and it still paid
  // for a progress bar by re-fetching the whole plugin library eight times a
  // second — which on a library of any size is the most expensive of the four.
  const jobs = downloads.of('plugin')
  const job = jobs[0] ?? null
  const active = downloads.activeOf('plugin')
  const downloading = active > 0
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

  // Re-read when the queue goes quiet, not while it runs: a finished download
  // adds a version to the library, and that is what this listing is for.
  useEffect(() => {
    if (!enabled || downloading) return
    void refresh()
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
        const next = await api.checkPlugins()
        setLibrary(next)
        return next
      }, '检查更新失败').catch(() => null),
    [act],
  )

  const download = useCallback(
    (id: string, tag: string, asset?: string) =>
      act(async () => {
        await api.downloadPlugin(id, tag, asset)
        // Ask the queue at once rather than waiting for its next tick, so the
        // row appears under the button that was just pressed. Asking for a jar
        // already queued answers with that same job, so a repeat shows up as
        // the row that is already there.
        await downloads.refresh()
      }, '下载失败').catch(() => undefined),
    [act, downloads],
  )

  const cancel = useCallback(
    (jobId?: string) =>
      act(async () => {
        if (jobId) await downloads.cancel(jobId)
        else for (const entry of jobs.filter((e) => isDownloadActive(e.state))) {
          await downloads.cancel(entry.id)
        }
        await refresh()
      }, '取消失败').catch(() => undefined),
    [act, refresh, downloads, jobs],
  )

  const clearFinished = useCallback(
    () =>
      act(async () => {
        await downloads.clearFinished()
        await refresh()
      }, '清空记录失败').catch(() => undefined),
    [act],
  )

  const removeVersion = useCallback(
    (id: string, tag: string) =>
      act(async () => {
        await api.deletePluginVersion(id, tag)
        await refresh()
      }, '删除版本失败').catch(() => undefined),
    [act, refresh],
  )

  // All three answer with the library as it looks afterwards, so the token list
  // updates without a second round trip.
  const addToken = useCallback(
    (name: string, token: string) =>
      act(async () => {
        setLibrary(await api.addPluginToken(name, token))
        return true
      }, '保存访问令牌失败').catch(() => false),
    [act],
  )

  const updateToken = useCallback(
    (id: string, input: { name?: string; token?: string; default?: boolean }) =>
      act(async () => {
        setLibrary(await api.updatePluginToken(id, input))
        return true
      }, '保存访问令牌失败').catch(() => false),
    [act],
  )

  const removeToken = useCallback(
    (id: string) =>
      act(async () => {
        setLibrary(await api.deletePluginToken(id))
        return true
      }, '删除访问令牌失败').catch(() => false),
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
    jobs,
    job,
    active,
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
    clearFinished,
    removeVersion,
    addToken,
    updateToken,
    removeToken,
    setMirror,
  }
}
