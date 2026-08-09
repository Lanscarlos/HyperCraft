import { formatBytes, formatPercent } from './format'
import type { Route } from './routes'
import type { InstanceStatus, SystemInfo, UpdateStatus } from './types'
import { isLive } from './types'

const MIB = 1024 * 1024

/** Below this share of the disk left, a Minecraft world is one save away from
 *  a truncated region file. It is the most destructive failure this panel has
 *  to warn about, so it is red rather than amber. */
export const DISK_CRITICAL_FREE = 0.15
/** Enough room left to be fine today and not next month. */
export const DISK_WARN_FREE = 0.25

export type AlertLevel = 'error' | 'warn' | 'info'

export interface PanelAlert {
  id: string
  level: AlertLevel
  title: string
  detail?: string
  /** Where the fix is. Every alert has one — an alert you cannot act on from
   *  is a decoration. */
  action: { label: string; route: Route }
}

/** The machine's memory, in the three numbers that actually decide anything. */
export interface HostMemory {
  /** What the machine has. */
  total: number
  /** What is in use right now, everything on the box included. */
  used: number
  /** Sum of -Xmx over the servers that are up: memory already promised away. */
  committedLive: number
  /** The same sum over every instance, running or not: what starting them all
   *  would promise. Above `total` this machine cannot run its own inventory. */
  committedAll: number
  /** True when the running servers alone are allowed to ask for more than the
   *  machine has. The kernel resolves that by killing one of them. */
  overcommitted: boolean
}

export function hostMemory(instances: InstanceStatus[], system: SystemInfo | null): HostMemory {
  const latest = system?.samples[system.samples.length - 1]
  const committedLive = instances
    .filter((item) => isLive(item.state))
    .reduce((sum, item) => sum + Math.max(0, item.maxMemoryMB) * MIB, 0)
  const committedAll = instances.reduce(
    (sum, item) => sum + Math.max(0, item.maxMemoryMB) * MIB,
    0,
  )
  const total = system?.host.memoryTotal ?? 0
  return {
    total,
    used: latest?.memoryUsed ?? 0,
    committedLive,
    committedAll,
    overcommitted: total > 0 && committedLive > total,
  }
}

/** The share of the disk still free, 0–1. */
export function diskFreeRatio(system: SystemInfo | null): number {
  if (!system || system.disk.total <= 0) return 1
  return system.disk.free / system.disk.total
}

/**
 * How full the disk is, derived from free-against-total rather than taken from
 * the collector's own `percent`.
 *
 * The two do not always agree — a filesystem reserves blocks that count as
 * neither free nor used — and a page that shows "21% used" next to "only 12%
 * left" has told the reader nothing except that it cannot be trusted. One
 * definition, used everywhere, and it is the one the warning is about.
 */
export function diskUsedPercent(system: SystemInfo | null): number {
  return (1 - diskFreeRatio(system)) * 100
}

/**
 * Everything asking for a decision, worst first.
 *
 * This is the single source for both the overview's alert block and its
 * "待处理告警" count, so the number on the summary bar is by construction the
 * length of the list underneath it.
 */
export function collectAlerts(input: {
  instances: InstanceStatus[]
  system: SystemInfo | null
  update: UpdateStatus | null
  pluginUpdates: number
}): PanelAlert[] {
  const { instances, system, update, pluginUpdates } = input
  const alerts: PanelAlert[] = []

  for (const item of instances) {
    if (item.state !== 'crashed') continue
    alerts.push({
      id: `crash:${item.id}`,
      level: 'error',
      title: `${item.name} 意外退出`,
      detail:
        item.exitCode != null
          ? `退出码 ${item.exitCode}${item.message ? ` · ${item.message}` : ''}`
          : (item.message ?? '进程不是被面板停止的'),
      action: {
        label: '看控制台',
        route: { kind: 'instance', id: item.id, section: 'console' },
      },
    })
  }

  if (system) {
    const free = diskFreeRatio(system)
    if (free < DISK_WARN_FREE) {
      alerts.push({
        id: 'disk',
        level: free < DISK_CRITICAL_FREE ? 'error' : 'warn',
        title:
          free < DISK_CRITICAL_FREE
            ? `磁盘只剩 ${formatPercent(free * 100)}`
            : `磁盘剩余 ${formatPercent(free * 100)}`,
        detail:
          free < DISK_CRITICAL_FREE
            ? `${system.disk.path} 还剩 ${formatBytes(system.disk.free)}。写满会让存档保存失败并损坏世界文件。`
            : `${system.disk.path} 还剩 ${formatBytes(system.disk.free)}，先看看旧备份能不能清。`,
        action: { label: '去清理', route: { kind: 'host', section: 'disk' } },
      })
    }
  }

  const memory = hostMemory(instances, system)
  if (memory.overcommitted) {
    alerts.push({
      id: 'memory',
      level: 'warn',
      title: '运行中的实例已超分配内存',
      detail: `已分配 ${formatBytes(memory.committedLive)}，本机共 ${formatBytes(memory.total)}。内核会在耗尽时随机杀掉其中一台服。`,
      action: { label: '看内存分布', route: { kind: 'host', section: 'metrics' } },
    })
  }

  if (update?.updateAvailable && update.latestVersion) {
    alerts.push({
      id: 'update',
      level: 'info',
      title: update.downgrade ? '可以装回正式版' : `面板可更新到 ${update.latestVersion}`,
      detail: `当前 ${update.currentVersion}`,
      action: { label: '去更新', route: { kind: 'settings', section: 'update' } },
    })
  }

  if (pluginUpdates > 0) {
    alerts.push({
      id: 'plugins',
      level: 'info',
      title: `${pluginUpdates} 个插件有新版本`,
      detail: '插件库里可以一次性下载新版本，再挑实例更新。',
      action: { label: '去插件库', route: { kind: 'library', section: 'plugins' } },
    })
  }

  const rank: Record<AlertLevel, number> = { error: 0, warn: 1, info: 2 }
  return alerts.sort((a, b) => rank[a.level] - rank[b.level])
}
