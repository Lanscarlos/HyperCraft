import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'

import { useConsole } from '../useConsole'
import type { ConsoleLine, InstanceState, StateInfo } from '../types'
import { isLive } from '../types'

interface ConsoleProps {
  instanceId: string
  state: InstanceState
  onState: (state: StateInfo) => void
}

// SGR prefixes applied per stream. stdout is left untouched so the server's own
// colour codes render as they would in a real terminal.
const STREAM_STYLE: Record<ConsoleLine['stream'], string> = {
  stdout: '',
  stderr: '\x1b[38;5;203m',
  system: '\x1b[38;5;110m',
}

const CONNECTION_LABEL = {
  connecting: '连接中…',
  open: '已连接',
  reconnecting: '重连中…',
} as const

export function Console({ instanceId, state, onState }: ConsoleProps) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)

  const [command, setCommand] = useState('')
  const [history, setHistory] = useState<string[]>([])
  const historyIndex = useRef<number | null>(null)

  // Create the terminal once per mount; instance switches clear it below.
  useEffect(() => {
    const term = new Terminal({
      convertEol: true,
      cursorBlink: false,
      disableStdin: true,
      fontFamily:
        '"JetBrains Mono", "Cascadia Mono", "Sarasa Mono SC", Menlo, Consolas, monospace',
      fontSize: 13,
      lineHeight: 1.25,
      scrollback: 5000,
      theme: {
        background: '#0d1117',
        foreground: '#c9d1d9',
        cursor: '#0d1117',
        selectionBackground: '#2f5580',
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(hostRef.current!)
    fit.fit()

    termRef.current = term
    fitRef.current = fit

    const observer = new ResizeObserver(() => {
      // fit() throws if the element is hidden (tab switched away).
      try {
        fit.fit()
      } catch {
        /* not visible right now */
      }
    })
    observer.observe(hostRef.current!)

    return () => {
      observer.disconnect()
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [])

  const onLines = useCallback((lines: ConsoleLine[]) => {
    const term = termRef.current
    if (!term) return
    for (const line of lines) {
      const style = STREAM_STYLE[line.stream]
      term.write(style ? `${style}${line.text}\x1b[0m\r\n` : `${line.text}\r\n`)
    }
  }, [])

  const onClear = useCallback(() => termRef.current?.clear(), [])

  const handlers = useMemo(
    () => ({ onLines, onClear, onState }),
    [onLines, onClear, onState],
  )
  const { status, error, send } = useConsole(instanceId, handlers)

  // Switching instances must not leave the previous server's output on screen.
  useEffect(() => {
    termRef.current?.reset()
  }, [instanceId])

  const canType = isLive(state) && status === 'open'

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const text = command.trim()
    if (!text) return
    if (!send(text)) return

    setHistory((prev) => (prev[0] === text ? prev : [text, ...prev].slice(0, 100)))
    historyIndex.current = null
    setCommand('')
  }

  // Up/Down walk the command history, the way a real console does.
  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    if (history.length === 0) return
    event.preventDefault()

    const current = historyIndex.current
    if (event.key === 'ArrowUp') {
      const next = current === null ? 0 : Math.min(current + 1, history.length - 1)
      historyIndex.current = next
      setCommand(history[next])
    } else {
      if (current === null) return
      const next = current - 1
      if (next < 0) {
        historyIndex.current = null
        setCommand('')
      } else {
        historyIndex.current = next
        setCommand(history[next])
      }
    }
  }

  return (
    <div className="console">
      <div className="console__screen" ref={hostRef} />

      {error && <div className="console__error">{error}</div>}

      <form className="console__input" onSubmit={submit}>
        <span className="console__prompt">&gt;</span>
        <input
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={!canType}
          spellCheck={false}
          autoComplete="off"
          placeholder={
            canType
              ? '输入服务器命令，回车发送（↑↓ 翻历史）'
              : status !== 'open'
                ? `控制台${CONNECTION_LABEL[status]}`
                : '服务器未运行'
          }
        />
        <button type="submit" disabled={!canType || !command.trim()}>
          发送
        </button>
      </form>
    </div>
  )
}
