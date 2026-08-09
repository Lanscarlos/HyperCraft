import { useMemo, useState } from 'react'

import { DISK_CRITICAL_FREE, DISK_WARN_FREE, diskUsedPercent, hostMemory } from '../alerts'
import { formatBytes, formatPercent } from '../format'
import type { HostSection, Route } from '../routes'
import type { InstanceStatus, SystemInfo } from '../types'
import { STATE_LABELS, byUrgency, isLive } from '../types'
import type { SystemController } from '../useSystem'
import type { TerminalController } from '../useTerminal'
import { Meter } from './Meter'
import { Page } from './Page'
import { TerminalSettings } from './TerminalSettings'
import { TimeSeriesChart, type Point } from './TimeSeriesChart'

const CPU_COLOR = 'var(--series-cpu)'
const WINDOW_MS = 30 * 60_000

interface Props {
  section: HostSection
  system: SystemController
  instances: InstanceStatus[]
  terminal: TerminalController
  onNavigate: (route: Route) => void
}

/**
 * The machine everything runs on.
 *
 * HyperCraft manages one host — the one it is installed on — so there is no
 * node list to walk through: this *is* the node, and the sidebar links
 * straight into it. The diagnostics that live here (per-core CPU shape,
 * allocation against physical memory, disk headroom) are deliberately not on
 * the home page: they tell you *why*, and the home page's job is *whether*.
 */
export function HostPage({ section, system, instances, terminal, onNavigate }: Props) {
  if (section === 'instances') {
    return <HostInstances system={system.info} instances={instances} onNavigate={onNavigate} />
  }
  if (section === 'disk') {
    return <HostDisk system={system.info} instances={instances} onNavigate={onNavigate} />
  }
  if (section === 'config') {
    return <HostConfig system={system.info} terminal={terminal} onNavigate={onNavigate} />
  }
  return <HostMetrics system={system} instances={instances} />
}

// ----------------------------------------------------------------- monitor

function HostMetrics({
  system,
  instances,
}: {
  system: SystemController
  instances: InstanceStatus[]
}) {
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)
  const info = system.info

  const cpuPoints = useMemo<Point[]>(() => {
    if (!info) return []
    const samples = info.samples.map((s) => ({ t: new Date(s.time).getTime(), v: s.cpuPercent }))
    const newest = samples.length > 0 ? samples[samples.length - 1].t : Date.now()
    return samples.filter((s) => s.t >= newest - WINDOW_MS)
  }, [info])

  if (!info) {
    return (
      <Page title="监控" lead={system.error ?? '正在读取本机状态…'}>
        {system.error && <div className="alert alert--error">{system.error}</div>}
      </Page>
    )
  }

  const memory = hostMemory(instances, info)
  const latest = info.samples[info.samples.length - 1]

  return (
    <Page
      wide
      title="监控"
      lead="这台机器整体的负载。要看单台服务器的曲线，去它自己的「监控」页。"
    >
      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">内存</h2>
          <p className="chart-head__meta">物理 {formatBytes(memory.total)}</p>
        </div>

        {/* Three numbers, not one percentage. "已分配" is the one that decides
            whether another server can be started at all, and a single bar
            hides it completely — right up until the kernel picks a running
            server to kill. */}
        <AllocationBar
          total={memory.total}
          used={memory.used}
          committed={memory.committedLive}
        />

        <dl className="figures">
          <div>
            <dt>物理总量</dt>
            <dd>{formatBytes(memory.total)}</dd>
          </div>
          <div>
            <dt>实际占用</dt>
            <dd>
              {formatBytes(memory.used)}
              <small>
                {memory.total > 0 ? ` · ${formatPercent((memory.used / memory.total) * 100)}` : ''}
              </small>
            </dd>
          </div>
          <div>
            <dt>已分配（运行中 -Xmx 之和）</dt>
            <dd>
              {formatBytes(memory.committedLive)}
              <small>
                {memory.total > 0
                  ? ` · ${formatPercent((memory.committedLive / memory.total) * 100)}`
                  : ''}
              </small>
            </dd>
          </div>
          <div>
            <dt>全部实例配置之和</dt>
            <dd>{formatBytes(memory.committedAll)}</dd>
          </div>
        </dl>

        {memory.overcommitted ? (
          <div className="alert alert--warn">
            运行中的实例被允许申请 {formatBytes(memory.committedLive)}，超过本机的{' '}
            {formatBytes(memory.total)}。真正用满时内核会挑一个进程杀掉，而它挑中的
            通常就是最大的那台服 —— 表现为服务器「无缘无故」消失。
          </div>
        ) : memory.committedAll > memory.total && memory.total > 0 ? (
          <p className="chart-note">
            现在没问题，但所有实例同时启动会需要 {formatBytes(memory.committedAll)}，
            超过本机的 {formatBytes(memory.total)}。别让它们同时开着。
          </p>
        ) : (
          <p className="chart-note">
            已分配是各实例 -Xmx 之和，也就是它们「有权」占用的上限。能不能再开一台服，
            看的是这个数，不是当前占用。
          </p>
        )}
      </section>

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">CPU 占用（全机）</h2>
          <p className="chart-head__meta">
            最近 30 分钟 · {info.host.cpuCores} 核 ·{' '}
            {latest ? formatPercent(latest.cpuPercent) : '—'}
          </p>
        </div>
        <TimeSeriesChart
          points={cpuPoints}
          color={CPU_COLOR}
          format={(v) => formatPercent(v)}
          windowMs={WINDOW_MS}
          minYMax={100}
          hoverIndex={hoverIndex}
          onHover={setHoverIndex}
          ariaLabel="本机 CPU 占用曲线，最近 30 分钟"
        />
      </section>

      <section className="panel">
        <h2 className="panel__title">面板自身</h2>
        <p className="chart-note">
          {formatBytes(info.panel.heapBytes)} 堆内存 · {info.panel.goroutines} 个协程 ·{' '}
          {info.goVersion}。守护进程本身在一台跑着 JVM 的机器上是舍入误差，
          这里放着只是为了让它可被证伪。
        </p>
      </section>
    </Page>
  )
}

