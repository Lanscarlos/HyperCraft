import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

export interface Point {
  /** Epoch milliseconds. */
  t: number
  v: number
}

interface Props {
  points: Point[]
  /** Series colour. One series per chart, so there is no legend to key. */
  color: string
  /** Renders a value for the axis, the end label and the tooltip. */
  format: (value: number) => string
  /** Time window in milliseconds; the x-axis always spans it, so points
   *  slide in from the right instead of the axis rescaling every tick. */
  windowMs: number
  /** Lower bound for the y-axis so an idle server is not all noise. */
  minYMax: number
  /** Axis rounding. Byte scales round on powers of 1024, so ticks read
   *  "256 MB" and "512 MB" rather than "286.1 MB". */
  scale?: 'decimal' | 'binary'
  /** Optional horizontal marker, e.g. one full core or the -Xmx ceiling. */
  reference?: { value: number; label: string }
  /** Index shared between the charts so one hover moves both crosshairs. */
  hoverIndex: number | null
  onHover: (index: number | null) => void
  ariaLabel: string
}

const PADDING = { top: 14, right: 62, bottom: 22, left: 56 }
const HEIGHT = 168

/**
 * A single-series area chart over time.
 *
 * CPU and memory get one chart each rather than sharing a dual-axis plot:
 * two y-scales on one frame make the crossing point look meaningful when it
 * is an artefact of the scaling.
 */
