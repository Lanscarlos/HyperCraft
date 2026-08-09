import { useCallback, useEffect, useState } from 'react'

import { ApiError, api } from './api'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { CoreLibraryPage } from './components/CoreLibraryPage'
import { Dashboard } from './components/Dashboard'
import { InstanceView } from './components/InstanceView'
import { JavaPage } from './components/JavaPage'
import { Login } from './components/Login'
import { HostTerminal } from './components/HostTerminal'
import { NewInstanceDialog } from './components/NewInstanceDialog'
import { PluginLibraryPage } from './components/PluginLibraryPage'
import { SettingsPage, isSettingsSection } from './components/SettingsPage'
import type { SettingsSection } from './components/SettingsPage'
import { ThemeToggle } from './components/ThemeToggle'
import type { InstanceStatus, User } from './types'
import { STATE_LABELS, isLive, mergeState } from './types'
import { useCores } from './useCores'
import { useJava } from './useJava'
import { usePlugins } from './usePlugins'
import { useTerminal } from './useTerminal'
import { updateLabel, useUpdate } from './useUpdate'

/** How often the instance list refreshes; the console pushes state instantly,
 *  this is only to keep the sidebar honest for servers you are not watching. */
const POLL_INTERVAL_MS = 5000

/** Where the app is. Panel-wide pages sit beside the per-instance view; the
 *  URL is the only place it is stored, so deep links and reloads work. */
type Route =
  | { kind: 'dashboard' }
  | { kind: 'java' }
  | { kind: 'cores' }
  | { kind: 'plugins' }
  | { kind: 'settings'; section: SettingsSection }
  | { kind: 'terminal' }
  | { kind: 'instance'; id: string }

function startsWith(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(`${prefix}/`)
}

function routeFromPath(): Route {
  const path = window.location.pathname
  const instance = path.match(/^\/i\/([^/]+)/)
  if (instance) return { kind: 'instance', id: instance[1] }

  if (startsWith(path, '/terminal')) return { kind: 'terminal' }
  if (startsWith(path, '/java')) return { kind: 'java' }
  if (startsWith(path, '/cores')) return { kind: 'cores' }
  if (startsWith(path, '/plugins')) return { kind: 'plugins' }

  const settings = path.match(/^\/settings(?:\/([^/]+))?/)
  if (settings) {
    const section = settings[1] ?? ''
    // Both used to be settings sections. Old bookmarks and links from an
    // earlier release still point at them, so they land on the pages that
    // replaced them rather than on a default tab.
    if (section === 'java') return { kind: 'java' }
    if (section === 'cores') return { kind: 'cores' }
    return { kind: 'settings', section: isSettingsSection(section) ? section : 'terminal' }
  }
  return { kind: 'dashboard' }
}

function pathOf(route: Route): string {
  switch (route.kind) {
    case 'instance':
      return `/i/${route.id}`
    case 'settings':
      return `/settings/${route.section}`
    case 'terminal':
      return '/terminal'
    case 'java':
      return '/java'
    case 'cores':
      return '/cores'
    case 'plugins':
      return '/plugins'
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
  const plugins = usePlugins(Boolean(user))
  // Not polled, unlike the three above: nothing turns the terminal on but a
  // person clicking the switch, and that path already refreshes the status.
  const terminal = useTerminal(Boolean(user))

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

  const openTerminal = useCallback(() => navigate({ kind: 'terminal' }), [navigate])
  const openJava = useCallback(() => navigate({ kind: 'java' }), [navigate])
  const openCores = useCallback(() => navigate({ kind: 'cores' }), [navigate])
  const openPlugins = useCallback(() => navigate({ kind: 'plugins' }), [navigate])

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
  const updateNotice = updateLabel(update.status)

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar__brand" onClick={() => navigate({ kind: 'dashboard' })}>
          <span className="sidebar__logo">⛏</span>
          <div>
            <strong>HyperCraft</strong>
            <small>
              {user.version}
              {updateNotice && (
                <span className="badge badge--update" title={`可更新到 ${update.status?.latestVersion}`}>
                  {updateNotice}
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
          {/* The two shared-asset pages sit at the top level rather than under
              设置: installing a runtime and downloading a core are routine
              errands, and both run as daemon jobs whose progress belongs
              somewhere always visible. */}
          <button
            className={`sidebar__link${route.kind === 'java' ? ' sidebar__link--active' : ''}`}
            onClick={openJava}
          >
            <span className="sidebar__name">Java 运行时</span>
            {java.installing && <span className="badge badge--update">安装中</span>}
          </button>
          <button
            className={`sidebar__link${route.kind === 'cores' ? ' sidebar__link--active' : ''}`}
            onClick={openCores}
          >
            <span className="sidebar__name">服务端核心</span>
            {cores.downloading && <span className="badge badge--update">下载中</span>}
          </button>
          <button
            className={`sidebar__link${route.kind === 'plugins' ? ' sidebar__link--active' : ''}`}
            onClick={openPlugins}
          >
            <span className="sidebar__name">插件库</span>
            {plugins.downloading ? (
              <span className="badge badge--update">下载中</span>
            ) : (
              plugins.updates > 0 && <span className="badge badge--update">{plugins.updates}</span>
            )}
          </button>
          {/* Only shown once the operator has switched it on; there is nothing
              useful behind this entry otherwise, and an always-visible shell
              icon invites clicking on something you did not ask for. */}
          {terminal.status?.enabled && terminal.status.supported && (
            <button
              className={`sidebar__link${route.kind === 'terminal' ? ' sidebar__link--active' : ''}`}
              onClick={openTerminal}
            >
              <span className="sidebar__name">终端</span>
            </button>
          )}
          <button
            className={`sidebar__link${route.kind === 'settings' ? ' sidebar__link--active' : ''}`}
            onClick={() => openSettings(route.kind === 'settings' ? route.section : 'terminal')}
          >
            <span className="sidebar__name">设置</span>
            {updateNotice && <span className="badge badge--update">1</span>}
          </button>
        </nav>

        <button className="btn btn--primary sidebar__new" onClick={() => setShowNew(true)}>
          + 新建实例
        </button>

        <div className="sidebar__section">
          <span>实例</span>
          {instances.length > 0 && (
            <span className="sidebar__count">
              {runningNames.length}/{instances.length}
            </span>
          )}
        </div>

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
          <ThemeToggle />
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
            terminal={terminal}
            update={update}
            onOpenTerminal={openTerminal}
            runningNames={runningNames}
          />
        ) : route.kind === 'java' ? (
          <JavaPage java={java} onOpenCores={openCores} />
        ) : route.kind === 'cores' ? (
          <CoreLibraryPage cores={cores} onOpenJava={openJava} />
        ) : route.kind === 'plugins' ? (
          <PluginLibraryPage plugins={plugins} onOpenInstances={() => select(instances[0]?.id ?? null)} />
        ) : route.kind === 'terminal' ? (
          <HostTerminal terminal={terminal} onOpenSettings={() => openSettings('terminal')} />
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
            onOpenLibrary={openCores}
            onOpenPlugins={openPlugins}
          />
        ) : (
          <Dashboard
            user={user}
            instances={instances}
            onSelect={select}
            onCreate={() => setShowNew(true)}
            onOpenJava={openJava}
            onOpenCores={openCores}
            onOpenPlugins={openPlugins}
            onOpenUpdate={() => openSettings('update')}
            onChanged={applyInstance}
            update={update}
            java={java}
            cores={cores}
            plugins={plugins}
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
            openCores()
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
