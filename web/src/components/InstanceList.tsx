import { useState } from 'react'

import { formatBytes, formatPercent } from '../format'
import type { Route, StateFilter } from '../routes'
import type { InstanceStatus } from '../types'
import { STATE_LABELS, byUrgency, isLive } from '../types'
import { useLiveMetrics } from '../useLiveMetrics'
import { useUptime } from '../useUptime'
import { Page } from './Page'
import { PowerControls } from './PowerControls'

const FILTERS: { id: StateFilter; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'live', label: '运行中' },
  { id: 'stopped', label: '已停止' },
  { id: 'problem', label: '异常' },
]

interface Props {
  instances: InstanceStatus[]
  query: string
  state: StateFilter
  /** Filters are part of the URL, so changing one is a navigation: a filtered
   *  list can be reloaded, bookmarked and pasted into a chat. */
  onFilter: (next: { query: string; state: StateFilter }) => void
  onNavigate: (route: Route) => void
  onCreate: () => void
  /** Adopts a server directory that already exists on the machine. */
  onImport: () => void
  onChanged: (instance: InstanceStatus) => void
}

/**
 * Every server there is.
 *
 * The sidebar keeps five; this is where the other twenty-five live. It follows
 * the panel's page skeleton — title and primary action, then the filter row,
 * then the content — and the sort is urgency, never the alphabet.
 */
export function InstanceList({
  instances,
  query,
  state,
  onFilter,
  onNavigate,
  onCreate,
  onImport,
  onChanged,
}: Props) {
  const needle = query.trim().toLowerCase()
  const shown = byUrgency(
    instances.filter((item) => {
      if (state === 'live' && !isLive(item.state)) return false
      if (state === 'stopped' && item.state !== 'stopped') return false
      if (state === 'problem' && item.state !== 'crashed') return false
      return needle === '' || item.name.toLowerCase().includes(needle)
    }),
  )
  const metrics = useLiveMetrics(
    shown.filter((item) => isLive(item.state)).map((item) => item.id),
    true,
  )

  return (
    <Page
      wide
      title="所有实例"
      aside={
        <div className="actions">
          {/* Adopting is the rarer of the two and the one with a world at
              stake, so it sits beside 新建 rather than inside it — but it is
              here, on the list, because that is where someone who has just
              realised the panel does not know about their server looks. */}
          <button className="btn" onClick={onImport}>
            导入现有目录
          </button>
          <button className="btn btn--primary" onClick={onCreate}>
            + 新建实例
          </button>
        </div>
      }
    >
      <div className="filters">
        <input
          className="filters__search"
          type="search"
          value={query}
          onChange={(event) => onFilter({ query: event.target.value, state })}
          placeholder="按名称搜索"
          aria-label="按名称搜索实例"
        />
        <div className="filters__chips" role="group" aria-label="按状态筛选">
          {FILTERS.map((filter) => (
            <button
              key={filter.id}
              className={`chip${state === filter.id ? ' chip--active' : ''}`}
              onClick={() => onFilter({ query, state: filter.id })}
              aria-pressed={state === filter.id}
            >
              {filter.label}
            </button>
          ))}
        </div>
        <span className="filters__count">
          {shown.length} / {instances.length}
        </span>
      </div>

      <div className="rows" role="table" aria-label="实例列表">
        <div className="rows__head" role="row">
          <span role="columnheader">实例</span>
          <span role="columnheader">状态</span>
          <span role="columnheader">内存</span>
          <span role="columnheader">核心 / Java</span>
          <span role="columnheader">操作</span>
        </div>
        {shown.length === 0 && (
          <p className="rows__empty">
            {instances.length === 0 ? (
              <>
                还没有实例，先新建一个吧 —— 机器上已经有服务端目录的话，
                <button className="link" onClick={onImport}>
                  直接导入它
                </button>
                。
              </>
            ) : (
              '没有符合条件的实例。'
            )}
          </p>
        )}
        {shown.map((item) => (
          <Row
            key={item.id}
            instance={item}
            memory={memoryText(item, metrics[item.id])}
            onOpen={() => onNavigate({ kind: 'instance', id: item.id, section: 'console' })}
            onChanged={onChanged}
          />
        ))}
      </div>
    </Page>
  )
}

function memoryText(
  instance: InstanceStatus,
  metric: { memoryBytes: number; xmxBytes: number; cpuPercent: number } | undefined,
): string {
  const xmx = instance.maxMemoryMB > 0 ? instance.maxMemoryMB * 1024 * 1024 : 0
  if (!isLive(instance.state) || !metric) {
    return xmx > 0 ? `已分配 ${formatBytes(xmx)}` : '未限制'
  }
  const ceiling = metric.xmxBytes || xmx
  return `${formatBytes(metric.memoryBytes)}${ceiling > 0 ? ` / ${formatBytes(ceiling)}` : ''} · CPU ${formatPercent(metric.cpuPercent)}`
}

function Row({
  instance,
  memory,
  onOpen,
  onChanged,
}: {
  instance: InstanceStatus
  memory: string
  onOpen: () => void
  onChanged: (instance: InstanceStatus) => void
}) {
  const [error, setError] = useState<string | null>(null)
  const uptime = useUptime(instance.startedAt, isLive(instance.state))

  return (
    <div className="rows__row" role="row">
      <button className="rows__name" onClick={onOpen} role="cell">
        <strong>{instance.name}</strong>
        <small className="rows__path">{instance.directory}</small>
      </button>
      <span className="rows__cell" role="cell">
        <span className={`status__dot status__dot--${instance.state}`} />
        {STATE_LABELS[instance.state]}
        {uptime && <small> · {uptime}</small>}
      </span>
      <span className="rows__cell rows__cell--num" role="cell">
        {memory}
      </span>
      <span className="rows__cell" role="cell">
        {instance.jar ? instance.jar.split(/[\\/]/).pop() : '未设置核心'}
        {/* A proxy and a server are told apart by their jar name at best, and
            not at all once it has been renamed. The list is where you pick
            which one to open, so it says which is which. */}
        {instance.kind === 'proxy' && <span className="badge">代理端</span>}
      </span>
      <span className="rows__cell rows__cell--end" role="cell">
        <PowerControls
          instance={instance}
          onChanged={onChanged}
          variant="compact"
          onError={setError}
        />
      </span>
      {error && <div className="rows__error">{error}</div>}
    </div>
  )
}
