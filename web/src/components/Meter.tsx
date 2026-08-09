import { formatPercent } from '../format'

/**
 * A usage meter. Severity rides the fill colour, but the percentage is always
 * spelled out beside it — colour never carries the meaning alone.
 */
export function Meter({
  label,
  percent,
  detail,
}: {
  label: string
  percent: number
  detail: string
}) {
  const severity = percent >= 90 ? 'critical' : percent >= 75 ? 'warning' : 'ok'

  return (
    <div className="meter">
      <div className="meter__head">
        <span className="meter__label">{label}</span>
        <span className="meter__value">{formatPercent(percent)}</span>
      </div>
      <div className="meter__track">
        <div
          className={`meter__fill meter__fill--${severity}`}
          style={{ width: `${Math.min(100, Math.max(0, percent))}%` }}
        />
      </div>
      <p className="meter__detail">{detail}</p>
    </div>
  )
}
