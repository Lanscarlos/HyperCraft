import { useState } from 'react'

import { api } from '../api'
import type { InstanceStatus, User } from '../types'
import { STATE_LABELS, isLive } from '../types'
import type { CoreController } from '../useCores'
import type { JavaController } from '../useJava'
import { updateLabel } from '../useUpdate'
import type { UpdateController } from '../useUpdate'
import { HostOverview } from './HostOverview'

interface Props {
  user: User
  instances: InstanceStatus[]
  onSelect: (id: string) => void
  onCreate: () => void
  onOpenJava: () => void
  onOpenCores: () => void
  onOpenUpdate: () => void
  onChanged: (instance: InstanceStatus) => void
  update: UpdateController
  java: JavaController
  cores: CoreController
}

/**
 * The panel's home page: everything running on this machine, at a glance.
 *
 * It answers the three questions an operator opens the panel with — is
 * everything up, is the machine coping, and is anything waiting on me — and
 * links out to whichever page owns the answer. The shared assets it counts
 * (Java runtimes, server cores, the panel binary) are managed on their own
 * pages; this only reports on them.
 */
export function Dashboard({
  user,
  instances,
  onSelect,
  onCreate,
  onOpenJava,
  onOpenCores,
  onOpenUpdate,
  onChanged,
  update,
  java,
  cores,
}: Props) {
  const notice = updateLabel(update.status)
  const running = instances.filter((item) => isLive(item.state))
  const crashed = instances.filter((item) => item.state === 'crashed')

  return (
    <div className="page page--wide">
      <h1>仪表盘</h1>
      <p className="page__lead">
        面板以后台守护进程的方式持有服务器进程。关掉浏览器、退出登录，甚至重启路由，
        服务器都会照常运行 —— 只有停止面板本身才会（优雅地）关掉它们。
      </p>

      {notice && (
        <div className="alert alert--ok">
          {notice}：{update.status?.latestVersion}（当前 {update.status?.currentVersion}）。
          <button className="link" onClick={onOpenUpdate}>
            {update.status?.downgrade ? '去装回正式版' : '去更新'}
          </button>
        </div>
      )}
      {crashed.length > 0 && (
        <div className="alert alert--error">
          {crashed.length} 个实例已崩溃：
          {crashed.map((item) => (
            <button key={item.id} className="link" onClick={() => onSelect(item.id)}>
              {item.name}
            </button>
          ))}
        </div>
      )}

      <div className="stats">
        <Stat
          label="实例"
          value={`${running.length} / ${instances.length}`}
          detail={instances.length === 0 ? '还没有实例' : '运行中 / 总数'}
        />
        <Stat
          label="Java 运行时"
          value={java.installing ? '安装中' : String(java.overview?.runtimes.length ?? 0)}
          detail={javaDetail(java)}
          onClick={onOpenJava}
        />
        <Stat
          label="服务端核心"
          value={cores.downloading ? '下载中' : String(cores.cores.length)}
          detail={
            cores.downloading
              ? `正在下载 ${cores.job?.fileName ?? ''}`
              : cores.cores.length > 0
                ? '可直接复制到新实例'
                : '还没下载过核心'
          }
          onClick={onOpenCores}
        />
        <Stat
          label="面板版本"
          value={user.version}
          detail={notice ? `${notice} ${update.status?.latestVersion}` : '已是最新'}
          onClick={onOpenUpdate}
        />
      </div>

      {/* Instances first: the machine's own graphs are context, but the list of
          servers is what the page is opened for. */}
      <section className="dashboard__instances">
        <div className="chart-head">
          <h2 className="panel__title">实例</h2>
          <button className="btn btn--primary" onClick={onCreate}>
            + 新建实例
          </button>
        </div>

        {instances.length === 0 ? (
          <div className="welcome__empty">
            <p>还没有任何实例。</p>
            <button className="btn btn--primary" onClick={onCreate}>
              新建第一个服务器
            </button>
          </div>
        ) : (
          <div className="cards">
            {instances.map((item) => (
              <InstanceCard
                key={item.id}
                instance={item}
                onOpen={() => onSelect(item.id)}
                onChanged={onChanged}
              />
            ))}
          </div>
        )}
      </section>

      <HostOverview />
    </div>
  )
}

/** One number worth seeing without clicking anything. */
function Stat({
  label,
  value,
  detail,
  onClick,
}: {
  label: string
  value: string
  detail: string
  onClick?: () => void
}) {
  const body = (
    <>
      <span className="stat__label">{label}</span>
      <strong className="stat__value">{value}</strong>
      <span className="stat__detail">{detail}</span>
    </>
  )
  if (!onClick) return <div className="stat">{body}</div>
  return (
    <button className="stat stat--link" onClick={onClick}>
      {body}
    </button>
  )
}

/**
 * An instance card with its power buttons on it, so the common case — start
 * the server, stop the server — does not need a trip through the console page.
 */
function InstanceCard({
  instance,
  onOpen,
  onChanged,
}: {
  instance: InstanceStatus
  onOpen: () => void
  onChanged: (instance: InstanceStatus) => void
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const live = isLive(instance.state)

  const power = async (action: 'start' | 'stop') => {
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

  return (
    <div className="card card--static">
      <button className="card__open" onClick={onOpen}>
        <div className="card__head">
          <span className={`status__dot status__dot--${instance.state}`} />
          <strong>{instance.name}</strong>
        </div>
        <div className="card__meta">{STATE_LABELS[instance.state]}</div>
        <div className="card__path" title={instance.directory}>
          {instance.directory}
        </div>
      </button>

      {error && <div className="card__error">{error}</div>}

      <div className="card__actions">
        <button className="btn" onClick={() => void power('start')} disabled={busy || live}>
          启动
        </button>
        <button className="btn" onClick={() => void power('stop')} disabled={busy || !live}>
          停止
        </button>
        <button className="link" onClick={onOpen}>
          控制台
        </button>
      </div>
    </div>
  )
}

/** One line about Java for the dashboard tile. */
function javaDetail(java: JavaController): string {
  if (java.installing) return `Java ${java.job?.major ?? ''}`
  if (java.overview === null) return '正在读取…'
  if (java.overview.runtimes.length > 0) return '面板已安装'
  return java.overview.system ? '仅系统自带的那个' : '这台机器上还没有 Java'
}