/**
 * Two bars in one track: what is promised under what is used.
 *
 * The pale layer is allocation and the solid one is real usage, so
 * over-allocation is visible as a pale bar running past the end of the track
 * rather than as a number nobody looked up.
 */
function AllocationBar({
  total,
  used,
  committed,
}: {
  total: number
  used: number
  committed: number
}) {
  if (total <= 0) return null
  // Both bars are drawn against whichever is larger, so an over-allocated
  // machine shows the overflow instead of silently clipping at 100%.
  const scale = Math.max(total, committed)
  const pct = (value: number) => `${Math.min(100, (value / scale) * 100)}%`

  return (
    <div className="alloc">
      <div className="alloc__track">
        <div className="alloc__committed" style={{ width: pct(committed) }} />
        <div className="alloc__used" style={{ width: pct(used) }} />
        {committed > total && (
          <div className="alloc__limit" style={{ left: pct(total) }} title="本机物理内存" />
        )}
      </div>
      <div className="alloc__legend">
        <span className="alloc__key alloc__key--used">实际占用 {formatBytes(used)}</span>
        <span className="alloc__key alloc__key--committed">已分配 {formatBytes(committed)}</span>
        <span className="alloc__key">物理 {formatBytes(total)}</span>
      </div>
    </div>
  )
}

// ----------------------------------------------------------- distribution

function HostInstances({
  system,
  instances,
  onNavigate,
}: {
  system: SystemInfo | null
  instances: InstanceStatus[]
  onNavigate: (route: Route) => void
}) {
  const memory = hostMemory(instances, system)
  const ordered = byUrgency(instances)

  return (
    <Page
      wide
      title="实例分布"
      lead="这台机器上的服务器各自占了多少额度。「已分配」是 -Xmx，也就是它有权拿走的上限。"
    >
      <div className="rows" role="table" aria-label="实例内存分布">
        <div className="rows__head" role="row">
          <span role="columnheader">实例</span>
          <span role="columnheader">状态</span>
          <span role="columnheader">已分配 -Xmx</span>
          <span role="columnheader">占本机</span>
          <span role="columnheader"></span>
        </div>
        {ordered.map((item) => {
          const xmx = Math.max(0, item.maxMemoryMB) * 1024 * 1024
          const share = memory.total > 0 ? (xmx / memory.total) * 100 : 0
          return (
            <div className="rows__row" role="row" key={item.id}>
              <button
                className="rows__name"
                role="cell"
                onClick={() => onNavigate({ kind: 'instance', id: item.id, section: 'console' })}
              >
                <strong>{item.name}</strong>
                <small className="rows__path">{item.directory}</small>
              </button>
              <span className="rows__cell" role="cell">
                <span className={`status__dot status__dot--${item.state}`} />
                {STATE_LABELS[item.state]}
              </span>
              <span className="rows__cell rows__cell--num" role="cell">
                {xmx > 0 ? formatBytes(xmx) : '未限制'}
              </span>
              <span className="rows__cell" role="cell">
                <span className="share" aria-hidden="true">
                  <span
                    className={`share__fill${isLive(item.state) ? ' share__fill--live' : ''}`}
                    style={{ width: `${Math.min(100, share)}%` }}
                  />
                </span>
                <small>{formatPercent(share)}</small>
              </span>
              <span className="rows__cell rows__cell--end" role="cell">
                <button
                  className="link"
                  onClick={() =>
                    onNavigate({ kind: 'instance', id: item.id, section: 'settings' })
                  }
                >
                  改内存
                </button>
              </span>
            </div>
          )
        })}
        {instances.length === 0 && <p className="rows__empty">这台机器上还没有实例。</p>}
      </div>

      <p className="chart-note">
        运行中合计已分配 {formatBytes(memory.committedLive)}，全部实例合计{' '}
        {formatBytes(memory.committedAll)}，本机物理内存 {formatBytes(memory.total)}。
      </p>
    </Page>
  )
}

// -------------------------------------------------------------------- disk

