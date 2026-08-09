import { useCallback, useEffect, useState } from 'react'

import { ApiError, api } from './api'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { HostOverview } from './components/HostOverview'
import { InstanceView } from './components/InstanceView'
import { JavaPage } from './components/JavaPage'
import { Login } from './components/Login'
import { NewInstanceDialog } from './components/NewInstanceDialog'
import { UpdatePanel } from './components/UpdatePanel'
import type { InstanceStatus, User } from './types'
import { STATE_LABELS, isLive, mergeState } from './types'
import { useJava } from './useJava'
import type { JavaController } from './useJava'
import { useUpdate } from './useUpdate'
import type { UpdateController } from './useUpdate'

/** How often the instance list refreshes; the console pushes state instantly,
 *  this is only to keep the sidebar honest for servers you are not watching. */
const POLL_INTERVAL_MS = 5000

/** Where the app is. Panel-wide pages sit beside the per-instance view; the
 *  URL is the only place it is stored, so deep links and reloads work. */
type Route = { kind: 'overview' } | { kind: 'java' } | { kind: 'instance'; id: string }

function routeFromPath(): Route {
  const path = window.location.pathname
  const instance = path.match(/^\/i\/([^/]+)/)
  if (instance) return { kind: 'instance', id: instance[1] }
  if (path === '/java' || path.startsWith('/java/')) return { kind: 'java' }
  return { kind: 'overview' }
}

