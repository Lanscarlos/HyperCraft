import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatPercent } from '../format'
import type { SystemInfo } from '../types'
import { Meter } from './Meter'
import { TimeSeriesChart, type Point } from './TimeSeriesChart'

const HOST_CPU_COLOR = 'var(--series-cpu)'
const HOST_WINDOW_MS = 30 * 60_000

/**
 * Machine-level usage above the instance cards.
 *
 * Memory and disk are meters rather than charts: the reader's question is
 * "how much room is left", which a single filled bar answers better than a
 * timeline. CPU gets the chart because its shape over time is the part that
 * matters.
 */
export function HostOverview() {
  const [system, setSystem] = useState<SystemInfo | null>(null)
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const fetched = await api.system()
        if (!cancelled) setSystem(fetched)
      } catch {
        // The overview is informational; the instance list already surfaces
        // connection problems.
      }
    }
    void load()
    const timer = window.setInterval(() => void load(), 5000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [])

  const cpuPoints = useMemo<Point[]>(() => {
    if (!system) return []
    const samples = system.samples.map((s) => ({
      t: new Date(s.time).getTime(),
      v: s.cpuPercent,
    }))
    const newest = samples.length > 0 ? samples[samples.length - 1].t : Date.now()
    return samples.filter((s) => s.t >= newest - HOST_WINDOW_MS)
  }, [system])

  if (!system) return null

  const latest = system.samples[system.samples.length - 1]
  const memoryPercent = latest?.memoryPercent ?? 0
  const diskUsedPercent = system.disk.percent

  return (
    <section className="host">
      <div className="host__head">
        <h2>本机资源</h2>
        <p className="host__meta">
          {system.host.hostname && <span>{system.host.hostname}</span>}
          {system.host.platform && <span>{system.host.platform}</span>}
          <span>{system.host.cpuCores} 核</span>
          <span>
            {system.instances.running}/{system.instances.total} 个实例运行中
          </span>
        </p>
      </div>

      <div className="meters">
        <Meter
          label="内存"
          percent={memoryPercent}
          detail={`${formatBytes(latest?.memoryUsed ?? 0)} / ${formatBytes(system.host.memoryTotal)}`}
        />
        <Meter
          label="磁盘"
          percent={diskUsedPercent}
          detail={`剩余 ${formatBytes(system.disk.free)} / ${formatBytes(system.disk.total)}`}
        />
        <div className="meter">
          <div className="meter__head">
            <span className="meter__label">面板自身</span>
            <span className="meter__value">{formatBytes(system.panel.heapBytes)}</span>
          </div>
          <p className="meter__detail">
            {system.panel.goroutines} 个协程 · {system.goVersion}
          </p>
        </div>
      </div>

      <div className="host__chart">
        <div className="chart-head">
          <h3 className="panel__title">CPU 占用（全机）</h3>
          <p className="chart-head__meta">最近 30 分钟</p>
        </div>
        <TimeSeriesChart
          points={cpuPoints}
          color={HOST_CPU_COLOR}
          format={(v) => formatPercent(v)}
          windowMs={HOST_WINDOW_MS}
          minYMax={100}
          hoverIndex={hoverIndex}
          onHover={setHoverIndex}
          ariaLabel="本机 CPU 占用曲线，最近 30 分钟"
        />
      </div>
    </section>
  )
}
