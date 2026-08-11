import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatPercent } from '../format'
import type { InstanceSection } from '../routes'
import type { InstanceMetrics, InstanceStatus, StateInfo } from '../types'
import { STATE_LABELS, isLive, mergeState } from '../types'
import { useTween } from '../useTween'
import { useUptime } from '../useUptime'
import { Console } from './Console'
import { PowerControls } from './PowerControls'
import { Sparkline } from './Sparkline'

/** The window the tiles summarise. Long enough to show a spike, short enough
 *  that it is still about right now. */
const TREND_MS = 5 * 60_000

interface Props {
  instance: InstanceStatus
  /** False while another section is in front; the pane stays mounted so the
   *  console's socket and scrollback survive a trip to 文件. */
  active: boolean
  onChanged: (instance: InstanceStatus) => void
  onOpenSection: (section: InstanceSection) => void
}

/**
 * The instance home page: a live operations desk, not a menu.
 *
 * Everything an operator does in the first ten seconds is here — see the
 * state, read the log, send a command, stop the thing — because a console
 * filed under a second-level tab is a console that gets replaced by SSH, and a
 * panel nobody opens the console in is a panel that has been routed around.
 * The other sections are one click away in the sidebar and none of them are on
 * the critical path.
 */
export function InstanceCockpit({ instance, active, onChanged, onOpenSection }: Props) {
  const [error, setError] = useState<string | null>(null)
  const live = isLive(instance.state)
  const uptime = useUptime(instance.startedAt, live)
  const metrics = useSeries(instance.id, active && live)
  const address = useAddress(instance.id, active)

  const trend = useMemo(() => {
    if (!metrics) return { cpu: [] as number[], memory: [] as number[], latest: null }
    const samples = metrics.samples.map((s) => ({ ...s, ms: new Date(s.time).getTime() }))
    const newest = samples.length > 0 ? samples[samples.length - 1].ms : Date.now()
    const window = samples.filter((s) => s.ms >= newest - TREND_MS)
    return {
      cpu: window.map((s) => s.cpuPercent),
      memory: window.map((s) => s.memoryBytes),
      latest: window[window.length - 1] ?? null,
    }
  }, [metrics])

  const xmx = instance.maxMemoryMB > 0 ? instance.maxMemoryMB * 1024 * 1024 : 0
  // null rather than 0 for "no reading": a stopped server has not got a
  // memory figure of zero, it has not got one at all, and the tiles say so in
  // words. It is also what keeps the readouts from counting up from zero the
  // first time a sample lands — see useTween.
  const used = trend.latest?.memoryBytes ?? null
  const cpu = trend.latest?.cpuPercent ?? null

  const applyState = (state: StateInfo) => onChanged(mergeState(instance, state))

  return (
    <div className="cockpit">
      <header className="cockpit__bar">
        <div className="cockpit__identity">
          <div className="cockpit__title">
            <span className={`status__dot status__dot--${instance.state}`} />
            <h1>{instance.name}</h1>
            <span className={`pill pill--${instance.state}`}>{STATE_LABELS[instance.state]}</span>
          </div>
          <div className="cockpit__facts">
            <span title={instance.jar}>{basename(instance.jar) || '未设置核心'}</span>
            <span title={instance.java}>{javaLabel(instance.java)}</span>
            {uptime ? <span>已运行 {uptime}</span> : <span>{stopNote(instance)}</span>}
            {instance.pid ? <span>PID {instance.pid}</span> : null}
            {address && <CopyAddress address={address} />}
          </div>
        </div>

        <PowerControls instance={instance} onChanged={onChanged} onError={setError} />
      </header>

      {error && <div className="alert alert--error">{error}</div>}
      {instance.message && !error && <div className="instance__message">{instance.message}</div>}

      {/* Three tiles, not six rings. Each one is a number that changes what you
          do next; the rest of the history is a click away in 监控. */}
      <div className="tiles">
        <Tile
          label="CPU"
          value={<Reading value={cpu} format={formatPercent} fallback={live ? '等采样…' : '—'} />}
          detail={
            metrics ? `本机 ${metrics.cpuCores} 核 · 100% 为占满一核` : '主线程基本是单线程的'
          }
          spark={
            trend.cpu.length > 1 ? (
              <Sparkline
                values={trend.cpu}
                max={100}
                color="var(--series-cpu)"
                ariaLabel="最近 5 分钟 CPU 走势"
              />
            ) : null
          }
          onClick={() => onOpenSection('metrics')}
        />
        <Tile
          label="内存"
          value={<Reading value={used} format={formatBytes} fallback={live ? '等采样…' : '—'} />}
          detail={
            xmx > 0 ? (
              <>
                已分配 {formatBytes(xmx)}
                {used !== null && (
                  <>
                    {' · '}
                    <Reading value={used} format={(v) => formatPercent((v / xmx) * 100)} />
                  </>
                )}
              </>
            ) : (
              '没有设置 -Xmx'
            )
          }
          spark={
            trend.memory.length > 1 ? (
              <Sparkline
                values={trend.memory}
                max={xmx || Math.max(...trend.memory)}
                color="var(--series-memory)"
                ariaLabel="最近 5 分钟内存走势"
              />
            ) : null
          }
          onClick={() => onOpenSection('metrics')}
        />
        <Tile
          label="运行时长"
          value={uptime ?? '未运行'}
          detail={
            trend.latest
              ? `${trend.latest.processes} 个进程 · 采样间隔 ${Math.round(metrics?.intervalSeconds ?? 5)} 秒`
              : stopNote(instance)
          }
        />
      </div>

      <div className="cockpit__split">
        <Console instanceId={instance.id} state={instance.state} onState={applyState} />
        <QuickPanel instance={instance} onOpenSection={onOpenSection} />
      </div>
    </div>
  )
}

