import { formatBytes, formatPercent } from './format'
import type { InstanceStatus, MetricSample } from './types'
import { isLive } from './types'

/**
 * What the monitor page says about a server, beyond the two curves.
 *
 * A curve is a record of what happened; it does not say whether what happened
 * was fine. Reading "3.4 GB" off a memory chart still needs the -Xmx it is
 * being measured against, the share that works out to, and a judgement about
 * whether that share is a problem — which is three steps the page can take for
 * the reader and then show its working.
 *
 * Everything here is derived from what the page already has: the instance and
 * the samples it is already drawing. No extra request, and nothing invented —
 * plugin load failures, which would belong in this rail, live on 插件 because
 * that is the page that reads them out of the console, and the daemon has no
 * per-instance event log to draw a real timeline from.
 */

export type EventLevel = 'error' | 'warn' | 'ok' | 'info'

export interface InstanceEvent {
  id: string
  level: EventLevel
  title: string
  detail?: string
}

export interface Verdict {
  label: string
  level: EventLevel
}

/**
 * A Minecraft server's main thread is effectively single-threaded, so 100 here
 * is one core saturated. Brief visits are normal — a chunk load, a world save
 * — so these are read against the mean over the window, not the latest sample:
 * one spike is not a verdict.
 */
const CPU_WARN = 70
const CPU_HIGH = 90

/**
 * Share of -Xmx. The JVM is supposed to run close to its ceiling — a heap that
 * never fills is a heap that was over-allocated — so 偏高 starts late, and 高
 * is where GC pressure stops being theoretical.
 */
const MEM_WARN = 0.85
const MEM_HIGH = 0.95

export function cpuVerdict(mean: number): Verdict {
  if (mean >= CPU_HIGH) return { label: '偏高', level: 'warn' }
  if (mean >= CPU_WARN) return { label: '偏忙', level: 'info' }
  return { label: '正常', level: 'ok' }
}

export function memoryVerdict(used: number, ceiling: number): Verdict | null {
  // No ceiling means nobody knows what this should be measured against, and a
  // verdict against a number the JVM never saw is worse than none. See the
  // note on effectiveMaxMemoryMB in types.ts.
  if (ceiling <= 0) return null
  const share = used / ceiling
  if (share >= MEM_HIGH) return { label: '接近上限', level: 'warn' }
  if (share >= MEM_WARN) return { label: '偏高', level: 'info' }
  return { label: '正常', level: 'ok' }
}

function mean(values: number[]): number {
  if (values.length === 0) return 0
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

/**
 * The rail beside the charts.
 *
 * Conditions that are true now come before things that happened, because the
 * first question is "is it all right at the moment" — a crash an hour ago that
 * has since been restarted from is history, and a heap sitting at 97% is not.
 */
export function instanceEvents(
  instance: InstanceStatus,
  samples: MetricSample[],
  /** The window the charts are showing, for the wording. */
  rangeLabel: string,
): InstanceEvent[] {
  const events: InstanceEvent[] = []
  const live = isLive(instance.state)
  const ceiling =
    instance.effectiveMaxMemoryMB > 0 ? instance.effectiveMaxMemoryMB * 1024 * 1024 : 0

  if (instance.state === 'crashed') {
    events.push({
      id: 'crashed',
      level: 'error',
      title: '服务器异常退出',
      detail:
        instance.exitCode !== undefined
          ? `退出码 ${instance.exitCode}。原因通常在控制台最后几十行里。`
          : '原因通常在控制台最后几十行里。',
    })
  }

  if (samples.length > 0) {
    const cpuMean = mean(samples.map((s) => s.cpuPercent))
    const cpu = cpuVerdict(cpuMean)
    if (cpu.level !== 'ok') {
      events.push({
        id: 'cpu',
        level: cpu.level,
        title: `CPU ${rangeLabel}平均 ${formatPercent(cpuMean)}`,
        detail:
          cpuMean >= CPU_HIGH
            ? '主线程基本跑满了。先看视距和模拟距离，加核心对单线程没有帮助。'
            : '还有余量，但已经不是空闲状态。',
      })
    }

    if (ceiling > 0) {
      const peak = Math.max(...samples.map((s) => s.memoryBytes))
      const memory = memoryVerdict(peak, ceiling)
      if (memory && memory.level !== 'ok') {
        events.push({
          id: 'memory',
          level: memory.level,
          title: `内存峰值 ${formatBytes(peak)}，占上限 ${formatPercent((peak / ceiling) * 100)}`,
          detail:
            '统计的是进程树的物理内存，含堆外开销，所以比 -Xmx 略高是正常的；持续贴顶才是问题。',
        })
      }
    }
  } else if (live) {
    events.push({
      id: 'nosamples',
      level: 'info',
      title: '还没有采样',
      detail: '服务器刚起来，第一批数据点要几秒钟。',
    })
  }

  if (!live && instance.state !== 'crashed') {
    events.push({
      id: 'stopped',
      level: 'info',
      title: '服务器没有在运行',
      detail: instance.message || '曲线画的是它上一次运行时留下的数据。',
    })
  }

  if (events.length === 0) {
    events.push({
      id: 'quiet',
      level: 'ok',
      title: `${rangeLabel}没有异常`,
      detail: 'CPU 和内存都在正常区间，没有异常退出。',
    })
  }

  return events
}
