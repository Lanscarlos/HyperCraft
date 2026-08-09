import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, api } from './api'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { CoreLibraryPage } from './components/CoreLibraryPage'
import { Dashboard } from './components/Dashboard'
import { Icon } from './components/Icon'
import { InstanceView } from './components/InstanceView'
import { JavaPage } from './components/JavaPage'
import { Login } from './components/Login'
import { HostTerminal } from './components/HostTerminal'
import { NewInstanceDialog } from './components/NewInstanceDialog'
import { PluginDetailPage } from './components/PluginDetailPage'
import { PluginLibraryPage } from './components/PluginLibraryPage'
import { SETTINGS_SECTIONS, SettingsPage, isSettingsSection } from './components/SettingsPage'
import type { SettingsSection } from './components/SettingsPage'
import { TopBar } from './components/TopBar'
import type { Crumb } from './components/TopBar'
import type { InstanceState, InstanceStatus, User } from './types'
import { STATE_LABELS, isLive, mergeState } from './types'
import { useCores } from './useCores'
import { useJava } from './useJava'
import { useMediaQuery } from './useMediaQuery'
import { usePlugins } from './usePlugins'
import { useTerminal } from './useTerminal'
import { updateLabel, useUpdate } from './useUpdate'

/** How often the instance list refreshes; the console pushes state instantly,
 *  this is only to keep the sidebar honest for servers you are not watching. */
const POLL_INTERVAL_MS = 5000

/** Below this the sidebar cannot sit beside the content without taking a third
 *  of it, so it becomes a drawer. Kept in step with the same breakpoint in
 *  styles.css — CSS decides how it looks, this decides what the button does. */
const DRAWER_QUERY = '(max-width: 720px)'

/** Whether the desktop sidebar is folded to icons. A per-device preference,
 *  like the theme, so it lives next to it in localStorage. */
const RAIL_KEY = 'hypercraft.sidebar'

/** Below this many servers the list is short enough to read, and a search box
 *  over four rows is furniture. */
const FILTER_FROM = 6

/** Sidebar order. A crashed server is the one thing in the list that is asking
 *  for something, so it goes first even though it is not running; after that
 *  the live ones, and the deliberately-stopped ones last. Within a group the
 *  order is whatever the API gave, which is creation order — stable, so a
 *  server does not move under the pointer while you are aiming at it. */
const STATE_RANK: Record<InstanceState, number> = {
  crashed: 0,
  running: 1,
  starting: 2,
  stopping: 3,
  stopped: 4,
}

function forSidebar(
  instances: InstanceStatus[],
  query: string,
  liveOnly: boolean,
): InstanceStatus[] {
  const needle = query.trim().toLowerCase()
  const kept = instances.filter((item) => {
    if (liveOnly && !isLive(item.state)) return false
    return needle === '' || item.name.toLowerCase().includes(needle)
  })
  return kept
    .map((item, index) => ({ item, index }))
    .sort((a, b) => STATE_RANK[a.item.state] - STATE_RANK[b.item.state] || a.index - b.index)
    .map((entry) => entry.item)
}

/** Where the app is. Panel-wide pages sit beside the per-instance view; the
 *  URL is the only place it is stored, so deep links and reloads work. */
type Route =
  | { kind: 'dashboard' }
  | { kind: 'java' }
  | { kind: 'cores' }
  // The plugin library is a list; one plugin's own page hangs off it, so a
  // link to a plugin survives a reload the same way an instance link does.
  | { kind: 'plugins'; id?: string }
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
  const plugin = path.match(/^\/plugins\/([^/]+)/)
  if (plugin) return { kind: 'plugins', id: plugin[1] }
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
      return route.id ? `/plugins/${route.id}` : '/plugins'
    default:
      return '/'
  }
}

/**
 * Where you are, as the top bar says it.
 *
 * Every page scrolls its own heading out of sight, so this is the only thing on
 * screen that still answers "which instance am I in" once you are three screens
 * into a file listing. A step links only when it is somewhere you can go back
 * to — the last one is where you already are.
 */
