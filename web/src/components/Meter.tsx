import { formatPercent } from '../format'
import { useTween } from '../useTween'

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
  // The true reading, not the animated one. A bar going amber is a statement
  // about the machine and it should be made the instant the sample says so;
  // hanging the colour off the tween would mean the warning arrives a fifth of
  // a second late and, worse, that it arrives *gradually*.
  const severity = percent >= 90 ? 'critical' : percent >= 75 ? 'warning' : 'ok'
  // The fill's width is a CSS transition on the same duration and the same
  // curve (--dur-data / --ease-out), so the digits and the bar travel together
  // rather than one chasing the other.
  const shown = useTween(percent)

  return (
    <div className="meter">
      <div className="meter__head">
        <span className="meter__label">{label}</span>
        <span className="meter__value">{formatPercent(shown)}</span>
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
