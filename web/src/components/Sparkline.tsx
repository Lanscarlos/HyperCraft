interface Props {
  values: number[]
  /** Y-axis ceiling. Fixed rather than fitted, so a flat idle line stays flat
   *  instead of being stretched into dramatic-looking noise. */
  max: number
  color?: string
  ariaLabel: string
}

const WIDTH = 120
const HEIGHT = 28

/**
 * A metric's recent shape, small enough to sit inside a stat tile.
 *
 * A single instantaneous number cannot tell a steady 60% from a spike that
 * happens to be caught at 60% on its way past 100 — and for the metrics this
 * panel shows, that difference is the entire question. So the tiles carry the
 * last few minutes rather than the last sample.
 */
export function Sparkline({ values, max, color = 'currentColor', ariaLabel }: Props) {
  if (values.length < 2) {
    return <div className="spark spark--empty" aria-hidden="true" />
  }

  const ceiling = Math.max(max, ...values) || 1
  const step = WIDTH / (values.length - 1)
  const y = (value: number) => HEIGHT - (Math.max(0, value) / ceiling) * (HEIGHT - 2) - 1
  const line = values.map((value, index) => `${index * step},${y(value)}`).join(' ')

  return (
    <svg
      className="spark"
      viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
      preserveAspectRatio="none"
      role="img"
      aria-label={ariaLabel}
    >
      <polyline
        points={`0,${HEIGHT} ${line} ${WIDTH},${HEIGHT}`}
        fill={color}
        fillOpacity="0.14"
        stroke="none"
      />
      <polyline
        points={line}
        fill="none"
        stroke={color}
        strokeWidth="1.6"
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}