function crumbsFor(
  route: Route,
  selected: InstanceStatus | null,
  pluginName: string | undefined,
  toDashboard: () => void,
  toPlugins: () => void,
): Crumb[] {
  switch (route.kind) {
    case 'java':
      return [{ label: 'Java 运行时' }]
    case 'cores':
      return [{ label: '服务端核心' }]
    case 'plugins':
      return pluginName
        ? [{ label: '插件库', onClick: toPlugins }, { label: pluginName }]
        : [{ label: '插件库' }]
    case 'settings': {
      const section = SETTINGS_SECTIONS.find((entry) => entry.id === route.section)
      return [{ label: '设置' }, { label: section?.label ?? '设置' }]
    }
    case 'terminal':
      return [{ label: '终端' }]
    case 'instance':
      return selected
        ? [
            { label: '仪表盘', onClick: toDashboard },
            { label: selected.name, state: selected.state },
          ]
        : [{ label: '仪表盘', onClick: toDashboard }, { label: '实例' }]
    default:
      return [{ label: '仪表盘' }]
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

  const compact = useMediaQuery(DRAWER_QUERY)
  const [query, setQuery] = useState('')
  const [liveOnly, setLiveOnly] = useState(false)
  const [navOpen, setNavOpen] = useState(false)
  const [railed, setRailed] = useState(() => window.localStorage.getItem(RAIL_KEY) === 'rail')
  const sidebarRef = useRef<HTMLElement | null>(null)
  const navToggle = useRef<HTMLButtonElement | null>(null)

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
    // A drawer covers what you just navigated to, so picking something is also
    // how you dismiss it. On a rail there is nothing to dismiss.
    setNavOpen(false)
  }, [])

  useEffect(() => {
    window.localStorage.setItem(RAIL_KEY, railed ? 'rail' : 'full')
  }, [railed])

  // Widening the window puts the sidebar back on screen for good; a drawer left
  // "open" in that state would only mean a stray scrim.
  useEffect(() => {
    if (!compact) setNavOpen(false)
  }, [compact])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setNavOpen(false)
        return
      }
      if (event.key !== '[' || event.metaKey || event.ctrlKey || event.altKey) return
      // The console command line and the host terminal are both real text
      // inputs where '[' is a character, not a shortcut.
      const target = event.target as HTMLElement | null
      if (target?.isContentEditable) return
      if (target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) return
      event.preventDefault()
      setRailed((value) => !value)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Focus follows the drawer: into it when it opens, back to the button that
  // opened it when it closes, so a keyboard never lands behind the scrim.
  const wasOpen = useRef(false)
  useEffect(() => {
    if (navOpen) sidebarRef.current?.focus()
    else if (wasOpen.current) navToggle.current?.focus()
    wasOpen.current = navOpen
  }, [navOpen])

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
  const openPlugin = useCallback(
    (id: string) => navigate({ kind: 'plugins', id }),
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
  // A plugin id from the URL that no longer exists (deleted, or a stale
  // bookmark) falls back to the list rather than to an error page.
  const openedPlugin =
    route.kind === 'plugins' && route.id
      ? (plugins.plugins.find((item) => item.id === route.id) ?? null)
      : null
  const updateNotice = updateLabel(update.status)
  const crumbs = crumbsFor(route, selected, openedPlugin?.name, () => select(null), openPlugins)
  const shown = forSidebar(instances, query, liveOnly)

  return (
    <div
      className="app"
      data-nav={compact && navOpen ? 'open' : undefined}
      data-rail={!compact && railed ? 'on' : undefined}
    >
      <aside className="sidebar" id="sidebar" ref={sidebarRef} tabIndex={-1}>
        <div className="sidebar__brand" onClick={() => navigate({ kind: 'dashboard' })}>
          <span className="sidebar__logo">⛏</span>
          <div className="sidebar__title">
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
            title="仪表盘"
            aria-current={route.kind === 'dashboard' ? 'page' : undefined}
          >
            <Icon name="dashboard" />
            <span className="sidebar__name">仪表盘</span>
          </button>
          {/* The two shared-asset pages sit at the top level rather than under
              设置: installing a runtime and downloading a core are routine
              errands, and both run as daemon jobs whose progress belongs
              somewhere always visible. */}
          <button
            className={`sidebar__link${route.kind === 'java' ? ' sidebar__link--active' : ''}`}
            onClick={openJava}
            title="Java 运行时"
            aria-current={route.kind === 'java' ? 'page' : undefined}
          >
            <Icon name="java" />
            <span className="sidebar__name">Java 运行时</span>
            {java.installing && <span className="badge badge--update">安装中</span>}
          </button>
          <button
            className={`sidebar__link${route.kind === 'cores' ? ' sidebar__link--active' : ''}`}
            onClick={openCores}
            title="服务端核心"
            aria-current={route.kind === 'cores' ? 'page' : undefined}
          >
            <Icon name="cores" />
            <span className="sidebar__name">服务端核心</span>
            {cores.downloading && <span className="badge badge--update">下载中</span>}
          </button>
          <button
            className={`sidebar__link${route.kind === 'plugins' ? ' sidebar__link--active' : ''}`}
            onClick={openPlugins}
            title="插件库"
            aria-current={route.kind === 'plugins' ? 'page' : undefined}
          >
            <Icon name="plugins" />
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
              title="终端"
              aria-current={route.kind === 'terminal' ? 'page' : undefined}
            >
              <Icon name="terminal" />
              <span className="sidebar__name">终端</span>
            </button>
          )}
          <button
            className={`sidebar__link${route.kind === 'settings' ? ' sidebar__link--active' : ''}`}
            onClick={() => openSettings(route.kind === 'settings' ? route.section : 'terminal')}
            title="设置"
            aria-current={route.kind === 'settings' ? 'page' : undefined}
          >
            <Icon name="settings" />
            <span className="sidebar__name">设置</span>
            {updateNotice && <span className="badge badge--update">1</span>}
          </button>
        </nav>

        <button
          className="btn btn--primary sidebar__new"
          onClick={() => setShowNew(true)}
          title="新建实例"
        >
          <span aria-hidden="true">+</span>
          <span className="sidebar__name">新建实例</span>
        </button>

        <div className="sidebar__section">
          <span>实例</span>
          {instances.length > 0 && (
            <span className="sidebar__count">
              {runningNames.length}/{instances.length}
            </span>
          )}
        </div>

        {/* Appears only once the list is long enough to need it. Four servers
            are read at a glance; twenty are searched. */}
        {instances.length >= FILTER_FROM && (
          <div className="sidebar__filter">
            <input
              className="sidebar__search"
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="搜索"
              aria-label="搜索实例"
            />
            <button
              className={`sidebar__only${liveOnly ? ' sidebar__only--on' : ''}`}
              onClick={() => setLiveOnly((value) => !value)}
              aria-pressed={liveOnly}
              title="只看运行中的实例"
            >
              运行中
            </button>
          </div>
        )}

        <nav className="sidebar__list">
          {instances.length === 0 && (
            <p className="sidebar__empty">还没有实例，先新建一个吧。</p>
          )}
          {instances.length > 0 && shown.length === 0 && (
            <p className="sidebar__empty">没有符合条件的实例。</p>
          )}
          {shown.map((item) => (
            <button
              key={item.id}
              className={`sidebar__item${item.id === selectedId ? ' sidebar__item--active' : ''}`}
              onClick={() => select(item.id)}
              title={`${item.name} · ${STATE_LABELS[item.state]}`}
              aria-current={item.id === selectedId ? 'page' : undefined}
            >
              {/* The initial only shows on the rail, where a column of identical
                  dots would be no way to tell six servers apart. */}
              <span className="sidebar__initial" aria-hidden="true">
                {item.name.slice(0, 1)}
              </span>
              <span className={`status__dot status__dot--${item.state}`} />
              <span className="sidebar__name">{item.name}</span>
              <span className="sidebar__state">{STATE_LABELS[item.state]}</span>
            </button>
          ))}
        </nav>
      </aside>

      {/* Only ever visible under an open drawer; it is what makes "tap the page
          to dismiss" work, and it stops clicks reaching what it covers. */}
      <div className="scrim" aria-hidden="true" onClick={() => setNavOpen(false)} />

      <div className="shell">
        <TopBar
          crumbs={crumbs}
          user={user}
          compact={compact}
          railed={railed}
          navOpen={navOpen}
          onToggleNav={() => (compact ? setNavOpen((open) => !open) : setRailed((on) => !on))}
          toggleRef={navToggle}
          onChangePassword={() => setShowPassword(true)}
          onSignOut={() => void signOut()}
        />

        <main className="main">
          {loadError && <div className="alert alert--error">{loadError}</div>}

          {route.kind === 'settings' ? (
            <SettingsPage
              section={route.section}
              onSection={openSettings}
              terminal={terminal}
              update={update}
              plugins={plugins}
              onOpenTerminal={openTerminal}
              runningNames={runningNames}
            />
          ) : route.kind === 'java' ? (
            <JavaPage java={java} onOpenCores={openCores} />
          ) : route.kind === 'cores' ? (
            <CoreLibraryPage cores={cores} onOpenJava={openJava} />
          ) : route.kind === 'plugins' ? (
            openedPlugin ? (
              <PluginDetailPage
                key={openedPlugin.id}
                item={openedPlugin}
                plugins={plugins}
                onBack={openPlugins}
              />
            ) : (
              <PluginLibraryPage
                plugins={plugins}
                onOpenPlugin={openPlugin}
                onOpenSettings={() => openSettings('plugin-source')}
              />
            )
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
      </div>

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
