import { useEffect, useMemo, useRef, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatPercent, formatTime } from '../format'
import { cpuVerdict, instanceEvents, memoryVerdict } from '../instanceEvents'
import type { Verdict } from '../instanceEvents'
import type { InstanceMetrics, InstanceStatus } from '../types'
import { isLive } from '../types'
import { Card } from './Card'
import { PageHead } from './Page'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'
import { CHART_HEIGHT, TimeSeriesChart, type Point } from './TimeSeriesChart'

// Named rather than literal: each theme steps the pair for its own chart
// surface, and both steps are validated where they are defined (styles.css).
const CPU_COLOR = 'var(--series-cpu)'
const MEMORY_COLOR = 'var(--series-memory)'

const RANGES = [
  { label: '5 分钟', ms: 5 * 60_000 },
  { label: '30 分钟', ms: 30 * 60_000 },
  { label: '1 小时', ms: 60 * 60_000 },
] as const

interface Props {
  instance: InstanceStatus
  /** False while another tab is in front. The pane stays mounted so coming
   *  back to it is instant, which would otherwise mean a hidden chart polling
   *  every five seconds for the rest of the session — so the poll stops with
   *  the pane and takes a fresh sample the moment it is back in front. Same
   *  contract as ConsoleStatus, for the same reason. */
  active: boolean
}

export function ResourcePanel({ instance, active }: Props) {
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
    if (!active) return
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
  }, [instance.id, active])

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

  const rangeLabel = RANGES.find((r) => r.ms === rangeMs)?.label ?? ''

  // The ceiling that will really apply, which is not maxMemoryMB the moment the
  // launch is a list of @argfiles — see the note on the field in types.ts.
  const ceiling =
    instance.effectiveMaxMemoryMB > 0 ? instance.effectiveMaxMemoryMB * 1024 * 1024 : 0

  // Only while it is running. A stopped server's last sample is a reading from
  // a process that no longer exists, and "CPU 0% 正常" over a server that is
  // down is the page reporting health it has no evidence for — the same reason
  // the cockpit's tiles and the top bar's strip show an em dash.
  const live = isLive(instance.state)
  const latest = live ? (windowed.samples[windowed.samples.length - 1] ?? null) : null

  const events = useMemo(
    () => instanceEvents(instance, windowed.samples, rangeLabel),
    [instance, windowed.samples, rangeLabel],
  )

  // The same head over the charts, the placeholder and the error, so the
  // section opens in the same place whichever of the three it is showing.
  const head = (
    <PageHead
      title="监控"
      lead="这台服务器自己的 CPU 和内存曲线。整机的负载在「主机 → 监控」。"
    />
  )

  if (error) {
    return (
      <div className="stack">
        {head}
        <div className="alert alert--error">{error}</div>
      </div>
    )
  }
  if (!data) {
    // Two cards with a chart-sized hole in each: the same shape the answer
    // arrives in, so the charts do not shove the page down when they land.
    return (
      <div className="stack">
        {head}
        <SkeletonScreen inPage label="正在读取监控数据…">
        <div className="chart-filters">
          <Skeleton w="56px" h={24} pill />
          <Skeleton w="56px" h={24} pill />
          <Skeleton w="56px" h={24} pill />
        </div>
        {['cpu', 'memory'].map((key) => (
          <SkeletonPanel key={key} title={false}>
            <div className="chart-head">
              <Skeleton w="88px" h={15} />
              <Skeleton w="42%" h={12} />
            </div>
            <Skeleton w="100%" h={CHART_HEIGHT} />
            <Skeleton w="70%" h={12} />
          </SkeletonPanel>
        ))}
        </SkeletonScreen>
      </div>
    )
  }

  const xmxBytes = data.maxMemoryMB > 0 ? data.maxMemoryMB * 1024 * 1024 : 0

  return (
    <div className="stack">
      {head}
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

      {/* The conclusion first, the curve under it. Reading a number off a chart
          still leaves "and is that bad?" unanswered, and that question has a
          defensible answer here — thresholds and their reasoning are in
          instanceEvents.ts rather than scattered through this file. */}
      <div className="kpis">
        <Kpi
          label="CPU"
          value={latest ? formatPercent(latest.cpuPercent) : '—'}
          verdict={live && windowed.samples.length > 0 ? cpuVerdict(stats.cpuMean) : null}
          note={
            live && windowed.samples.length > 0
              ? `${rangeLabel}平均 ${formatPercent(stats.cpuMean)} · 本机 ${data.cpuCores} 核`
              : live
                ? '还没有采样'
                : `服务器没有在运行 · 本机 ${data.cpuCores} 核`
          }
        />
        <Kpi
          label="内存"
          value={latest ? formatBytes(latest.memoryBytes) : '—'}
          verdict={latest ? memoryVerdict(stats.memPeak, ceiling) : null}
          note={
            ceiling > 0
              ? live
                ? `峰值 ${formatBytes(stats.memPeak)} · 上限 ${formatBytes(ceiling)}`
                : `上限 ${formatBytes(ceiling)}`
              : '没有设置 -Xmx，画不出参照'
          }
        />
        <Kpi
          label="进程数"
          value={latest ? String(latest.processes) : '—'}
          verdict={null}
          note={`采样间隔 ${Math.round(data.intervalSeconds)} 秒`}

        />
      </div>

      <div className="metrics">
        <div className="metrics__charts">

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

        </div>

        {/* The rail. Beside the charts where there is room for it, under them
            where there is not — the same 1280px the console's own right-hand
            column appears at. */}
        <aside className="metrics__events">
          <h3 className="panel__title">这段时间</h3>
          <ul className="events">
            {events.map((event) => (
              <li className={`events__item events__item--${event.level}`} key={event.id}>
                <strong>{event.title}</strong>
                {event.detail && <span>{event.detail}</span>}
              </li>
            ))}
          </ul>
        </aside>
      </div>

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

/**
 * One reading with a verdict on it.
 *
 * The verdict is the point: a number is only actionable next to the range it
 * is supposed to be in, and every operator working that out for themselves
 * from a chart is the panel making them do arithmetic it could have done. No
 * verdict at all is shown rather than a guessed one — a memory card with no
 * -Xmx to measure against says so in the note instead.
 */
function Kpi({
  label,
  value,
  verdict,
  note,
}: {
  label: string
  value: string
  verdict: Verdict | null
  note: string
}) {
  return (
    <Card pad="tight" className="kpi">
      <div className="kpi__head">
        <span className="kpi__label">{label}</span>
        {verdict && <span className={`kpi__verdict kpi__verdict--${verdict.level}`}>{verdict.label}</span>}
      </div>
      <strong className="kpi__value">{value}</strong>
      <small className="kpi__note">{note}</small>
    </Card>
  )
}
