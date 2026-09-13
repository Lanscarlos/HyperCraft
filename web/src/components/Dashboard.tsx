import { useState } from 'react'

import type { PanelAlert } from '../alerts'
import { DISK_CRITICAL_FREE, diskFreeRatio, diskUsedPercent, hostMemory } from '../alerts'
import { formatBytes, formatPercent } from '../format'
import type { Route } from '../routes'
import type { InstanceStatus, SystemInfo } from '../types'
import { CAP, useCan } from '../useCan'
import { STATE_LABELS, byUrgency, isLive } from '../types'
import { useLiveMetrics } from '../useLiveMetrics'
import type { LiveMetric } from '../useLiveMetrics'
import { useUptime } from '../useUptime'
import { Button } from './Button'
import { Card } from './Card'
import { EmptyState } from './EmptyState'
import { Page } from './Page'
import { PowerControls } from './PowerControls'
import { Section } from './Section'
import { StatusDot } from './StatusDot'

interface Props {
  instances: InstanceStatus[]
  system: SystemInfo | null
  alerts: PanelAlert[]
  onSelect: (id: string) => void
  onCreate: () => void
  onNavigate: (route: Route) => void
  onChanged: (instance: InstanceStatus) => void
}

/**
 * The panel's home page.
 *
 * It has exactly two jobs: answer "has anything gone wrong" and get you into
 * the right server. Everything here serves one of those — four numbers that
 * would change what you do next, the things asking for a decision, and the
 * servers themselves in the order urgency puts them in. Machine diagnostics
 * (network, per-core CPU, IO wait) are a floor down on the host page; a number
 * that does not change your next action does not belong on a home page.
 */
export function Dashboard({
  instances,
  system,
  alerts,
  onSelect,
  onCreate,
  onNavigate,
  onChanged,
}: Props) {
  const can = useCan()
  const running = instances.filter((item) => isLive(item.state))
  const ordered = byUrgency(instances)
  const metrics = useLiveMetrics(
    running.map((item) => item.id),
    running.length > 0,
  )

  const memory = hostMemory(instances, system)
  const worst = alerts[0]?.level

  return (
    <Page
      wide
      title="概览"
      lead="这台机器上的每台服务器：在不在跑、吃了多少内存、磁盘还剩多少，以及要处理的告警。"
      actions={
        /* Solid only while there is a list to stand beside. With no
           instances the empty state below carries the same onCreate, and
           two filled buttons for one action is the shape this rule
           exists to stop — the one that is where the eye already is
           wins. */
        can(CAP.panelCreate) && (
          <Button
            variant={instances.length === 0 ? 'default' : 'primary'}
            onClick={onCreate}
          >
            + 新建实例
          </Button>
        )
      }
    >
      <div className="summary">
        <Summary
          label="运行中"
          value={`${running.length} / ${instances.length}`}
          detail={
            instances.length === 0
              ? '还没有实例'
              : instances.some((item) => item.state === 'crashed')
                ? `${instances.filter((item) => item.state === 'crashed').length} 个异常退出`
                : '全部按预期运行'
          }
          onClick={() => onNavigate({ kind: 'instances', query: '', state: 'all' })}
        />
        <Summary
          label="本机内存"
          value={memory.total > 0 ? formatPercent((memory.used / memory.total) * 100) : '—'}
          detail={
            memory.total > 0
              ? `${formatBytes(memory.used)} / ${formatBytes(memory.total)} · 已分配 ${formatBytes(memory.committedLive)}`
              : '正在读取…'
          }
          tone={memory.overcommitted ? 'warn' : undefined}
          onClick={() => onNavigate({ kind: 'host', section: 'metrics' })}
        />
        <Summary
          label="磁盘剩余"
          value={system ? formatBytes(system.disk.free) : '—'}
          detail={
            system
              ? `${system.disk.path} · 已用 ${formatPercent(diskUsedPercent(system))}`
              : '正在读取…'
          }
          tone={system && diskFreeRatio(system) < DISK_CRITICAL_FREE ? 'error' : undefined}
          onClick={() => onNavigate({ kind: 'host', section: 'disk' })}
        />
        <Summary
          label="待处理告警"
          value={String(alerts.length)}
          detail={
            alerts.length === 0
              ? '没有需要处理的事情'
              : (alerts[0]?.title ?? '')
          }
          tone={worst === 'error' ? 'error' : worst === 'warn' ? 'warn' : undefined}
        />
      </div>

      {/* Nothing wrong, nothing rendered. A permanently-present "一切正常" box
          trains the eye to skip the place warnings appear. */}
      {alerts.length > 0 && (
        <section className="alerts" aria-label="待处理告警">
          {alerts.map((alert) => (
            <div key={alert.id} className={`alert alert--${alert.level}`}>
              <div className="alert__body">
                <strong>{alert.title}</strong>
                {alert.detail && <span>{alert.detail}</span>}
              </div>
              <button className="link" onClick={() => onNavigate(alert.action.route)}>
                {alert.action.label}
              </button>
            </div>
          ))}
        </section>
      )}

      {/* The create button used to sit in this head too. It is in the page
          head now, where every other page keeps its one primary action, so
          the section's only tool is the way to the long list. */}
      <Section
        title="实例"
        tools={
          instances.length > 6 && (
            <button
              className="link"
              onClick={() => onNavigate({ kind: 'instances', query: '', state: 'all' })}
            >
              所有实例
            </button>
          )
        }
      >
        {instances.length === 0 ? (
          can(CAP.panelCreate) ? (
            <EmptyState
              title="还没有任何实例。"
              action={
                <Button variant="primary" onClick={onCreate}>
                  新建第一个服务器
                </Button>
              }
            />
          ) : (
            // Not "create one" for somebody who cannot: the empty state has
            // to say what is actually true for them, which is that nobody
            // has given them a server yet.
            <EmptyState title="还没有分配给你的实例。">
              找管理员在「面板设置 → 账号与角色」里授权。
            </EmptyState>
          )
        ) : (
          <div className="cards">
            {ordered.map((item) => (
              <InstanceCard
                key={item.id}
                instance={item}
                metric={metrics[item.id]}
                onOpen={() => onSelect(item.id)}
                onChanged={onChanged}
              />
            ))}
          </div>
        )}
      </Section>
    </Page>
  )
}

