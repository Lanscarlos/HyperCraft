import { useEffect, useState } from 'react'

import { api } from './api'
import type { JarInfo } from './types'

/** How long the operator has to stop typing before the directory is listed. */
const DEBOUNCE_MS = 400

export interface HostJars {
  jars: JarInfo[]
  /** False for a directory that does not exist yet. */
  exists: boolean
  loading: boolean
}

/**
 * Lists the jars in a directory on the host, following what the operator types.
 *
 * This is what lets the 服务端 jar dropdown work for a path outside the panel's
 * own servers root: the field is free text, so the only way to offer real
 * choices is to go and look. Debounced, because it follows every keystroke.
 *
 * `rev` is a caller-supplied revision — bump it to re-list after copying a core
 * into the directory.
 */
export function useHostJars(directory: string, rev = 0): HostJars {
  const [jars, setJars] = useState<JarInfo[]>([])
  const [exists, setExists] = useState(true)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    const dir = directory.trim()
    if (!dir) {
      setJars([])
      setExists(true)
      return
    }

    let live = true
    setLoading(true)
    const timer = window.setTimeout(() => {
      api
        .browseHost(dir)
        .then((listing) => {
          if (!live) return
          setJars(listing.jars)
          setExists(listing.exists)
        })
        .catch(() => {
          if (!live) return
          // An unreadable directory is not worth a banner here: the field still
          // takes a hand-typed name, which is what it did before this existed.
          setJars([])
          setExists(false)
        })
        .finally(() => live && setLoading(false))
    }, DEBOUNCE_MS)

    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [directory, rev])

  return { jars, exists, loading }
}
