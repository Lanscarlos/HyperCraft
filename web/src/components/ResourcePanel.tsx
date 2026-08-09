import { useEffect, useMemo, useRef, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatPercent, formatTime } from '../format'
import type { InstanceMetrics, InstanceStatus } from '../types'
import { TimeSeriesChart, type Point } from './TimeSeriesChart'

// Named rather than literal: each theme steps the pair for its own chart
// surface, and both steps are validated where they are defined (styles.css).
const CPU_COLOR = 'var(--series-cpu)'
const MEMORY_COLOR = 'var(--series-memory)'

const RANGES = [
  { label: '5 分钟', ms: 5 * 60_000 },
  { label: '30 分钟', ms: 30 * 60_000 },
  { label: '1 小时', ms: 60 * 60_000 },
] as const

export function ResourcePanel({ instance }: { instance: InstanceStatus }) {
  const [data, setData] = useState<InstanceMetrics | null>(null)
  const [error, setError] = useState<string | null>(null)
  // 5 minutes by default: a freshly started panel has minutes of history, and
  // a 30-minute window would squeeze all of it against the right edge.
  const [rangeMs, setRangeMs] = useState<number>(RANGES[0].ms)
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)
  const [showTable, setShowTable] = useState(false)

  // Kept in a ref so the poll interval does not restart on every response.
  const instanceId = useRef(instance.id)
  instanceId.current = instance.id

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const fetched = await api.instanceMetrics(instanceId.current)
        if (!cancelled) {
          setData(fetched)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : '读取监控数据失败')
      }
    }

    void load()
    // Match the server's sampling cadence: polling faster only re-sends the
    // same points, and slower makes the chart lag behind reality.
    const timer = window.setInterval(() => void load(), 5000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [instance.id])

  const windowed = useMemo(() => {
    if (!data) return { cpu: [] as Point[], memory: [] as Point[], samples: [] }

    const samples = data.samples.map((s) => ({ ...s, ms: new Date(s.time).getTime() }))
    const newest = samples.length > 0 ? samples[samples.length - 1].ms : Date.now()
    const visible = samples.filter((s) => s.ms >= newest - rangeMs)

    return {
      cpu: visible.map((s) => ({ t: s.ms, v: s.cpuPercent })),
      memory: visible.map((s) => ({ t: s.ms, v: s.memoryBytes })),
      samples: visible,
    }
  }, [data, rangeMs])

  const stats = useMemo(() => {
    const { cpu, memory } = windowed
    const peak = (points: Point[]) => points.reduce((max, p) => Math.max(max, p.v), 0)
    const mean = (points: Point[]) =>
      points.length === 0 ? 0 : points.reduce((sum, p) => sum + p.v, 0) / points.length
    return {
      cpuPeak: peak(cpu),
      cpuMean: mean(cpu),
      memPeak: peak(memory),
      memMean: mean(memory),
    }
  }, [windowed])

  if (error) {
    return <div className="alert alert--error">{error}</div>
  }
  if (!data) {
    return <div className="panel">加载中…</div>
  }

  const xmxBytes = data.maxMemoryMB > 0 ? data.maxMemoryMB * 1024 * 1024 : 0

  return (
    <div className="settings">
      {/* Filters sit in one row above everything they scope. */}
      <div className="chart-filters">
        <span className="chart-filters__label">时间范围</span>
        {RANGES.map((range) => (
          <button
            key={range.ms}
            type="button"
            className={`chip${rangeMs === range.ms ? ' chip--active' : ''}`}
            onClick={() => setRangeMs(range.ms)}
          >
            {range.label}
          </button>
        ))}
        <button
          type="button"
          className={`chip chip--right${showTable ? ' chip--active' : ''}`}
          onClick={() => setShowTable((prev) => !prev)}
        >
          数据表
        </button>
      </div>

      <section className="panel">
        <div className="chart-head">
          <h3 className="panel__title">CPU 占用</h3>
          <p className="chart-head__meta">
            峰值 {formatPercent(stats.cpuPeak)} · 平均 {formatPercent(stats.cpuMean)} ·
            本机 {data.cpuCores} 核
          </p>
        </div>
        <TimeSeriesChart
          points={windowed.cpu}
          color={CPU_COLOR}
          format={(v) => formatPercent(v)}
          windowMs={rangeMs}
          minYMax={100}
          reference={{ value: 100, label: '1 核' }}
          hoverIndex={hoverIndex}
          onHover={setHoverIndex}
          ariaLabel={`CPU 占用曲线，峰值 ${formatPercent(stats.cpuPeak)}，平均 ${formatPercent(stats.cpuMean)}`}
        />
        <p className="chart-note">
          按单核计算，100% 表示占满一个核心。Minecraft 主线程基本是单线程的，
          所以接近 100% 通常意味着主线程已经跑满，加核心不会有帮助。
        </p>
      </section>

      <section className="panel">
        <div className="chart-head">
          <h3 className="panel__title">内存占用</h3>
          <p className="chart-head__meta">
            峰值 {formatBytes(stats.memPeak)} · 平均 {formatBytes(stats.memMean)}
            {xmxBytes > 0 && ` · 上限 ${formatBytes(xmxBytes)}`}
          </p>
        </div>
        <TimeSeriesChart
          points={windowed.memory}
          color={MEMORY_COLOR}
          format={(v) => formatBytes(v, 1)}
          windowMs={rangeMs}
          minYMax={256 * 1024 * 1024}
          scale="binary"
          reference={xmxBytes > 0 ? { value: xmxBytes, label: '-Xmx' } : undefined}
          hoverIndex={hoverIndex}
          onHover={setHoverIndex}
          ariaLabel={`内存占用曲线，峰值 ${formatBytes(stats.memPeak)}`}
        />
        <p className="chart-note">
          统计的是进程树的物理内存 (RSS)，含 JVM 堆外开销，所以会比 -Xmx 略高一些。
        </p>
      </section>

      {showTable && (
        <section className="panel">
          <h3 className="panel__title">采样数据</h3>
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>CPU</th>
                  <th>内存</th>
                  <th>进程数</th>
                </tr>
              </thead>
              <tbody>
                {[...windowed.samples].reverse().slice(0, 60).map((sample) => (
                  <tr key={sample.time}>
                    <td>{formatTime(sample.time)}</td>
                    <td>{formatPercent(sample.cpuPercent, 1)}</td>
                    <td>{formatBytes(sample.memoryBytes)}</td>
                    <td>{sample.processes}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {windowed.samples.length > 60 && (
            <p className="chart-note">仅显示最近 60 条，共 {windowed.samples.length} 条。</p>
          )}
        </section>
      )}
    </div>
  )
}
