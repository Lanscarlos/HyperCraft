import { useCallback, useEffect, useState } from 'react'

import { ApiError, api } from './api'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { HostOverview } from './components/HostOverview'
import { InstanceView } from './components/InstanceView'
import { JavaRuntimes } from './components/JavaRuntimes'
import { Login } from './components/Login'
import { NewInstanceDialog } from './components/NewInstanceDialog'
import type { InstanceStatus, User } from './types'
import { STATE_LABELS, mergeState } from './types'

/** How often the instance list refreshes; the console pushes state instantly,
 *  this is only to keep the sidebar honest for servers you are not watching. */
const POLL_INTERVAL_MS = 5000

/** Reads the selected instance out of the URL so deep links and reloads work. */
function instanceIdFromPath(): string | null {
  const match = window.location.pathname.match(/^\/i\/([^/]+)/)
  return match ? match[1] : null
}

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [checkingSession, setCheckingSession] = useState(true)
  const [instances, setInstances] = useState<InstanceStatus[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(instanceIdFromPath)
  const [showNew, setShowNew] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    api
      .me()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setCheckingSession(false))
  }, [])

  useEffect(() => {
    const onPop = () => setSelectedId(instanceIdFromPath())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  const select = useCallback((id: string | null) => {
    setSelectedId(id)
    window.history.pushState(null, '', id ? `/i/${id}` : '/')
  }, [])

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

  const selected = instances.find((item) => item.id === selectedId) ?? null

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar__brand" onClick={() => select(null)}>
          <span className="sidebar__logo">⛏</span>
          <div>
            <strong>HyperCraft</strong>
            <small>{user.version}</small>
          </div>
        </div>

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

        {selected ? (
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
}: {
  instances: InstanceStatus[]
  onSelect: (id: string) => void
  onCreate: () => void
}) {
  return (
    <div className="welcome">
      <h1>服务器总览</h1>
      <p className="welcome__lead">
        面板以后台守护进程的方式持有服务器进程。关掉浏览器、退出登录，甚至重启路由，
        服务器都会照常运行 —— 只有停止面板本身才会（优雅地）关掉它们。
      </p>

      <HostOverview />

      <JavaRuntimes />

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