/**
 * The column beside the log.
 *
 * A player list with kick/ban/op on each row is what belongs here, and it
 * needs something this panel does not have yet: the daemon reads the server's
 * stdout, not its player registry. Until it does, the honest thing is the
 * commands that answer the same questions — and the `list` shortcut is one
 * click, which is what a player list would have cost anyway.
 */
function QuickPanel({
  instance,
  onOpenSection,
}: {
  instance: InstanceStatus
  onOpenSection: (section: InstanceSection) => void
}) {
  const [busy, setBusy] = useState<string | null>(null)
  const [note, setNote] = useState<string | null>(null)
  const live = instance.state === 'running'

  const send = async (command: string) => {
    setBusy(command)
    setNote(null)
    try {
      await api.sendCommand(instance.id, command)
      setNote(`已发送 ${command} —— 结果看左边的日志`)
    } catch (err) {
      setNote(err instanceof Error ? err.message : '发送失败')
    } finally {
      setBusy(null)
    }
  }

  return (
    <aside className="quick">
      <h2 className="panel__title">快捷操作</h2>

      {!live ? (
        <p className="quick__idle">服务器没有在运行，控制台命令发不出去。</p>
      ) : (
        <div className="quick__list">
          {QUICK_COMMANDS.map((item) => (
            <button
              key={item.command}
              className="quick__item"
              disabled={busy !== null}
              onClick={() => void send(item.command)}
              aria-busy={busy === item.command || undefined}
            >
              <strong>{item.label}</strong>
              <code>{item.command}</code>
              <small>{item.note}</small>
            </button>
          ))}
        </div>
      )}

      {note && <p className="quick__note">{note}</p>}

      <div className="quick__links">
        <button className="link" onClick={() => onOpenSection('metrics')}>
          看曲线
        </button>
        <button className="link" onClick={() => onOpenSection('properties')}>
          服务器配置
        </button>
        <button className="link" onClick={() => onOpenSection('files')}>
          文件
        </button>
      </div>
    </aside>
  )
}

const QUICK_COMMANDS = [
  { label: '在线玩家', command: 'list', note: '当前在线的名字和人数' },
  { label: '保存世界', command: 'save-all', note: '立刻落盘，重启前先来一下' },
  { label: '重载白名单', command: 'whitelist reload', note: '改完 whitelist.json 后生效' },
  { label: '看 TPS', command: 'tps', note: 'Paper / Spigot 有这个命令' },
]