/** One number on the summary bar. Four of them, and only numbers that would
 *  change what the reader does next. */
function Summary({
  label,
  value,
  detail,
  tone,
  onClick,
}: {
  label: string
  value: string
  detail: string
  tone?: 'warn' | 'error'
  onClick?: () => void
}) {
  const body = (
    <>
      <span className="summary__label">{label}</span>
      <strong className="summary__value">{value}</strong>
      <span className="summary__detail">{detail}</span>
    </>
  )
  const className = `summary__cell${tone ? ` summary__cell--${tone}` : ''}`
  if (!onClick) return <div className={className}>{body}</div>
  return (
    <button className={`${className} summary__cell--link`} onClick={onClick}>
      {body}
    </button>
  )
}

/**
 * One server, with the power control on the card.
 *
 * A running card carries live numbers; a stopped one carries the three facts
 * that decide whether it will come back up — which core, which Java, and why
 * it went down. Those are different questions, so they are different cards.
 */
function InstanceCard({
  instance,
  metric,
  onOpen,
  onChanged,
}: {
  instance: InstanceStatus
  metric: LiveMetric | undefined
  onOpen: () => void
  onChanged: (instance: InstanceStatus) => void
}) {
  const [error, setError] = useState<string | null>(null)
  const live = isLive(instance.state)
  const uptime = useUptime(instance.startedAt, live)
  const xmx = metric?.xmxBytes || instance.effectiveMaxMemoryMB * 1024 * 1024
  const share = metric && xmx > 0 ? Math.min(100, (metric.memoryBytes / xmx) * 100) : null

  return (
    <Card className={`card--${instance.state}`}>
      <button className="card__open" onClick={onOpen}>
        <div className="card__head">
          <StatusDot state={instance.state} />
          <strong>{instance.name}</strong>
          <span className="card__state">{STATE_LABELS[instance.state]}</span>
        </div>

        {live ? (
          <div className="card__live">
            <div className="card__bar" aria-hidden="true">
              <div
                className="card__bar-fill"
                style={{ width: `${share ?? 0}%` }}
                data-hot={share != null && share >= 90 ? 'on' : undefined}
              />
            </div>
            <div className="card__figures">
              <span>
                内存{' '}
                {metric
                  ? `${formatBytes(metric.memoryBytes)} / ${xmx > 0 ? formatBytes(xmx) : '未限制'}`
                  : '等采样…'}
              </span>
              <span>CPU {metric ? formatPercent(metric.cpuPercent) : '—'}</span>
              {uptime && <span>已运行 {uptime}</span>}
            </div>
          </div>
        ) : (
          <div className="card__figures card__figures--down">
            <span title={instance.jar}>{basename(instance.jar) || '未设置核心'}</span>
            <span title={instance.java}>{javaLabel(instance.java)}</span>
            <span className={instance.state === 'crashed' ? 'card__why card__why--bad' : 'card__why'}>
              {stopReason(instance)}
            </span>
          </div>
        )}
      </button>

      {error && <div className="card__error">{error}</div>}

      <div className="card__actions">
        <PowerControls
          instance={instance}
          onChanged={onChanged}
          variant="compact"
          onError={setError}
        />
        <button className="link" onClick={onOpen}>
          控制台
        </button>
      </div>
    </Card>
  )
}

function basename(path: string): string {
  if (!path) return ''
  const parts = path.split(/[\\/]/)
  return parts[parts.length - 1]
}

function javaLabel(java: string): string {
  if (!java || java === 'java') return '系统 java'
  const major = java.match(/(?:jdk|jre|java)[-_]?(\d{1,2})/i)
  return major ? `Java ${major[1]}` : basename(java)
}

function stopReason(instance: InstanceStatus): string {
  if (instance.state === 'crashed') {
    return instance.exitCode != null ? `异常退出，退出码 ${instance.exitCode}` : '异常退出'
  }
  return instance.message ? instance.message : '已手动停止'
}
