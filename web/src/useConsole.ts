import { useCallback, useEffect, useRef, useState } from 'react'

import { consoleSocketURL } from './api'
import type { ConsoleLine, ConsoleMessage, StateInfo } from './types'

export type ConnectionStatus = 'connecting' | 'open' | 'reconnecting'

interface ConsoleHandlers {
  /** Append lines to the terminal. */
  onLines: (lines: ConsoleLine[]) => void
  /** Wipe the terminal, used when a reconnect cannot be stitched onto what is
   *  already on screen. */
  onClear: () => void
  onState: (state: StateInfo) => void
}

interface ConsoleController {
  status: ConnectionStatus
  error: string | null
  send: (command: string) => boolean
}

const MAX_BACKOFF_MS = 10_000

/**
 * Keeps a console websocket open for one instance, reconnecting on its own.
 *
 * The socket is a view onto a server that is running regardless — so a dropped
 * connection is a UI problem to paper over, never a reason to touch the server.
 * On reconnect the panel replays only the lines newer than the last one shown.
 */
export function useConsole(
  instanceId: string,
  handlers: ConsoleHandlers,
): ConsoleController {
  const [status, setStatus] = useState<ConnectionStatus>('connecting')
  const [error, setError] = useState<string | null>(null)

  const socketRef = useRef<WebSocket | null>(null)
  const lastSeqRef = useRef(0)
  const attemptRef = useRef(0)
  const retryRef = useRef<number | undefined>(undefined)

  // Handlers are captured in a ref so a parent re-render does not tear down
  // and rebuild the socket.
  const handlersRef = useRef(handlers)
  handlersRef.current = handlers

  useEffect(() => {
    let disposed = false
    lastSeqRef.current = 0
    attemptRef.current = 0

    const connect = () => {
      if (disposed) return
      setStatus(attemptRef.current === 0 ? 'connecting' : 'reconnecting')

      const socket = new WebSocket(consoleSocketURL(instanceId))
      socketRef.current = socket

      socket.onopen = () => {
        if (disposed) return
        attemptRef.current = 0
        setStatus('open')
        setError(null)
      }

      socket.onmessage = (event) => {
        if (disposed) return
        let msg: ConsoleMessage
        try {
          msg = JSON.parse(event.data as string)
        } catch {
          return
        }

        switch (msg.type) {
          case 'history': {
            // Defensive: an older panel build could omit an empty list.
            const lines = msg.lines ?? []
            const fresh = lines.filter((l) => l.seq > lastSeqRef.current)
            // If the oldest buffered line is already newer than what we have,
            // output was lost in between; a partial render would be a lie, so
            // start the pane over from the scrollback we do have.
            const gap =
              lastSeqRef.current > 0 &&
              lines.length > 0 &&
              lines[0].seq > lastSeqRef.current + 1
            if (gap) {
              handlersRef.current.onClear()
              handlersRef.current.onLines(lines)
            } else if (fresh.length > 0) {
              handlersRef.current.onLines(fresh)
            }
            if (lines.length > 0) {
              lastSeqRef.current = lines[lines.length - 1].seq
            }
            handlersRef.current.onState(msg.state)
            break
          }
          case 'line':
            if (msg.line.seq > lastSeqRef.current) {
              lastSeqRef.current = msg.line.seq
              handlersRef.current.onLines([msg.line])
            }
            break
          case 'state':
            handlersRef.current.onState(msg.state)
            break
          case 'error':
            setError(msg.message)
            break
          case 'resync':
            // The server dropped us for lagging; the reconnect below refills.
            socket.close()
            break
        }
      }

      socket.onclose = () => {
        if (disposed) return
        socketRef.current = null
        setStatus('reconnecting')

        const delay = Math.min(
          MAX_BACKOFF_MS,
          1000 * 2 ** Math.min(attemptRef.current, 4),
        )
        attemptRef.current += 1
        retryRef.current = window.setTimeout(connect, delay)
      }

      socket.onerror = () => {
        // onclose always follows; reconnection is handled there.
      }
    }

    connect()

    return () => {
      disposed = true
      window.clearTimeout(retryRef.current)
      socketRef.current?.close()
      socketRef.current = null
    }
  }, [instanceId])

  const send = useCallback((command: string) => {
    const socket = socketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      setError('控制台连接已断开，正在重连…')
      return false
    }
    socket.send(JSON.stringify({ type: 'command', command }))
    return true
  }, [])

  return { status, error, send }
}
