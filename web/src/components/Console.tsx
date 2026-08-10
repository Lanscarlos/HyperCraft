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

// SGR prefixes applied per stream on a line console. stdout is left untouched
// so the server's own colour codes render as they would in a real terminal.
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

/**
 * One server's console.
 *
 * It renders in one of two modes, decided by the server on connect and fixed
 * for as long as the socket is open. A terminal console is exactly that — the
 * keyboard goes straight to the server's own line editor, so completion and
 * history are answered by the running server rather than guessed at here. A
 * line console is the pipe-backed fallback, and keeps the panel's own input box
 * because on the other side of a pipe there is nobody to answer a Tab.
 */
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
  const [ttyActive, setTtyActive] = useState<boolean | undefined>(undefined)

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

  const onBytes = useCallback((data: Uint8Array) => {
    termRef.current?.write(data)
  }, [])

  // reset rather than clear: the previous session may have left the terminal in
  // a mode of its own (an alternate screen, application keys), and only a reset
  // puts those back too.
  const onClear = useCallback(() => termRef.current?.reset(), [])

  const handleState = useCallback(
    (info: StateInfo) => {
      setTtyActive(info.ttyActive)
      onState(info)
    },
    [onState],
  )

  const viewport = useCallback(() => {
    const term = termRef.current
    return term ? { cols: term.cols, rows: term.rows } : null
  }, [])

  const handlers = useMemo(
    () => ({ onLines, onBytes, onClear, onState: handleState, viewport }),
    [onLines, onBytes, onClear, handleState, viewport],
  )

  // Held in a ref so the terminal can be created in an effect declared *before*
  // useConsole's. Effects run in declaration order, and that order is what
  // decides whether the socket handshake can carry a real window size or has to
  // open blind and correct itself a frame later.
  const sendResizeRef = useRef<(cols: number, rows: number) => void>(() => {})

  // Create the terminal once per mount; instance switches clear it below.
  useEffect(() => {
    const term = new Terminal({
      // Unicode 11 widths need the proposed API; without them CJK text and
      // emoji are measured as one cell and overlap whatever follows.
      allowProposedApi: true,
      cursorBlink: false,
      // Both modes send \r\n of their own — the pseudo-terminal's line
      // discipline adds it, and the line renderer writes it explicitly — so
      // translating here would only double it up.
      convertEol: false,
      // Input is enabled once the server says this console has a terminal
      // behind it; until then there is nowhere for a keystroke to go.
      disableStdin: true,
      fontFamily: CONSOLE_FONTS,
      fontSize: 13,
      lineHeight: 1.25,
      scrollback: 5000,
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
        return // not visible right now
      }
      // Harmless on a line console, which the server sizes nothing from.
      sendResizeRef.current(term.cols, term.rows)
    })
    observer.observe(hostRef.current!)

    return () => {
      observer.disconnect()
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [])

  const { status, error, tty, send, sendBytes, sendResize } = useConsole(
    instanceId,
    handlers,
  )
  sendResizeRef.current = sendResize

  // The canvas cannot inherit a token, so a mode switch has to be handed to it;
  // the scrollback is untouched, only the palette is swapped. A terminal
  // console shows a real cursor, because there is a real cursor to show.
  useEffect(() => {
    const term = termRef.current
    if (!term) return

    const paint = () => {
      const theme = terminalTheme()
      term.options.theme = tty ? theme : { ...theme, cursor: 'transparent' }
    }
    paint()
    term.options.disableStdin = !tty
    term.options.cursorBlink = Boolean(tty)

    return onThemeChange(paint)
  }, [tty])

  // Keystrokes go to the server's own console. Only wired up in terminal mode:
  // a pipe has no line editor listening, and turning keys into commands behind
  // the operator's back would be worse than ignoring them.
  useEffect(() => {
    const term = termRef.current
    if (!term || !tty) return

    const encoder = new TextEncoder()
    const typed = term.onData((data) => sendBytes(encoder.encode(data)))
    // Binary events carry the bytes for keys xterm cannot express as a string.
    const typedBinary = term.onBinary((data) => {
      const bytes = new Uint8Array(data.length)
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 255
      sendBytes(bytes)
    })

    return () => {
      typed.dispose()
      typedBinary.dispose()
    }
  }, [tty, sendBytes])

  // Switching instances must not leave the previous server's output on screen.
  useEffect(() => {
    termRef.current?.reset()
    setPlayers([])
    setSuggestions(null)
    setTtyActive(undefined)
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
  // An instance configured for a terminal whose start could not get one. The
  // console still works — the pipe output is drawn into it — but the server
  // will not be offering completion, so say so rather than let it look broken.
  const fellBackToPipes = tty === true && isLive(state) && ttyActive === false

  return (
    <div className="console">
      <div className="console__screen" ref={hostRef} />

      {error && <div className="console__error">{error}</div>}

      {tty && (
        <div className="console__status">
          <span className={`console__dot console__dot--${status}`} />
          <span>
            {status === 'open'
              ? fellBackToPipes
                ? '已连接 · 本次启动没能拿到伪终端，已回落到管道，服务端不会提供补全'
                : canType
                  ? '已连接 · 直接在上面输入，Tab 补全由服务端回答'
                  : '已连接 · 服务器未运行'
              : CONNECTION_LABEL[status]}
          </span>
        </div>
      )}

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

      {tty === false && (
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
      )}
    </div>
  )
}