function HostDisk({
  system,
  instances,
  onNavigate,
}: {
  system: SystemInfo | null
  instances: InstanceStatus[]
  onNavigate: (route: Route) => void
}) {
  if (!system) return <Page title="磁盘" lead="正在读取本机状态…" />

  const free = system.disk.total > 0 ? system.disk.free / system.disk.total : 1
  const level = free < DISK_CRITICAL_FREE ? 'error' : free < DISK_WARN_FREE ? 'warn' : 'ok'

  return (
    <Page
      wide
      title="磁盘"
      lead="磁盘写满会让世界保存失败，正在写入的区块可能直接损坏 —— 这是本面板能遇到的破坏性最大的故障，所以它按告警级别处理，而不只是一根进度条。"
    >
      {level !== 'ok' && (
        <div className={`alert alert--${level}`}>
          <div className="alert__body">
            <strong>
              {level === 'error' ? '立刻清理' : '该清理了'}：{system.disk.path} 只剩{' '}
              {formatBytes(system.disk.free)}（{formatPercent(free * 100)}）
            </strong>
            <span>
              先看旧的世界备份和实例目录里的日志 —— 十有八九是它们。清完再回来刷新。
            </span>
          </div>
        </div>
      )}

      <section className="panel">
        <Meter
          label={system.disk.path}
          percent={diskUsedPercent(system)}
          detail={`已用 ${formatBytes(system.disk.total - system.disk.free)} / ${formatBytes(system.disk.total)} · 剩余 ${formatBytes(system.disk.free)}`}
        />
        <p className="chart-note">
          面板只统计它自己所在的这块盘。实例目录挂在别的盘上时，那块盘的用量要自己看。
        </p>
      </section>

      <section className="panel">
        <h2 className="panel__title">从这里开始清</h2>
        <p className="chart-note">
          按经验，占地方的顺序通常是：世界备份 &gt; 旧日志 &gt; 下载过的核心 jar &gt; 插件历史版本。
        </p>
        <div className="rows" role="table" aria-label="清理入口">
          {instances.map((item) => (
            <div className="rows__row" role="row" key={item.id}>
              <button
                className="rows__name"
                role="cell"
                onClick={() => onNavigate({ kind: 'instance', id: item.id, section: 'files' })}
              >
                <strong>{item.name}</strong>
                <small className="rows__path">{item.directory}</small>
              </button>
              <span className="rows__cell rows__cell--end" role="cell">
                <button
                  className="link"
                  onClick={() => onNavigate({ kind: 'instance', id: item.id, section: 'files' })}
                >
                  打开文件管理器
                </button>
              </span>
            </div>
          ))}
          <div className="rows__row" role="row">
            <div className="rows__name" role="cell">
              <strong>服务端核心库</strong>
              <small>下载过但没人再用的 jar</small>
            </div>
            <span className="rows__cell rows__cell--end" role="cell">
              <button
                className="link"
                onClick={() => onNavigate({ kind: 'library', section: 'cores' })}
              >
                去清理
              </button>
            </span>
          </div>
          <div className="rows__row" role="row">
            <div className="rows__name" role="cell">
              <strong>插件库</strong>
              <small>每个插件保留的历史版本</small>
            </div>
            <span className="rows__cell rows__cell--end" role="cell">
              <button
                className="link"
                onClick={() => onNavigate({ kind: 'library', section: 'plugins' })}
              >
                去清理
              </button>
            </span>
          </div>
          <div className="rows__row" role="row">
            <div className="rows__name" role="cell">
              <strong>Java 环境</strong>
              <small>没有实例在用的运行时，一份大约 180 MB</small>
            </div>
            <span className="rows__cell rows__cell--end" role="cell">
              <button
                className="link"
                onClick={() => onNavigate({ kind: 'library', section: 'java' })}
              >
                去清理
              </button>
            </span>
          </div>
        </div>
      </section>
    </Page>
  )
}

// ------------------------------------------------------------------ config

function HostConfig({
  system,
  terminal,
  onNavigate,
}: {
  system: SystemInfo | null
  terminal: TerminalController
  onNavigate: (route: Route) => void
}) {
  return (
    <Page
      title="节点配置"
      lead="这台机器本身的设置。面板账号、更新通道之类跟机器无关的东西在「面板设置」里。"
    >
      <section className="panel">
        <h2 className="panel__title">本机</h2>
        <dl className="figures">
          <div>
            <dt>主机名</dt>
            <dd>{system?.host.hostname || '—'}</dd>
          </div>
          <div>
            <dt>平台</dt>
            <dd>{system?.host.platform || '—'}</dd>
          </div>
          <div>
            <dt>CPU</dt>
            <dd>{system ? `${system.host.cpuCores} 核` : '—'}</dd>
          </div>
          <div>
            <dt>内存</dt>
            <dd>{system ? formatBytes(system.host.memoryTotal) : '—'}</dd>
          </div>
          <div>
            <dt>面板版本</dt>
            <dd>{system?.version ?? '—'}</dd>
          </div>
        </dl>
      </section>

      <TerminalSettings
        terminal={terminal}
        onOpenTerminal={() => onNavigate({ kind: 'host', section: 'terminal' })}
      />
    </Page>
  )
}
