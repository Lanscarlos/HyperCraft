import type { PluginCompat } from '../types'

/**
 * The compatibility verdict, as a badge.
 *
 * A first-class thing on every row that shows a plugin, because it is the one
 * fact that can end a decision on its own — and because the three states have
 * to look different at a glance. Green says it fits. Yellow says it does not,
 * and says how far it got: "最高支持 1.16.5" is a useful sentence, "不兼容" is
 * not. Grey says nobody knows, which is a real answer and must never be
 * painted like the green one.
 */
export function CompatBadge({ compat }: { compat?: PluginCompat }) {
  if (!compat) return null

  const tone =
    compat.state === 'ok' ? 'badge--ok' : compat.state === 'bad' ? 'badge--warn' : 'badge--muted'
  return (
    <span className={`badge ${tone}`} title={compat.detail || compat.label}>
      {compat.label}
    </span>
  )
}
