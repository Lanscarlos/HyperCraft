import { useCallback, useEffect, useState } from 'react'

import { api } from './api'
import type { SchematicEntry, SchematicLibrary, SchematicTarget } from './types'

export interface SchematicController {
  library: SchematicLibrary | null
  entries: SchematicEntry[]
  targets: SchematicTarget[]
  loading: boolean
  /** True while one of the actions below is in flight. */
  busy: boolean
  error: string | null
  refresh: () => Promise<void>
  edit: (id: string, patch: { name?: string; note?: string; tags?: string[] }) => Promise<void>
  remove: (id: string) => Promise<void>
  rescan: () => Promise<{ added: number; dropped: number } | null>
}

/**
 * Tracks the panel's building library.
 *
 * Unlike the other four shelves this one has nothing long-running to watch —
 * there is no download job, because a schematic is a few hundred kilobytes and
 * the request that fetches one has finished before a progress bar would have
 * drawn its first frame. So it is loaded once and refreshed after the actions
 * that change it, with no poll at all.
 *
 * It still lives at the app level rather than inside the page, for the same
 * reason the others do: the sidebar says how many builds are on the shelf, and
 * the count would otherwise only be right on the page that already shows them.
 */
export function useSchematics(enabled: boolean): SchematicController {
  const [library, setLibrary] = useState<SchematicLibrary | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      setLibrary(await api.schematics())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取建筑库失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    void refresh()
  }, [enabled, refresh])

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

  const edit = useCallback(
    (id: string, patch: { name?: string; note?: string; tags?: string[] }) =>
      act(async () => {
        const updated = await api.editSchematic(id, patch)
        // Swapped in place rather than re-fetched: the list is sorted by when
        // things were added, and a rename must not make a row move.
        setLibrary((prev) =>
          prev
            ? { ...prev, entries: prev.entries.map((e) => (e.id === id ? updated : e)) }
            : prev,
        )
      }, '保存失败').catch(() => undefined),
    [act],
  )

  const remove = useCallback(
    (id: string) =>
      act(async () => {
        await api.deleteSchematic(id)
        await refresh()
      }, '删除失败').catch(() => undefined),
    [act, refresh],
  )

  const rescan = useCallback(
    async () => {
      let result: { added: number; dropped: number } | null = null
      await act(async () => {
        result = await api.rescanSchematics()
        await refresh()
      }, '扫描失败').catch(() => undefined)
      return result
    },
    [act, refresh],
  )

  return {
    library,
    entries: library?.entries ?? [],
    targets: library?.targets ?? [],
    loading,
    busy,
    error,
    refresh,
    edit,
    remove,
    rescan,
  }
}
