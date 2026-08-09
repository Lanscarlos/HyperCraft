import { useCallback, useEffect, useState } from 'react'

import { ApiError, api } from './api'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { Dashboard } from './components/Dashboard'
import { InstanceView } from './components/InstanceView'
import { Login } from './components/Login'
import { NewInstanceDialog } from './components/NewInstanceDialog'
import { SettingsPage, isSettingsSection } from './components/SettingsPage'
import type { SettingsSection } from './components/SettingsPage'
import type { InstanceStatus, User } from './types'
import { STATE_LABELS, isLive, mergeState } from './types'
import { useCores } from './useCores'
import { useJava } from './useJava'
import { useUpdate } from './useUpdate'

/** How often the instance list refreshes; the console pushes state instantly,
 *  this is only to keep the sidebar honest for servers you are not watching. */
const POLL_INTERVAL_MS = 5000

/** Where the app is. Panel-wide pages sit beside the per-instance view; the
 *  URL is the only place it is stored, so deep links and reloads work. */
type Route =
  | { kind: 'dashboard' }
  | { kind: 'settings'; section: SettingsSection }
  | { kind: 'instance'; id: string }

function routeFromPath(): Route {
  const path = window.location.pathname
  const instance = path.match(/^\/i\/([^/]+)/)
  if (instance) return { kind: 'instance', id: instance[1] }

  const settings = path.match(/^\/settings(?:\/([^/]+))?/)
  if (settings) {
    const section = settings[1] ?? ''
    return { kind: 'settings', section: isSettingsSection(section) ? section : 'java' }
  }
  // /java was the Java page's own URL before the settings section existed;
  // bookmarks and the old release's links still point at it.
  if (path === '/java' || path.startsWith('/java/')) {
    return { kind: 'settings', section: 'java' }
  }
  return { kind: 'dashboard' }
}

function pathOf(route: Route): string {
  switch (route.kind) {
    case 'instance':
      return `/i/${route.id}`
    case 'settings':
      return `/settings/${route.section}`
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

  // Polled at the app level rather than inside the pages that show them: all
  // three are long-running daemon jobs that keep going after you navigate away,
  // and the sidebar says so while they do.
  const update = useUpdate(Boolean(user))
  const java = useJava(Boolean(user))
  const cores = useCores(Boolean(user))

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
    (id: string | null) => navigate(id ? { kind: 'instance', id } : { kind: 'dashboard' }),
    [navigate],
  )

  const openSettings = useCallback(
    (section: SettingsSection) => navigate({ kind: 'settings', section }),
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
  // Matches the backend's State.Running(), which is what decides the list of
  // servers recorded for resume — so the update dialog promises exactly what
  // will happen.
  const runningNames = instances.filter((item) => isLive(item.state)).map((item) => item.name)
  const settingsBusy = java.installing || cores.downloading

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar__brand" onClick={() => navigate({ kind: 'dashboard' })}>
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
            className={`sidebar__link${route.kind === 'dashboard' ? ' sidebar__link--active' : ''}`}
            onClick={() => navigate({ kind: 'dashboard' })}
          >
            <span className="sidebar__name">仪表盘</span>
          </button>
          <button
            className={`sidebar__link${route.kind === 'settings' ? ' sidebar__link--active' : ''}`}
            onClick={() => openSettings(route.kind === 'settings' ? route.section : 'java')}
          >
            <span className="sidebar__name">设置</span>
            {settingsBusy ? (
              <span className="badge badge--update">
                {java.installing ? '安装中' : '下载中'}
              </span>
            ) : (
              update.status?.updateAvailable && <span className="badge badge--update">1</span>
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

        {route.kind === 'settings' ? (
          <SettingsPage
            section={route.section}
            onSection={openSettings}
            java={java}
            cores={cores}
            update={update}
            runningNames={runningNames}
          />
        ) : selected ? (
          <InstanceView
            key={selected.id}
            instance={selected}
            cores={cores}
            onChanged={applyInstance}
            onDeleted={() => {
              select(null)
              void refresh()
            }}
            onOpenLibrary={() => openSettings('cores')}
          />
        ) : (
          <Dashboard
            user={user}
            instances={instances}
            onSelect={select}
            onCreate={() => setShowNew(true)}
            onOpenSettings={openSettings}
            onChanged={applyInstance}
            update={update}
            java={java}
            cores={cores}
          />
        )}
      </main>

      {showNew && (
        <NewInstanceDialog
          cores={cores}
          onCancel={() => setShowNew(false)}
          onCreated={(created) => {
            setShowNew(false)
            setInstances((prev) => [...prev, created])
            select(created.id)
          }}
          onOpenLibrary={() => {
            setShowNew(false)
            openSettings('cores')
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