function Tile({
  label,
  value,
  detail,
  spark,
  onClick,
}: {
  label: string
  value: React.ReactNode
  detail: React.ReactNode
  spark?: React.ReactNode
  onClick?: () => void
}) {
  const body = (
    <>
      <span className="tile__label">{label}</span>
      <strong className="tile__value">{value}</strong>
      {spark}
      <span className="tile__detail">{detail}</span>
    </>
  )
  if (!onClick) return <div className="tile">{body}</div>
  return (
    <button className="tile tile--link" onClick={onClick}>
      {body}
    </button>
  )
}

/**
 * One live number, and nothing else.
 *
 * The tween runs a frame at a time, so whichever component owns it re-renders
 * sixty times a second for the length of the journey. Owned by the cockpit,
 * that would be the console and both sparklines redrawn on every one of those
 * frames — the exact cost this panel refuses to pay, on the exact page that
 * can least afford it. Owned by a component that renders a string, it is a
 * text node being rewritten, which is what it looked like it was all along.
 */
function Reading({
  value,
  format,
  fallback,
}: {
  value: number | null
  format: (value: number) => string
  fallback?: React.ReactNode
}) {
  const shown = useTween(value)
  return <>{shown === null ? fallback : format(shown)}</>
}

function CopyAddress({ address }: { address: string }) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      // Clipboard needs a secure context; the address is on screen either way.
    }
  }

  return (
    <button className="cockpit__address" onClick={() => void copy()} title="复制连接地址">
      <code>{address}</code>
      <span>{copied ? '已复制' : '复制'}</span>
    </button>
  )
}

/** The instance's own CPU/memory history, polled while its page is in front. */
function useSeries(id: string, enabled: boolean): InstanceMetrics | null {
  const [data, setData] = useState<InstanceMetrics | null>(null)

  useEffect(() => {
    setData(null)
    if (!enabled) return
    let cancelled = false

    const load = async () => {
      try {
        const fetched = await api.instanceMetrics(id)
        if (!cancelled) setData(fetched)
      } catch {
        // Informational; the console itself reports a lost connection.
      }
    }

    void load()
    const timer = window.setInterval(() => void load(), 5000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [id, enabled])

  return data
}

/**
 * Where players type to get in.
 *
 * Read from the instance's own server.properties rather than assumed, because
 * a second server on the same box is not on 25565 and the person asking for
 * the address is usually about to paste it to someone else. The host part is
 * whatever this panel was reached on, which is the best guess available from
 * inside the browser and is right whenever the panel and the server share a
 * machine — which, this being a single-host panel, they do.
 */
function useAddress(id: string, enabled: boolean): string | null {
  const [port, setPort] = useState<string | null>(null)

  useEffect(() => {
    setPort(null)
    if (!enabled) return
    let cancelled = false

    api
      .getProperties(id)
      .then((props) => {
        if (cancelled || !props.exists) return
        const entry = props.entries.find((row) => row.key === 'server-port')
        if (entry?.value) setPort(entry.value)
      })
      .catch(() => {
        // A proxy core has no server.properties; there is simply no address to
        // show, which is not an error worth a banner.
      })

    return () => {
      cancelled = true
    }
  }, [id, enabled])

  return port ? `${window.location.hostname}:${port}` : null
}

function basename(path: string): string {
  if (!path) return ''
  const parts = path.split(/[\\/]/)
  return parts[parts.length - 1]
}

function javaLabel(java: string): string {
  if (!java || java === 'java') return '系统 java'
  const major = java.match(/(?:jdk|jre|java)[-_]?(\d{1,2})/i)
  return major ? `Java ${major[1]}` : basename(java)
}

function stopNote(instance: InstanceStatus): string {
  if (instance.state === 'crashed') {
    return instance.exitCode != null ? `异常退出，退出码 ${instance.exitCode}` : '异常退出'
  }
  return '已停止'
}
