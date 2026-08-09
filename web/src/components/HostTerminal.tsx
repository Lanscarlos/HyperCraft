import { useCallback, useEffect, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'

import { api, terminalSocketURL } from '../api'
import type { TerminalController } from '../useTerminal'

/** Same stack the server console uses: server output and shell output are full
 *  of the same box drawing, CJK and emoji, and the browser only falls back
 *  through fonts that are actually named. */
const TERMINAL_FONTS = [
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

type Phase = 'connecting' | 'open' | 'closed'

interface Props {
  terminal: TerminalController
  onOpenSettings: () => void
}

/**
 * A shell on the machine the panel runs on.
 *
 * The session is the socket: closing this page ends the shell, and opening it
 * again starts a fresh one. That is the opposite of the server console, and
 * deliberately so — a Minecraft server is the panel's to keep running, while a
 * shell nobody is watching is only a way to leave something half-finished. Run
 * anything that has to outlive the tab under systemd, or inside tmux.
 */
export function HostTerminal({ terminal, onOpenSettings }: Props) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)

  const [phase, setPhase] = useState<Phase>('connecting')
  const [notice, setNotice] = useState<string | null>(null)
  // Bumped to start a new shell. Never bumped automatically: a reconnect here
  // is a *different* shell, so silently reconnecting would throw away whatever
  // the operator had running and pretend nothing happened.
  const [attempt, setAttempt] = useState(0)

  const status = terminal.status
  const available = Boolean(status?.enabled && status.supported)

  useEffect(() => {
    if (!available) return

    const term = new Terminal({
      allowProposedApi: true,
      cursorBlink: true,
      fontFamily: TERMINAL_FONTS,
      fontSize: 13,
      lineHeight: 1.2,
      scrollback: 5000,
      // macOS habits: Option as Meta is what makes Alt-b / Alt-f work in bash.
      macOptionIsMeta: true,
      theme: {
        background: '#0d1117',
        foreground: '#c9d1d9',
        cursor: '#58a6ff',
        selectionBackground: '#2f5580',
        black: '#484f58',
        brightBlack: '#6e7681',
        blue: '#6ea8ff',
        brightBlue: '#89b4ff',
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    const unicode = new Unicode11Addon()
    term.loadAddon(unicode)
    term.unicode.activeVersion = '11'
    term.open(hostRef.current!)
    try {
      fit.fit()
    } catch {
      /* laid out later */
    }
    termRef.current = term
    fitRef.current = fit

    setPhase('connecting')
    setNotice(null)

    const socket = new WebSocket(terminalSocketURL(term.cols, term.rows))
    // Output arrives as raw bytes so a multi-byte character straddling a read
    // is never split by a JSON encode; xterm reassembles UTF-8 across writes.
    socket.binaryType = 'arraybuffer'

    let opened = false
    const encoder = new TextEncoder()

    socket.onopen = () => {
      opened = true
      setPhase('open')
      term.focus()
    }

    socket.onmessage = (event) => {
      if (event.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(event.data))
        return
      }
      // Text frames are control messages; see terminalControl on the server.
      try {
        const msg = JSON.parse(event.data as string) as {
          type: string
          message?: string
          code?: number
        }
        if (msg.type === 'exit') {
          setNotice(
            msg.code ? `会话已结束（退出码 ${msg.code}）` : '会话已结束',
          )
        } else if (msg.type === 'error' && msg.message) {
          setNotice(msg.message)
        }
      } catch {
        /* not a control frame we understand */
      }
    }

    socket.onclose = () => {
      setPhase('closed')
      if (!opened) {
        // The WebSocket API hides the handshake's status code, so ask the API
        // why instead of guessing — "未启用" and "太多会话了" need different
        // answers from the operator.
        void api
          .terminalStatus()
          .then((fresh) => {
            setNotice(
              fresh.reason ||
                (fresh.enabled
                  ? '连接终端失败，请重试'
                  : '本机终端已被关闭，可在「设置 → 终端」中重新开启'),
            )
          })
          .catch(() => setNotice('连接终端失败，请重试'))
      }
    }

    const send = (data: string) => {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(encoder.encode(data))
      }
    }
    const typed = term.onData(send)
    // Binary events carry the bytes for keys xterm cannot express as a string.
    const typedBinary = term.onBinary((data) => {
      if (socket.readyState !== WebSocket.OPEN) return
      const bytes = new Uint8Array(data.length)
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 255
      socket.send(bytes)
    })

    const observer = new ResizeObserver(() => {
      try {
        fit.fit()
      } catch {
        return // hidden right now
      }
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(
          JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }),
        )
      }
    })
    observer.observe(hostRef.current!)

    return () => {
      observer.disconnect()
      typed.dispose()
      typedBinary.dispose()
      // Closing the socket is what ends the shell; there is no session left
      // behind to reattach to.
      socket.close()
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [available, attempt])

  const restart = useCallback(() => setAttempt((n) => n + 1), [])

  if (!status) {
    return <div className="page">正在读取终端设置…</div>
  }

  if (!available) {
    return (
      <div className="page">
        <h1>终端</h1>
        <p className="page__lead">
          {status.supported
            ? '本机终端还没有开启。开启后可以在这里直接得到一个面板所在机器的 shell，不用另外 SSH 上来。'
            : status.reason}
        </p>
        {status.supported && (
          <div>
            <button className="btn btn--primary" onClick={onOpenSettings}>
              去「设置 → 终端」开启
            </button>
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="hostterm">
      <div className="hostterm__bar">
        <div className="hostterm__where">
          <strong>{status.shell}</strong>
          <span>
            {status.user}@本机 · {status.cwd}
          </span>
        </div>
        <div className="hostterm__actions">
          <span className={`hostterm__dot hostterm__dot--${phase}`} />
          <span className="hostterm__phase">
            {phase === 'open' ? '已连接' : phase === 'connecting' ? '连接中…' : '已断开'}
          </span>
          <button className="btn" onClick={restart}>
            {phase === 'closed' ? '重新连接' : '重开会话'}
          </button>
        </div>
      </div>

      {notice && (
        <div className="hostterm__notice">
          {notice}
          <button className="link" onClick={restart}>
            开一个新会话
          </button>
        </div>
      )}

      <div className="hostterm__screen" ref={hostRef} />

      <small className="hostterm__hint">
        关掉这个页面就会挂断当前会话（连同它启动的程序）。要让命令活得比标签页久，
        请用 systemd 或者在里面开 tmux / screen。
      </small>
    </div>
  )
}
