/** Shared value formatting for charts, tables and stat tiles. */

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB']

export function formatBytes(bytes: number, digits = 1): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'

  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024
    unit++
  }
  // Whole bytes and kilobytes never need a decimal point, and a round value
  // reads better as "256 MB" than "256.0 MB" — axis ticks land on these.
  const places = unit <= 1 ? 0 : digits
  const text = value.toFixed(places).replace(/\.0+$/, '')
  return `${text} ${BYTE_UNITS[unit]}`
}

/** A transfer rate. Bytes per second rather than bits: everything else in the
 *  panel — memory, disk, a jar's size — is bytes, and a chart that switched
 *  units halfway down the page would be read wrong long before it was noticed. */
export function formatRate(bytesPerSecond: number): string {
  return `${formatBytes(bytesPerSecond)}/s`
}

export function formatPercent(value: number, digits = 0): string {
  if (!Number.isFinite(value)) return '0%'
  return `${value.toFixed(digits)}%`
}

/** Day and minute, for "installed at" / "added at" lines. Seconds would be
 *  noise: nobody cares which second a 60 MB download finished on. */
export function formatDate(iso: string): string {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/**
 * How long ago something happened, in words.
 *
 * A file list is read as a sequence — which of these did the server touch just
 * now, which have not moved since the world was created — and 2026/8/9 21:44
 * makes that a subtraction the reader has to do in their head for every row.
 * The exact stamp is still one hover away wherever this is used; it is the
 * ordering that belongs in the column.
 *
 * Past a week the elapsed form stops being the more useful of the two ("47 天
 * 前" is not a date anybody can place), so it hands back to formatDate.
 */
export function formatSince(iso: string, now: number = Date.now()): string {
  const at = new Date(iso).getTime()
  if (Number.isNaN(at)) return ''

  const elapsed = now - at
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour

  // A clock that is ahead of the panel's is a fact about the two machines, not
  // about the file; showing "-3 分钟前" would only look broken.
  if (elapsed < 0) return formatDate(iso)
  if (elapsed < minute) return '刚刚'
  if (elapsed < hour) return `${Math.floor(elapsed / minute)} 分钟前`
  if (elapsed < day) return `${Math.floor(elapsed / hour)} 小时前`
  if (elapsed < 7 * day) return `${Math.floor(elapsed / day)} 天前`
  return formatDate(iso)
}

export function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}
