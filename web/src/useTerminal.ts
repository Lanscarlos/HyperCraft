import { useCallback, useEffect, useState } from 'react'

import { ApiError, api } from './api'
import type { TerminalStatus } from './types'

export interface TerminalController {
  status: TerminalStatus | null
  loading: boolean
  saving: boolean
  error: string | null
  refresh: () => Promise<void>
  setEnabled: (enabled: boolean) => Promise<void>
}

/**
 * Tracks whether the panel is handing out a shell.
 *
 * Fetched once rather than polled: unlike a download or an update, this only
 * changes when somebody flips the switch on the settings page, and every place
 * that flips it gets the fresh status back in the same response.
 */
export function useTerminal(signedIn: boolean): TerminalController {
  const [status, setStatus] = useState<TerminalStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      setStatus(await api.terminalStatus())
      setError(null)
    } catch (err) {
      // A 401 is the session expiring, which App already handles by returning
      // to the login screen; surfacing it here too would just add noise.
      if (!(err instanceof ApiError && err.isUnauthorized)) {
        setError(err instanceof Error ? err.message : '读取终端设置失败')
      }
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!signedIn) {
      setStatus(null)
      return
    }
    void refresh()
  }, [signedIn, refresh])

  const setEnabled = useCallback(async (enabled: boolean) => {
    setSaving(true)
    try {
      setStatus(await api.setTerminalEnabled(enabled))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }, [])

  return { status, loading, saving, error, refresh, setEnabled }
}
