import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import type { InstanceStatus, StateInfo } from '../types'
import { STATE_LABELS, isLive, mergeState } from '../types'
import type { CoreController } from '../useCores'
import { Console } from './Console'
import { FileManager } from './FileManager'
import { InstancePlugins } from './InstancePlugins'
import { LaunchSettings } from './LaunchSettings'
import { PropertiesEditor } from './PropertiesEditor'
import { ResourcePanel } from './ResourcePanel'

type Tab = 'console' | 'files' | 'plugins' | 'resources' | 'launch' | 'properties'

const TABS: { id: Tab; label: string }[] = [
  { id: 'console', label: '控制台' },
  { id: 'files', label: '文件' },
  { id: 'plugins', label: '插件' },
  { id: 'resources', label: '资源' },
  { id: 'launch', label: '启动设置' },
  { id: 'properties', label: '服务器配置' },
]

interface Props {
  instance: InstanceStatus
  cores: CoreController
  onChanged: (instance: InstanceStatus) => void
  onDeleted: () => void
  onOpenLibrary: () => void
  onOpenPlugins: () => void
}

export function InstanceView({
  instance,
  cores,
  onChanged,
  onDeleted,
  onOpenLibrary,
  onOpenPlugins,
}: Props) {
  const [tab, setTab] = useState<Tab>('console')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setTab('console')
  }, [instance.id])

  // The console websocket is the fastest source of state changes, so state
  // pushed from it updates the header without waiting for the list poll.
  const applyState = useCallback(
    (state: StateInfo) => onChanged(mergeState(instance, state)),
    [instance, onChanged],
  )

  const power = async (action: 'start' | 'stop' | 'restart' | 'kill') => {
    if (action === 'kill') {
      const ok = window.confirm(
        '强制结束会直接杀掉进程，不会保存世界，可能丢失最近的存档数据。确定继续吗？',
      )
      if (!ok) return
    }
    setBusy(true)
    setError(null)
    try {
      onChanged(await api.power(instance.id, action))
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const live = isLive(instance.state)

  return (
    <div className="instance">
      <header className="instance__header">
        <div className="instance__identity">
          <h2>{instance.name}</h2>
          <StatusBadge instance={instance} />
        </div>

        <div className="instance__actions">
          <button
            className="btn btn--primary"
            onClick={() => power('start')}
            disabled={busy || live}
          >
            启动
          </button>
          <button
            className="btn"
            onClick={() => power('stop')}
            disabled={busy || !live}
          >
            停止
          </button>
          <button
            className="btn"
            onClick={() => power('restart')}
            disabled={busy}
          >
            重启
          </button>
          <button
            className="btn btn--danger"
            onClick={() => power('kill')}
            disabled={busy || !live}
          >
            强制结束
          </button>
        </div>
      </header>

      {error && <div className="alert alert--error">{error}</div>}
      {instance.message && !error && (
        <div className="instance__message">{instance.message}</div>
      )}

      <nav className="tabs">
        {TABS.map((entry) => (
          <button
            key={entry.id}
            className={`tabs__tab${tab === entry.id ? ' tabs__tab--active' : ''}`}
            onClick={() => setTab(entry.id)}
          >
            {entry.label}
          </button>
        ))}
      </nav>

      <div className="instance__body">
        {/* The console stays mounted so its websocket and scrollback survive a
            trip to the settings tabs. */}
        <div hidden={tab !== 'console'} className="instance__pane">
          <Console
            instanceId={instance.id}
            state={instance.state}
            onState={applyState}
          />
        </div>
        {tab === 'files' && (
          <div className="instance__pane instance__pane--scroll">
            <FileManager instance={instance} />
          </div>
        )}
        {tab === 'plugins' && (
          <div className="instance__pane instance__pane--scroll">
            <InstancePlugins instance={instance} onOpenLibrary={onOpenPlugins} />
          </div>
        )}
        {tab === 'resources' && (
          <div className="instance__pane instance__pane--scroll">
            <ResourcePanel instance={instance} />
          </div>
        )}
        {tab === 'launch' && (
          <div className="instance__pane instance__pane--scroll">
            <LaunchSettings
              instance={instance}
              cores={cores}
              onSaved={onChanged}
              onDeleted={onDeleted}
              onOpenLibrary={onOpenLibrary}
            />
          </div>
        )}
        {tab === 'properties' && (
          <div className="instance__pane instance__pane--scroll">
            <PropertiesEditor instance={instance} />
          </div>
        )}
      </div>
    </div>
  )
}

function StatusBadge({ instance }: { instance: InstanceStatus }) {
  const uptime = useUptime(instance.startedAt, isLive(instance.state))

  return (
    <div className="status">
      <span className={`status__dot status__dot--${instance.state}`} />
      <span className="status__label">{STATE_LABELS[instance.state]}</span>
      {instance.pid ? <span className="status__meta">PID {instance.pid}</span> : null}
      {uptime && <span className="status__meta">已运行 {uptime}</span>}
      {instance.state === 'crashed' && instance.exitCode != null && (
        <span className="status__meta">退出码 {instance.exitCode}</span>
      )}
    </div>
  )
}

/** Ticks once a second while the server is up. */
function useUptime(startedAt: string | undefined, live: boolean): string | null {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!live || !startedAt) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [live, startedAt])

  if (!live || !startedAt) return null

  const seconds = Math.max(0, Math.floor((now - new Date(startedAt).getTime()) / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = seconds % 60

  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  if (minutes > 0) return `${minutes} 分 ${secs} 秒`
  return `${secs} 秒`
}
