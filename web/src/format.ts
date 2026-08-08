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

export function formatPercent(value: number, digits = 0): string {
  if (!Number.isFinite(value)) return '0%'
  return `${value.toFixed(digits)}%`
}

export function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}
