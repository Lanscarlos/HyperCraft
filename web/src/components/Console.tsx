import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'

import { useConsole } from '../useConsole'
import { commonPrefix, complete, trackPlayers, type Candidate } from '../completion'
import { onThemeChange, terminalTheme } from '../theme'
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

/**
 * Font stack for the terminal.
 *
 * The tail matters as much as the head: server output is full of box drawing,
 * arrows and the occasional emoji, and the browser only falls back glyph by
 * glyph through fonts that are actually named here.
 */
const CONSOLE_FONTS = [
  '"JetBrains Mono"',
  '"Cascadia Mono"',
  '"Sarasa Mono SC"',
  '"Noto Sans Mono CJK SC"',
  '"Microsoft YaHei Mono"',
  'Menlo',
  'Consolas',
  '"DejaVu Sans Mono"',
  '"Noto Sans Mono"',
  'monospace',
  '"Noto Color Emoji"',
  '"Apple Color Emoji"',
  '"Segoe UI Emoji"',
].join(', ')

/** Suggestions currently offered for the input, with the highlighted one. */
interface Suggestions {
  candidates: Candidate[]
  index: number
}

export function Console({ instanceId, state, onState }: ConsoleProps) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)

  const [command, setCommand] = useState('')
  const [history, setHistory] = useState<string[]>([])
  const historyIndex = useRef<number | null>(null)
  const [players, setPlayers] = useState<string[]>([])
  const [suggestions, setSuggestions] = useState<Suggestions | null>(null)

  // Create the terminal once per mount; instance switches clear it below.
  useEffect(() => {
    const term = new Terminal({
      // Unicode 11 widths need the proposed API; without them CJK text and
      // emoji are measured as one cell and overlap whatever follows.
      allowProposedApi: true,
      convertEol: true,
      cursorBlink: false,
      disableStdin: true,
      fontFamily: CONSOLE_FONTS,
      fontSize: 13,
      lineHeight: 1.25,
      scrollback: 5000,
      // Read off the tokens, so the canvas matches what .console__screen
      // paints behind it in either mode. The console takes no input, so the
      // cursor is hidden by giving it the background.
      theme: { ...terminalTheme(), cursor: 'transparent' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    const unicode = new Unicode11Addon()
    term.loadAddon(unicode)
    term.unicode.activeVersion = '11'
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

    // The canvas cannot inherit a token, so a mode switch has to be handed to
    // it; the scrollback is untouched, only the palette is swapped.
    const unwatch = onThemeChange(() => {
      term.options.theme = { ...terminalTheme(), cursor: 'transparent' }
    })

    return () => {
      unwatch()
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
    // Scrollback replays on reconnect, so the roster rebuilds itself without
    // the panel having to track players server-side.
    setPlayers((current) =>
      lines.reduce((names, line) => trackPlayers(names, line.text), current),
    )
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
    setPlayers([])
    setSuggestions(null)
  }, [instanceId])

  const canType = isLive(state) && status === 'open'

  const setInput = (text: string) => {
    setCommand(text)
    setSuggestions(null)
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const text = command.trim()
    if (!text) return
    if (!send(text)) return

    setHistory((prev) => (prev[0] === text ? prev : [text, ...prev].slice(0, 100)))
    historyIndex.current = null
    setInput('')
  }

  /**
   * Tab behaves the way it does in a terminal: the first press fills in as
   * much as every candidate agrees on, and further presses cycle through them
   * while the list stays on screen.
   */
  const completeInput = (backwards: boolean) => {
    if (suggestions && suggestions.candidates.length > 1) {
      const count = suggestions.candidates.length
      // index -1 means the list is showing but nothing has been picked yet,
      // which is where the first Tab leaves it.
      const index =
        suggestions.index < 0
          ? backwards
            ? count - 1
            : 0
          : (suggestions.index + (backwards ? -1 : 1) + count) % count
      setCommand(suggestions.candidates[index].line)
      setSuggestions({ ...suggestions, index })
      return
    }

    const candidates = complete(command, { players, history })
    if (candidates.length === 0) {
      setSuggestions(null)
      return
    }
    if (candidates.length === 1) {
      // Unambiguous: finish it and leave a space ready for the next argument.
      setInput(candidates[0].line + ' ')
      return
    }

    const shared = commonPrefix(candidates)
    setCommand(shared.length > command.length ? shared : command)
    setSuggestions({ candidates, index: -1 })
  }

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Tab') {
      event.preventDefault()
      completeInput(event.shiftKey)
      return
    }
    if (event.key === 'Escape') {
      setSuggestions(null)
      return
    }
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    if (history.length === 0) return
    event.preventDefault()
    setSuggestions(null)

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

  const highlighted = suggestions?.candidates[suggestions.index]

  return (
    <div className="console">
      <div className="console__screen" ref={hostRef} />

      {error && <div className="console__error">{error}</div>}

      {suggestions && (
        <div className="console__hints">
          <div className="console__hint-list">
            {suggestions.candidates.slice(0, 40).map((candidate, index) => (
              <button
                key={candidate.line}
                type="button"
                className={
                  index === suggestions.index
                    ? 'console__hint console__hint--active'
                    : 'console__hint'
                }
                onClick={() => {
                  setInput(candidate.line + ' ')
                  inputRef.current?.focus()
                }}
              >
                {candidate.value}
              </button>
            ))}
            {suggestions.candidates.length > 40 && (
              <span className="console__hint-more">
                还有 {suggestions.candidates.length - 40} 个…
              </span>
            )}
          </div>
          <small className="console__hint-desc">
            {highlighted?.desc ?? 'Tab 切换候选，Esc 关闭'}
          </small>
        </div>
      )}

      <form className="console__input" onSubmit={submit}>
        <span className="console__prompt">&gt;</span>
        <input
          ref={inputRef}
          value={command}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={!canType}
          spellCheck={false}
          autoComplete="off"
          placeholder={
            canType
              ? '输入服务器命令，回车发送（Tab 补全，↑↓ 翻历史）'
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