export function TimeSeriesChart({
  points,
  color,
  format,
  windowMs,
  minYMax,
  scale = 'decimal',
  reference,
  hoverIndex,
  onHover,
  ariaLabel,
}: Props) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const [width, setWidth] = useState(640)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const observer = new ResizeObserver(([entry]) => {
      setWidth(Math.max(240, entry.contentRect.width))
    })
    observer.observe(host)
    return () => observer.disconnect()
  }, [])

  const geometry = useMemo(() => {
    const plotWidth = width - PADDING.left - PADDING.right
    const plotHeight = HEIGHT - PADDING.top - PADDING.bottom

    // Anchor the window to the newest sample rather than to wall-clock now,
    // so a paused tab does not show the line drifting away from the edge.
    const end = points.length > 0 ? points[points.length - 1].t : Date.now()
    const start = end - windowMs

    // The axis follows the data, not the reference line. Forcing a -Xmx 8G
    // ceiling onto a server that peaks at 1.5G would squash the whole shape
    // into the bottom fifth of the chart; the line reappears on its own once
    // usage climbs near the cap, which is when it matters.
    const peak = points.reduce((max, p) => Math.max(max, p.v), 0)
    const yMax = niceCeiling(Math.max(peak * 1.15, minYMax), scale)

    const x = (t: number) =>
      PADDING.left + ((t - start) / windowMs) * plotWidth
    const y = (v: number) =>
      PADDING.top + plotHeight - (Math.min(v, yMax) / yMax) * plotHeight

    return { plotWidth, plotHeight, start, end, yMax, x, y, peak }
  }, [width, points, windowMs, minYMax, scale])

  const { line, area } = useMemo(() => {
    if (points.length === 0) return { line: '', area: '' }

    const coords = points.map((p) => `${geometry.x(p.t).toFixed(1)},${geometry.y(p.v).toFixed(1)}`)
    const linePath = `M${coords.join('L')}`
    const baseline = geometry.y(0).toFixed(1)
    const first = geometry.x(points[0].t).toFixed(1)
    const last = geometry.x(points[points.length - 1].t).toFixed(1)

    return {
      line: linePath,
      area: `${linePath}L${last},${baseline}L${first},${baseline}Z`,
    }
  }, [points, geometry])

  // The crosshair snaps to the nearest sample: readers aim at a moment in
  // time, never at a 2px line.
  const handleMove = useCallback(
    (event: React.PointerEvent<SVGRectElement>) => {
      if (points.length === 0) return
      const rect = event.currentTarget.getBoundingClientRect()
      const ratio = (event.clientX - rect.left) / rect.width
      const targetTime = geometry.start + ratio * windowMs

      let nearest = 0
      let bestGap = Infinity
      for (let i = 0; i < points.length; i++) {
        const gap = Math.abs(points[i].t - targetTime)
        if (gap < bestGap) {
          bestGap = gap
          nearest = i
        }
      }
      onHover(nearest)
    },
    [points, geometry, windowMs, onHover],
  )

  const ticks = useMemo(() => yTicks(geometry.yMax), [geometry.yMax])
  const latest = points.length > 0 ? points[points.length - 1] : null
  const hovered =
    hoverIndex != null && hoverIndex >= 0 && hoverIndex < points.length
      ? points[hoverIndex]
      : null

  if (points.length === 0) {
    return (
      <div className="chart chart--empty" ref={hostRef}>
        正在采集数据…
      </div>
    )
  }

  return (
    <div className="chart" ref={hostRef}>
      <svg
        width={width}
        height={HEIGHT}
        role="img"
        aria-label={ariaLabel}
        className="chart__svg"
      >
        {/* Gridlines: hairline, solid, one step off the surface. */}
        {ticks.map((tick) => (
          <g key={tick}>
            <line
              className="chart__grid"
              x1={PADDING.left}
              x2={width - PADDING.right}
              y1={geometry.y(tick)}
              y2={geometry.y(tick)}
            />
            <text
              className="chart__tick"
              x={PADDING.left - 8}
              y={geometry.y(tick)}
              textAnchor="end"
              dominantBaseline="middle"
            >
              {format(tick)}
            </text>
          </g>
        ))}

        {reference && reference.value <= geometry.yMax && (
          <g>
            <line
              className="chart__reference"
              x1={PADDING.left}
              x2={width - PADDING.right}
              y1={geometry.y(reference.value)}
              y2={geometry.y(reference.value)}
            />
            <text
              className="chart__reference-label"
              x={width - PADDING.right + 6}
              y={geometry.y(reference.value)}
              dominantBaseline="middle"
            >
              {reference.label}
            </text>
          </g>
        )}

        {/* Area is a 10% wash; the 2px line carries the shape. */}
        <path d={area} fill={color} fillOpacity={0.1} />
        <path
          d={line}
          fill="none"
          stroke={color}
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
        />

        {hovered && (
          <g>
            <line
              className="chart__crosshair"
              x1={geometry.x(hovered.t)}
              x2={geometry.x(hovered.t)}
              y1={PADDING.top}
              y2={HEIGHT - PADDING.bottom}
            />
            {/* 2px surface ring keeps the marker legible over the line. */}
            <circle
              cx={geometry.x(hovered.t)}
              cy={geometry.y(hovered.v)}
              r={4}
              fill={color}
              stroke="var(--chart-surface)"
              strokeWidth={2}
            />
          </g>
        )}

        {latest && !hovered && (
          <g>
            <circle
              cx={geometry.x(latest.t)}
              cy={geometry.y(latest.v)}
              r={4}
              fill={color}
              stroke="var(--chart-surface)"
              strokeWidth={2}
            />
            {/* The one direct label: the current value at the line's end. */}
            <text
              className="chart__end-label"
              x={geometry.x(latest.t) + 9}
              y={geometry.y(latest.v)}
              dominantBaseline="middle"
            >
              {format(latest.v)}
            </text>
          </g>
        )}

        <line
          className="chart__axis"
          x1={PADDING.left}
          x2={width - PADDING.right}
          y1={geometry.y(0)}
          y2={geometry.y(0)}
        />

        <rect
          x={PADDING.left}
          y={PADDING.top}
          width={Math.max(1, geometry.plotWidth)}
          height={geometry.plotHeight}
          fill="transparent"
          onPointerMove={handleMove}
          onPointerLeave={() => onHover(null)}
        />
      </svg>

      <div className="chart__axis-labels">
        <span>{formatClock(geometry.start)}</span>
        <span>{formatClock(geometry.end)}</span>
      </div>

      {hovered && (
        <div
          className="chart__tooltip"
          style={{
            left: `${Math.min(Math.max(geometry.x(hovered.t), 70), width - 70)}px`,
          }}
        >
          {/* Value leads, label follows: the reader has the series already. */}
          <strong>{format(hovered.v)}</strong>
          <span>{formatClock(hovered.t)}</span>
        </div>
      )}
    </div>
  )
}

/**
 * Rounds an axis ceiling up to a readable number.
 *
 * Byte axes need their own ladder: the decimal one lands on values like
 * 3e8, which formatBytes renders as "286.1 MB" — a tick nobody reads as a
 * round number. Stepping on powers of two under a 1024 magnitude gives
 * 256 MB / 512 MB / 1 GB instead, and halving those stays clean too.
 */
function niceCeiling(value: number, scale: 'decimal' | 'binary'): number {
  if (value <= 0) return 1

  if (scale === 'binary') {
    const magnitude = 1024 ** Math.floor(Math.log(value) / Math.log(1024))
    for (const step of [1, 2, 4, 8, 16, 32, 64, 128, 256, 512]) {
      if (value <= step * magnitude) return step * magnitude
    }
    return 1024 * magnitude
  }

  const magnitude = 10 ** Math.floor(Math.log10(value))
  for (const step of [1, 1.5, 2, 2.5, 3, 4, 5, 7.5, 10]) {
    if (value <= step * magnitude) return step * magnitude
  }
  return 10 * magnitude
}

function yTicks(yMax: number): number[] {
  return [0, yMax / 2, yMax]
}

function formatClock(ms: number): string {
  return new Date(ms).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}
