import type { PluginCompat } from '../types'

/**
 * The compatibility verdict, as a badge.
 *
 * A first-class thing on every row that shows a plugin, because it is the one
 * fact that can end a decision on its own. Green says it fits. Yellow says it
 * does not, and says how far it got: "最高支持 1.16.5" is a useful sentence,
 * "不兼容" is not.
 *
 * There is no grey. 未知兼容性 was a third badge and it was the one that
 * actually rendered, on every row, all the way down the page — because the
 * common case is arriving with no reference server chosen, and then nothing is
 * knowable about anything. A column where every cell reads the same thing is
 * not information; it is the most prominent position on the row spent on
 * saying nothing, and it pushed the name and the author to the right to say
 * it. So an unknown verdict draws nothing, the row closes up, and whoever
 * needed to know why gets told once — beside the filter that would fix it, or
 * in the row's meta line when it is the source that stayed quiet.
 */
export function CompatBadge({ compat }: { compat?: PluginCompat }) {
  if (!compat || compat.state === 'unknown') return null

  return (
    <span
      className={`badge ${compat.state === 'ok' ? 'badge--ok' : 'badge--warn'}`}
      title={compat.detail || compat.label}
    >
      {compat.label}
    </span>
  )
}
