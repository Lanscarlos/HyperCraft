import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import type { InstanceStatus, StateInfo } from '../types'
import { STATE_LABELS, isLive, mergeState } from '../types'
import type { CoreController } from '../useCores'
import { Console } from './Console'
import { ConsoleStatus } from './ConsoleStatus'
import { FileManager } from './FileManager'
import { InstancePlugins } from './InstancePlugins'
import { LaunchSettings } from './LaunchSettings'
import { Menu } from './Menu'
import { PropertiesEditor } from './PropertiesEditor'
import { ResourcePanel } from './ResourcePanel'
import { Tabs } from './Tabs'

type Tab = 'console' | 'files' | 'plugins' | 'resources' | 'launch' | 'properties'

/** Whether the console keeps its status strip. Per-device, like the sidebar. */
const SIDE_KEY = 'hypercraft.console-side'

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
  const [side, setSide] = useState(() => window.localStorage.getItem(SIDE_KEY) !== 'off')

  useEffect(() => {
    window.localStorage.setItem(SIDE_KEY, side ? 'on' : 'off')
  }, [side])

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

        {/* Only the two you reach for daily are buttons. 重启 and 强制结束 sit
            behind the ⋯: a kill button the same size as 启动, one row from the
            pointer that just started the server, is how a world gets lost to a
            misclick. */}
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
          <Menu
            className="btn btn--icon"
            title="更多操作"
            ariaLabel="更多操作"
            items={[
              { label: '重启', onSelect: () => void power('restart'), disabled: busy },
              {
                label: '强制结束',
                onSelect: () => void power('kill'),
                disabled: busy || !live,
                danger: true,
              },
            ]}
          >
            ⋯
          </Menu>
        </div>
      </header>

      {error && <div className="alert alert--error">{error}</div>}
      {instance.message && !error && (
        <div className="instance__message">{instance.message}</div>
      )}

      <Tabs
        items={TABS}
        active={tab}
        onSelect={setTab}
        label={`${instance.name} 的页面`}
        idPrefix="instance"
      />

      <div className="instance__body">
        {/* The console stays mounted so its websocket and scrollback survive a
            trip to the settings tabs. On a wide screen it shares the pane with
            a strip of live numbers; below that width the strip is not rendered
            at all and 资源 is where the same figures live. */}
        <div
          hidden={tab !== 'console'}
          className="instance__pane instance__pane--split"
          data-side={side ? 'on' : 'off'}
          id="instance-panel-console"
          role="tabpanel"
          aria-labelledby="instance-tab-console"
        >
          <Console
            instanceId={instance.id}
            state={instance.state}
            onState={applyState}
          />
          {side ? (
            <ConsoleStatus
              instance={instance}
              active={tab === 'console'}
              onOpenResources={() => setTab('resources')}
              onCollapse={() => setSide(false)}
            />
          ) : (
            <button
              className="console-side__peek"
              onClick={() => setSide(true)}
              title="展开运行状态"
              aria-label="展开运行状态"
            >
              ‹
            </button>
          )}
        </div>
        {tab === 'files' && (
          <div
            className="instance__pane instance__pane--scroll"
            id="instance-panel-files"
            role="tabpanel"
            aria-labelledby="instance-tab-files"
          >
            <FileManager instance={instance} />
          </div>
        )}
        {tab === 'plugins' && (
          <div
            className="instance__pane instance__pane--scroll"
            id="instance-panel-plugins"
            role="tabpanel"
            aria-labelledby="instance-tab-plugins"
          >
            <InstancePlugins instance={instance} onOpenLibrary={onOpenPlugins} />
          </div>
        )}
        {tab === 'resources' && (
          <div
            className="instance__pane instance__pane--scroll"
            id="instance-panel-resources"
            role="tabpanel"
            aria-labelledby="instance-tab-resources"
          >
            <ResourcePanel instance={instance} />
          </div>
        )}
        {tab === 'launch' && (
          <div
            className="instance__pane instance__pane--scroll"
            id="instance-panel-launch"
            role="tabpanel"
            aria-labelledby="instance-tab-launch"
          >
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
          <div
            className="instance__pane instance__pane--scroll"
            id="instance-panel-properties"
            role="tabpanel"
            aria-labelledby="instance-tab-properties"
          >
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
