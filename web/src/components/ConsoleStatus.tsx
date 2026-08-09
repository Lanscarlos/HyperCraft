import { useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatPercent, formatTime } from '../format'
import type { InstanceMetrics, InstanceStatus } from '../types'
import { isLive } from '../types'
import { Meter } from './Meter'

interface Props {
  instance: InstanceStatus
  /** False while another tab is in front: nothing polls behind a hidden pane. */
  active: boolean
  /** Jumps to 资源, where the same numbers have a shape over time. */
  onOpenResources: () => void
  onCollapse: () => void
}

/**
 * The two numbers you want *beside* the log rather than instead of it.
 *
 * "The server is stuttering — is it memory or is it the main thread" is a
 * question asked while reading the log, and answering it used to mean leaving
 * the log to open 资源, by which time the interesting lines have scrolled.
 * This is deliberately not that page: two meters and a timestamp, no history,
 * no controls. The curve stays one click away and keeps its own tab.
 */
export function ConsoleStatus({ instance, active, onOpenResources, onCollapse }: Props) {
  const [data, setData] = useState<InstanceMetrics | null>(null)
  const live = isLive(instance.state)

  useEffect(() => {
    if (!active || !live) return
    let cancelled = false

    const load = async () => {
      try {
        const fetched = await api.instanceMetrics(instance.id)
        if (!cancelled) setData(fetched)
      } catch {
        // Informational only; the console itself reports a lost connection.
      }
    }

    void load()
    // The server samples every five seconds, so anything faster re-sends the
    // same point and anything slower lags the log it sits beside.
    const timer = window.setInterval(() => void load(), 5000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [instance.id, active, live])

  const latest = data?.samples[data.samples.length - 1]
  const xmxBytes = data && data.maxMemoryMB > 0 ? data.maxMemoryMB * 1024 * 1024 : 0

  return (
    <aside className="console-side">
      <div className="console-side__head">
        <h3 className="panel__title">运行状态</h3>
        <button className="link" onClick={onCollapse}>
          收起
        </button>
      </div>

      {!live ? (
        <p className="console-side__idle">服务器未运行。</p>
      ) : !latest ? (
        <p className="console-side__idle">正在等第一个采样点…</p>
      ) : (
        <>
          <Meter
            label="CPU"
            percent={latest.cpuPercent}
            detail={`本机 ${data?.cpuCores ?? 0} 核 · 100% 为占满一核`}
          />
          <Meter
            label="内存"
            // Against -Xmx when there is one: "2.4 GB" means nothing without
            // the ceiling the JVM was told to respect.
            percent={xmxBytes > 0 ? (latest.memoryBytes / xmxBytes) * 100 : 0}
            detail={
              xmxBytes > 0
                ? `${formatBytes(latest.memoryBytes)} / ${formatBytes(xmxBytes)}`
                : formatBytes(latest.memoryBytes)
            }
          />
          <p className="console-side__meta">
            {instance.pid ? <span>PID {instance.pid}</span> : null}
            <span>{latest.processes} 个进程</span>
            <span>采样于 {formatTime(latest.time)}</span>
          </p>
        </>
      )}

      <button className="btn" onClick={onOpenResources}>
        查看曲线
      </button>

      {live && latest && (
        <p className="console-side__note">
          CPU 峰值贴着 {formatPercent(100)} 通常是主线程跑满了，加核心不会有帮助。
        </p>
      )}
    </aside>
  )
}