function pathOf(route: Route): string {
  switch (route.kind) {
    case 'instance':
      return `/i/${route.id}`
    case 'java':
      return '/java'
    default:
      return '/'
  }
}

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [checkingSession, setCheckingSession] = useState(true)
  const [instances, setInstances] = useState<InstanceStatus[]>([])
  const [route, setRoute] = useState<Route>(routeFromPath)
  const [showNew, setShowNew] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  // Polled at the app level rather than inside the overview page, so the "new
  // version" hint still appears while you are looking at an instance.
  const update = useUpdate(Boolean(user))
  // Same reason: an install keeps running in the daemon after you navigate
  // away from the Java page, and the sidebar says so while it does.
  const java = useJava(Boolean(user))

  useEffect(() => {
    api
      .me()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setCheckingSession(false))
  }, [])

  useEffect(() => {
    const onPop = () => setRoute(routeFromPath())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  const navigate = useCallback((next: Route) => {
    setRoute(next)
    window.history.pushState(null, '', pathOf(next))
  }, [])

  const select = useCallback(
    (id: string | null) => navigate(id ? { kind: 'instance', id } : { kind: 'overview' }),
    [navigate],
  )

  const refresh = useCallback(async () => {
    try {
      const fetched = await api.listInstances()
      setInstances((prev) =>
        fetched.map((item) => {
          const existing = prev.find((p) => p.id === item.id)
          return existing ? mergeState(existing, item) : item
        }),
      )
      setLoadError(null)
    } catch (err) {
      if (err instanceof ApiError && err.isUnauthorized) {
        setUser(null)
        return
      }
      setLoadError(err instanceof Error ? err.message : '加载失败')
    }
  }, [])

  useEffect(() => {
    if (!user) return
    void refresh()
    const timer = window.setInterval(() => void refresh(), POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [user, refresh])

  // Console state arrives faster than the poll; merge it in without waiting.
  const applyInstance = useCallback((updated: InstanceStatus) => {
    setInstances((prev) =>
      prev.map((item) => (item.id === updated.id ? mergeState(item, updated) : item)),
    )
  }, [])

  const signOut = async () => {
    try {
      await api.logout()
    } finally {
      setUser(null)
      setInstances([])
    }
  }

  if (checkingSession) {
    return <div className="boot">正在检查登录状态…</div>
  }
  if (!user) {
    return <Login onSignedIn={setUser} />
  }

  const selectedId = route.kind === 'instance' ? route.id : null
  const selected = instances.find((item) => item.id === selectedId) ?? null

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar__brand" onClick={() => navigate({ kind: 'overview' })}>
          <span className="sidebar__logo">⛏</span>
          <div>
            <strong>HyperCraft</strong>
            <small>
              {user.version}
              {update.status?.updateAvailable && (
                <span className="badge badge--update" title={`可更新到 ${update.status.latestVersion}`}>
                  有新版本
                </span>
              )}
            </small>
          </div>
        </div>

        <nav className="sidebar__nav">
          <button
            className={`sidebar__link${route.kind === 'overview' ? ' sidebar__link--active' : ''}`}
            onClick={() => navigate({ kind: 'overview' })}
          >
            <span className="sidebar__name">总览</span>
          </button>
          <button
            className={`sidebar__link${route.kind === 'java' ? ' sidebar__link--active' : ''}`}
            onClick={() => navigate({ kind: 'java' })}
          >
            <span className="sidebar__name">Java 运行时</span>
            {java.installing ? (
              <span className="badge badge--update">安装中</span>
            ) : (
              // Only once there is something to count: a "0" next to a feature
              // you have not used yet reads as a warning it is not.
              (java.overview?.runtimes.length ?? 0) > 0 && (
                <span className="badge">{java.overview?.runtimes.length}</span>
              )
            )}
          </button>
        </nav>

        <button className="btn btn--primary sidebar__new" onClick={() => setShowNew(true)}>
          + 新建实例
        </button>

        <nav className="sidebar__list">
          {instances.length === 0 && (
            <p className="sidebar__empty">还没有实例，先新建一个吧。</p>
          )}
          {instances.map((item) => (
            <button
              key={item.id}
              className={`sidebar__item${item.id === selectedId ? ' sidebar__item--active' : ''}`}
              onClick={() => select(item.id)}
            >
              <span className={`status__dot status__dot--${item.state}`} />
              <span className="sidebar__name">{item.name}</span>
              <span className="sidebar__state">{STATE_LABELS[item.state]}</span>
            </button>
          ))}
        </nav>

        <div className="sidebar__footer">
          <span title={user.username}>{user.username}</span>
          <button className="link" onClick={() => setShowPassword(true)}>
            修改密码
          </button>
          <button className="link" onClick={() => void signOut()}>
            退出
          </button>
        </div>
      </aside>

      <main className="main">
        {loadError && <div className="alert alert--error">{loadError}</div>}

        {route.kind === 'java' ? (
          <JavaPage java={java} />
        ) : selected ? (
          <InstanceView
            key={selected.id}
            instance={selected}
            onChanged={applyInstance}
            onDeleted={() => {
              select(null)
              void refresh()
            }}
          />
        ) : (
          <Welcome
            instances={instances}
            onSelect={select}
            onCreate={() => setShowNew(true)}
            onOpenJava={() => navigate({ kind: 'java' })}
            update={update}
            java={java}
          />
        )}
      </main>

      {showNew && (
        <NewInstanceDialog
          onCancel={() => setShowNew(false)}
          onCreated={(created) => {
            setShowNew(false)
            setInstances((prev) => [...prev, created])
            select(created.id)
          }}
        />
      )}

      {showPassword && (
        <ChangePasswordDialog
          onCancel={() => setShowPassword(false)}
          onChanged={() => {
            setShowPassword(false)
            setUser(null)
          }}
        />
      )}
    </div>
  )
}

function Welcome({
  instances,
  onSelect,
  onCreate,
  onOpenJava,
  update,
  java,
}: {
  instances: InstanceStatus[]
  onSelect: (id: string) => void
  onCreate: () => void
  onOpenJava: () => void
  update: UpdateController
  java: JavaController
}) {
  // isLive matches the backend's State.Running(), which is what decides the
  // list of servers recorded for resume — so the dialog promises exactly what
  // will happen.
  const runningNames = instances.filter((item) => isLive(item.state)).map((item) => item.name)

  return (
    <div className="welcome">
      <h1>服务器总览</h1>
      <p className="welcome__lead">
        面板以后台守护进程的方式持有服务器进程。关掉浏览器、退出登录，甚至重启路由，
        服务器都会照常运行 —— 只有停止面板本身才会（优雅地）关掉它们。
      </p>

      <UpdatePanel update={update} runningNames={runningNames} />

      <HostOverview />

      <p className="welcome__hint">
        {javaSummary(java)}
        <button className="link" onClick={onOpenJava}>
          去管理 Java 运行时
        </button>
      </p>

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
            <button key={item.id} className="card" onClick={() => onSelect(item.id)}>
              <div className="card__head">
                <span className={`status__dot status__dot--${item.state}`} />
                <strong>{item.name}</strong>
              </div>
              <div className="card__meta">{STATE_LABELS[item.state]}</div>
              <div className="card__path" title={item.directory}>
                {item.directory}
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** One line about Java for the overview, so the page that owns it is still
 *  findable from here. */
function javaSummary(java: JavaController): string {
  if (java.installing) {
    return `正在安装 Java ${java.job?.major ?? ''}…`
  }
  if (java.overview === null) {
    return 'Java 运行时'
  }
  const installed = java.overview.runtimes.length
  if (installed === 0) {
    return java.overview.system
      ? '面板还没装 Java，实例可以用系统自带的那个。'
      : '这台机器上还没有 Java，服务端跑不起来。'
  }
  return `面板已装 ${installed} 个 Java 运行时。`
}
